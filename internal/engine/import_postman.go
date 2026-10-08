package engine

import (
	"cmp"
	"fmt"
	"strings"
)

func parsePostman(root Object) []Object {
	workspace := "import_workspace"
	info := obj(root, "info")
	name := str(info, "name")
	if name == "" {
		name = "Imported Collection"
	}
	w := Object{"model": "workspace", "id": workspace, "name": name}
	w["description"] = postmanDescription(info["description"])
	w["importScripts"] = array(root, "event")
	postmanAuth(w, obj(root, "auth"))
	result := []Object{w}
	vars := []any{}
	for _, v := range objects(array(root, "variable")) {
		vars = append(vars, Object{"name": str(v, "key"), "value": importText(v["value"]), "enabled": !boolean(v, "disabled")})
	}
	result = append(result, Object{"model": "environment", "id": "base_environment", "workspaceId": workspace, "name": "Global Variables", "parentModel": "workspace", "parentId": workspace, "variables": vars})
	seenKeys := map[string]int{}
	var visit func([]any, string)
	visit = func(items []any, parent string) {
		for index, item := range objects(items) {
			key := cmp.Or(str(item, "id"), parent+"/"+str(item, "name"))
			seenKeys[key]++
			key = fmt.Sprintf("%s#%d", key, seenKeys[key])
			id := stableImportID("http_request", key)
			if children, ok := item["item"].([]any); ok {
				folder := Object{"model": "folder", "id": id, "workspaceId": workspace, "folderId": nilIfBlank(parent), "name": str(item, "name"), "description": postmanDescription(item["description"]), "sortPriority": index, "importScripts": array(item, "event")}
				postmanAuth(folder, obj(item, "auth"))
				if parent == "" && folder["authenticationType"] == nil {
					folder["authenticationType"], folder["authentication"] = w["authenticationType"], w["authentication"]
				}
				result = append(result, folder)
				if variables := postmanVariables(array(item, "variable")); len(variables) > 0 {
					result = append(result, Object{"model": "environment", "id": stableImportID("environment", key), "workspaceId": workspace, "parentModel": "folder", "parentId": id, "name": "Folder Variables", "variables": variables})
				}
				visit(children, id)
				continue
			}
			req := obj(item, "request")
			if raw, ok := item["request"].(string); ok {
				req = Object{"url": raw, "method": "GET"}
			}
			rawURL := str(req, "url")
			if rawURL == "" {
				rawURL = str(obj(req, "url"), "raw")
			}
			urlObject := obj(req, "url")
			if rawURL == "" && len(urlObject) > 0 {
				rawURL = cmp.Or(str(urlObject, "protocol"), "https") + "://" + postmanURLPart(urlObject["host"], ".")
				if port := str(urlObject, "port"); port != "" {
					rawURL += ":" + port
				}
				rawURL += "/" + postmanURLPart(urlObject["path"], "/")
			}
			params := []any{}
			if query, exists := urlObject["query"]; exists {
				rawURL, _, _ = strings.Cut(rawURL, "?")
				rows, _ := query.([]any)
				params = append(params, postmanVariables(rows)...)
			}
			for _, v := range objects(postmanVariables(array(urlObject, "variable"))) {
				v["name"] = ":" + str(v, "name")
				params = append(params, v)
			}
			m := Object{"model": "http_request", "id": id, "workspaceId": workspace, "folderId": nilIfBlank(parent), "name": str(item, "name"), "url": rawURL, "urlParameters": params, "method": cmp.Or(str(req, "method"), "GET"), "description": postmanDescription(req["description"]), "sortPriority": index, "importScripts": array(item, "event")}
			headers := []any{}
			for _, h := range objects(array(req, "header")) {
				headers = append(headers, Object{"name": str(h, "key"), "value": str(h, "value"), "enabled": !boolean(h, "disabled")})
			}
			m["headers"] = headers
			body := obj(req, "body")
			switch str(body, "mode") {
			case "raw":
				m["bodyType"] = "text/plain"
				if str(obj(obj(body, "options"), "raw"), "language") == "json" {
					m["bodyType"] = "application/json"
				}
				m["body"] = Object{"text": str(body, "raw")}
			case "urlencoded", "formdata":
				kind := "application/x-www-form-urlencoded"
				if str(body, "mode") == "formdata" {
					kind = "multipart/form-data"
				}
				m["bodyType"] = kind
				form := []any{}
				for _, p := range objects(array(body, str(body, "mode"))) {
					entry := Object{"name": str(p, "key"), "value": str(p, "value"), "enabled": !boolean(p, "disabled")}
					if contentType := str(p, "contentType"); contentType != "" {
						entry["contentType"] = contentType
					}
					if str(p, "type") == "file" {
						delete(entry, "value")
						entry["type"] = "file"
						if paths := array(p, "src"); len(paths) > 0 {
							for _, path := range paths {
								if value, ok := path.(string); ok {
									file := clone(entry)
									file["file"] = value
									form = append(form, file)
								}
							}
							continue
						}
						entry["file"] = str(p, "src")
					}
					form = append(form, entry)
				}
				m["body"] = Object{"form": form}
			case "graphql":
				m["bodyType"] = "graphql"
				m["body"] = obj(body, "graphql")
			case "file":
				m["bodyType"] = "binary"
				m["body"] = Object{"filePath": str(obj(body, "file"), "src")}
			}
			postmanAuth(m, obj(req, "auth"))
			if parent == "" && m["authenticationType"] == nil {
				m["authenticationType"], m["authentication"] = w["authenticationType"], w["authentication"]
			}
			result = append(result, m)
		}
	}
	visit(array(root, "item"), "")
	return result
}

func importText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return jsonString(value)
}
func postmanDescription(value any) string {
	if m, ok := value.(map[string]any); ok {
		return str(m, "content")
	}
	return importText(value)
}
func postmanVariables(rows []any) []any {
	result := []any{}
	for _, row := range objects(rows) {
		result = append(result, Object{"name": str(row, "key"), "value": importText(row["value"]), "enabled": !boolean(row, "disabled")})
	}
	return result
}
func postmanURLPart(value any, separator string) string {
	if text, ok := value.(string); ok {
		return text
	}
	parts := []string{}
	if values, ok := value.([]any); ok {
		for _, v := range values {
			parts = append(parts, importText(v))
		}
	}
	return strings.Join(parts, separator)
}
func postmanAuth(model, auth Object) {
	kind := str(auth, "type")
	if kind == "" || kind == "inherit" {
		return
	}
	values := Object{}
	for _, v := range objects(array(auth, kind)) {
		values[str(v, "key")] = importText(v["value"])
	}
	switch kind {
	case "noauth":
		kind = "none"
	case "awsv4":
		values["accessKeyId"], values["secretAccessKey"] = values["accessKey"], values["secretKey"]
		delete(values, "accessKey")
		delete(values, "secretKey")
	case "oauth2":
		for old, new := range map[string]string{"authUrl": "authorizationUrl", "callbackUrl": "redirectUri", "grant_type": "grantType"} {
			if value := values[old]; value != nil {
				values[new] = value
				delete(values, old)
			}
		}
	}
	model["authenticationType"], model["authentication"] = kind, values
}
