package engine

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
)

type openAPIImport struct {
	root            Object
	workspace       string
	models          []Object
	variables       map[string]string
	authVariables   map[string]string
	serverOverrides map[string]string
	warnings        []string
	baseURL         string
	servers         []Object
	origin          string
}
type openAPIAuth struct {
	kind                string
	values              Object
	headers, parameters []any
}

func parseOpenAPI(root Object) ([]Object, error) {
	return parseOpenAPIAt(root, "")
}
func parseOpenAPIAt(root Object, origin string) ([]Object, error) {
	version := importText(root["openapi"])
	swagger := importText(root["swagger"])
	if !strings.HasPrefix(version, "3.") && version != "3" && swagger != "2" && swagger != "2.0" {
		return nil, errors.New("choose an OpenAPI 3.x or Swagger 2.0 document")
	}
	state := &openAPIImport{root: root, workspace: "import_workspace", variables: map[string]string{}, authVariables: map[string]string{}, serverOverrides: map[string]string{}, origin: origin}
	state.servers = objects(array(root, "servers"))
	if len(state.servers) > 0 {
		state.baseURL = state.serverURL(state.servers[0])
	}
	if len(state.servers) == 0 {
		base := str(root, "basePath")
		if host := str(root, "host"); host != "" {
			base = cmp.Or(firstImportString(array(root, "schemes")), "https") + "://" + host + "/" + strings.TrimLeft(base, "/")
		}
		if base == "" && swagger == "" {
			base = "/"
		}
		state.baseURL = state.serverURL(Object{"url": base})
		state.servers = []Object{{"description": "Default", "url": state.baseURL}}
	}
	state.variables["baseUrl"] = state.baseURL
	state.variables["baseUrlOrigin"] = openAPIOrigin(state.baseURL)
	info := obj(root, "info")
	workspace := Object{"model": "workspace", "id": state.workspace, "name": cmp.Or(str(info, "title"), "OpenAPI Import"), "description": openAPIInfoDescription(info)}
	state.models = []Object{workspace}
	rootAuth := state.authentication(root["security"])
	workspace["authenticationType"], workspace["authentication"] = nilIfBlank(rootAuth.kind), rootAuth.values
	folders := map[string]string{}
	folderFor := func(name, description string) string {
		if name == "" {
			return ""
		}
		if id := folders[name]; id != "" {
			return id
		}
		id := stableImportID("folder", "tag:"+name)
		folders[name] = id
		state.models = append(state.models, Object{"model": "folder", "id": id, "workspaceId": state.workspace, "name": name, "description": description, "sortPriority": len(folders) - 1})
		return id
	}
	for _, tag := range objects(array(root, "tags")) {
		folderFor(str(tag, "name"), openAPIDocsDescription(tag))
	}
	requests := []Object{}
	routes := map[string]string{}
	usedIDs := map[string]bool{}
	for _, path := range slices.Sorted(maps.Keys(obj(root, "paths"))) {
		pathItem := importRef(root, obj(obj(root, "paths"), path))
		operations := map[string]Object{}
		for _, method := range []string{"get", "put", "post", "delete", "patch", "options", "head", "trace", "query"} {
			if op, ok := pathItem[method].(map[string]any); ok {
				operations[strings.ToUpper(method)] = importRef(root, op)
			}
		}
		for method, value := range obj(pathItem, "additionalOperations") {
			if op, ok := value.(map[string]any); ok && operations[strings.ToUpper(method)] == nil {
				operations[strings.ToUpper(method)] = importRef(root, op)
			}
		}
		for _, method := range slices.Sorted(maps.Keys(operations)) {
			op := operations[method]
			parameters := openAPIParameters(root, array(pathItem, "parameters"), array(op, "parameters"))
			key := cmp.Or(str(op, "operationId"), method+" "+path)
			id := stableImportID("http_request", key)
			if usedIDs[id] {
				return nil, fmt.Errorf("OpenAPI has duplicate operation ID %q", key)
			}
			usedIDs[id] = true
			name := cmp.Or(str(op, "summary"), str(op, "operationId"), openAPIFirstLine(str(op, "description")), method+" "+path)
			request := Object{"model": "http_request", "id": id, "workspaceId": state.workspace, "folderId": nilIfBlank(folderFor(firstImportString(array(op, "tags")), "")), "name": name, "method": method, "sortPriority": len(requests)}
			auth := openAPIAuth{values: Object{}, headers: rootAuth.headers, parameters: rootAuth.parameters}
			if security, exists := op["security"]; exists {
				auth = state.authentication(security)
			}
			request["authenticationType"], request["authentication"] = nilIfBlank(auth.kind), auth.values
			requestPath, params, headers := state.parameterRows(path, parameters)
			base := "${[ baseUrl ]}"
			for _, level := range []Object{op, pathItem} {
				if servers := objects(array(level, "servers")); len(servers) > 0 {
					baseURL := state.serverURL(servers[0])
					variable := state.serverOverrides[baseURL]
					if variable == "" {
						variable = fmt.Sprintf("serverUrl%d", len(state.serverOverrides)+1)
						state.serverOverrides[baseURL] = variable
						state.variables[variable] = baseURL
					}
					base = "${[ " + variable + " ]}"
					break
				}
			}
			request["url"] = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(requestPath, "/")
			request["urlParameters"] = append(params, auth.parameters...)
			body, bodyType, bodyHeaders := state.requestBody(op, parameters)
			request["body"], request["bodyType"] = body, nilIfBlank(bodyType)
			request["headers"] = openAPIMergeHeaders(auth.headers, headers, bodyHeaders, state.acceptHeader(op))
			contentType := bodyType
			for _, h := range objects(bodyHeaders) {
				if strings.EqualFold(str(h, "name"), "Content-Type") {
					contentType = str(h, "value")
				}
			}
			request["description"] = state.operationDescription(op, parameters, contentType)
			routes[id] = method + " " + path
			requests = append(requests, request)
		}
	}
	if len(requests) == 0 {
		return nil, errors.New("OpenAPI document contains no HTTP operations")
	}
	counts := map[string]int{}
	for _, request := range requests {
		counts[str(request, "folderId")+"\n"+str(request, "name")]++
	}
	for _, request := range requests {
		if counts[str(request, "folderId")+"\n"+str(request, "name")] > 1 {
			request["name"] = str(request, "name") + " (" + routes[str(request, "id")] + ")"
		}
	}
	state.models = append(state.models, requests...)
	variables := []any{}
	for _, name := range slices.Sorted(maps.Keys(state.variables)) {
		variables = append(variables, Object{"name": name, "value": state.variables[name], "enabled": true})
	}
	state.models = append(state.models, Object{"model": "environment", "id": "base_environment", "workspaceId": state.workspace, "parentModel": "workspace", "parentId": state.workspace, "name": "Global Variables", "variables": variables})
	names := map[string]int{}
	for i, server := range state.servers {
		name := cmp.Or(strings.TrimSpace(str(server, "description")), fmt.Sprintf("Server %d", i+1))
		names[name]++
		if names[name] > 1 {
			name = fmt.Sprintf("%s %d", name, names[name])
		}
		base := state.serverURL(server)
		state.models = append(state.models, Object{"model": "environment", "id": stableImportID("environment", fmt.Sprintf("server:%d", i)), "workspaceId": state.workspace, "parentModel": "environment", "parentId": nil, "name": name, "sortPriority": i + 1, "variables": []any{Object{"name": "baseUrl", "value": base, "enabled": true}, Object{"name": "baseUrlOrigin", "value": openAPIOrigin(base), "enabled": true}}})
	}
	if len(state.warnings) > 0 {
		workspace["importWarnings"] = state.warnings
	}
	return state.models, nil
}

func firstImportString(values []any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func openAPIServerURL(server Object) string {
	base := str(server, "url")
	for name, raw := range obj(server, "variables") {
		if variable, ok := raw.(map[string]any); ok {
			base = strings.ReplaceAll(base, "{"+name+"}", importText(variable["default"]))
		}
	}
	return strings.TrimRight(base, "/")
}
func (s *openAPIImport) serverURL(server Object) string {
	raw := openAPIServerURL(server)
	if !strings.HasPrefix(s.origin, "http://") && !strings.HasPrefix(s.origin, "https://") {
		return raw
	}
	base, err := url.Parse(s.origin)
	if err != nil {
		return raw
	}
	if raw == "" {
		raw = "/"
	}
	reference, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.TrimRight(base.ResolveReference(reference).String(), "/")
}
func openAPIOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme == "" {
		return "//" + u.Host
	}
	return u.Scheme + "://" + u.Host
}
func openAPIFirstLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len([]rune(line)) <= 100 {
				return line
			}
			return ""
		}
	}
	return ""
}
func openAPIDocsDescription(model Object) string {
	parts := []string{}
	if description := str(model, "description"); description != "" {
		parts = append(parts, description)
	}
	if docs := obj(model, "externalDocs"); str(docs, "url") != "" {
		parts = append(parts, cmp.Or(str(docs, "description"), "External docs")+": "+str(docs, "url"))
	}
	return strings.Join(parts, "\n\n")
}
func openAPIInfoDescription(info Object) string {
	parts := []string{str(info, "description")}
	if value := str(info, "termsOfService"); value != "" {
		parts = append(parts, "Terms of service: "+value)
	}
	if value := str(obj(info, "contact"), "email"); value != "" {
		parts = append(parts, "Contact: "+value)
	}
	if license := obj(info, "license"); str(license, "name") != "" {
		parts = append(parts, "License: "+str(license, "name")+" "+str(license, "url"))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
func (s *openAPIImport) operationDescription(op Object, parameters []Object, bodyType string) string {
	parts := []string{}
	if boolean(op, "deprecated") {
		parts = append(parts, "Deprecated.")
	}
	if value := cmp.Or(str(op, "description"), str(op, "summary")); value != "" {
		parts = append(parts, value)
	}
	if id := str(op, "operationId"); id != "" {
		parts = append(parts, "Operation ID: "+id)
	}
	lines := []string{}
	for _, p := range parameters[:min(len(parameters), 40)] {
		required := ""
		if boolean(p, "required") {
			required = ", required"
		}
		lines = append(lines, "- "+str(p, "name")+" ("+str(p, "in")+required+"): "+str(p, "description"))
	}
	if len(lines) > 0 {
		parts = append(parts, "Parameters:\n"+strings.Join(lines, "\n"))
	}
	if body := importRef(s.root, obj(op, "requestBody")); len(body) > 0 {
		parts = append(parts, strings.TrimSpace("Request body:\n"+str(body, "description")+"\nSelected content type: "+bodyType+"\nAvailable content types: "+strings.Join(slices.Sorted(maps.Keys(obj(body, "content"))), ", ")))
	}
	lines = nil
	for _, status := range slices.Sorted(maps.Keys(obj(op, "responses"))) {
		if len(lines) == 40 {
			break
		}
		response := importRef(s.root, obj(obj(op, "responses"), status))
		lines = append(lines, "- "+status+": "+str(response, "description"))
	}
	if len(lines) > 0 {
		parts = append(parts, "Responses:\n"+strings.Join(lines, "\n"))
	}
	if docs := obj(op, "externalDocs"); str(docs, "url") != "" {
		parts = append(parts, cmp.Or(str(docs, "description"), "External docs")+": "+str(docs, "url"))
	}
	return strings.Join(parts, "\n\n")
}
