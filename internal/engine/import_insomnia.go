package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"yeek/locales"
)

type insomniaResources struct {
	workspaces, environments, folders, httpRequests, grpcRequests, websocketRequests []Object
}

func (r *insomniaResources) models() []Object {
	var out []Object
	for _, list := range [][]Object{r.workspaces, r.environments, r.folders, r.httpRequests, r.grpcRequests, r.websocketRequests} {
		out = append(out, list...)
	}
	return out
}

func parseInsomniaImport(root Object) []Object {
	resources := convertInsomniaV5(root)
	if resources == nil {
		resources = convertInsomniaV4(root)
	}
	if resources == nil {
		return nil
	}
	models := resources.models()
	for _, m := range models {
		if str(m, "model") == "http_request" && str(m, "bodyType") == "other" {
			m["bodyType"] = "text/plain"
		}
	}
	return models
}

func insomniaID(id string) string {
	if strings.HasPrefix(id, "GENERATE_ID::") {
		return id
	}
	return "GENERATE_ID::" + id
}

func insomniaDate(value any) any {
	switch v := value.(type) {
	case float64:
		if v == 0 || math.IsNaN(v) {
			return nil
		}
		return time.UnixMilli(int64(v)).UTC().Format("2006-01-02T15:04:05.000")
	case int:
		return insomniaDate(float64(v))
	case int64:
		return insomniaDate(float64(v))
	case uint64:
		return insomniaDate(float64(v))
	case string:
		if v == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999", "2006-01-02"} {
			if t, err := time.Parse(layout, v); err == nil {
				return t.UTC().Format("2006-01-02T15:04:05.000")
			}
		}
	}
	return nil
}

func jsString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return jsNumber(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	}
	return ""
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	case int:
		return v != 0
	case int64:
		return v != 0
	case uint64:
		return v != 0
	}
	return true
}

func set(m Object, key string, value any, defined bool) {
	if defined {
		m[key] = value
	}
}

func insomniaMeta(r Object, key, fallback string) (any, bool) {
	if v, ok := obj(r, "meta")[key]; ok && v != nil {
		return v, true
	}
	v, ok := r[fallback]
	return v, ok && v != nil
}

func insomniaDescription(m Object, value any) {
	if truthy(value) {
		m["description"] = value
	}
}

func insomniaDates(m Object, created, updated any) {
	if d := insomniaDate(created); d != nil {
		m["createdAt"] = d
	}
	if d := insomniaDate(updated); d != nil {
		m["updatedAt"] = d
	}
}

func insomniaFolderID(parentID, workspaceID string) any {
	if parentID == workspaceID {
		return nil
	}
	return insomniaID(parentID)
}

func insomniaPairs(rows any, filter bool) []any {
	out := []any{}
	for _, row := range objects(asArray(rows)) {
		name, value := row["name"], row["value"]
		if name == nil {
			name = ""
		}
		if value == nil {
			value = ""
		}
		pair := Object{"enabled": !truthy(row["disabled"]), "name": jsString(name), "value": jsString(value)}
		if filter && pair["name"] == "" && pair["value"] == "" {
			continue
		}
		out = append(out, pair)
	}
	return out
}

func asArray(value any) []any {
	v, _ := value.([]any)
	return v
}

func insomniaHeaders(r Object) []any { return insomniaPairs(r["headers"], true) }

func insomniaAuthentication(m, r Object) {
	auth := obj(r, "authentication")
	var kind any
	values := Object{}
	switch str(auth, "type") {
	case "bearer":
		kind = "bearer"
		set(values, "token", auth["token"], auth["token"] != nil)
	case "basic":
		kind = "basic"
		set(values, "username", auth["username"], auth["username"] != nil)
		set(values, "password", auth["password"], auth["password"] != nil)
	}
	m["authenticationType"], m["authentication"] = kind, values
}

func insomniaBodyAndHeaders(m, r Object) {
	headers := insomniaHeaders(r)
	rawBody := obj(r, "body")
	body, bodyType := insomniaBody(rawBody)
	mimeType := ""
	if s, ok := rawBody["mimeType"].(string); ok {
		mimeType = strings.TrimSpace(s)
	}
	contentType := mimeType
	if bodyType == "graphql" {
		contentType = "application/json"
	}
	if bodyType != nil && mimeType != "" && !slices.ContainsFunc(objects(headers), func(h Object) bool { return strings.EqualFold(str(h, "name"), "content-type") }) {
		headers = append(headers, Object{"enabled": true, "name": "Content-Type", "value": contentType})
	}
	m["body"], m["bodyType"], m["headers"] = body, bodyType, headers
}

func insomniaBody(rawBody Object) (Object, any) {
	mimeType := ""
	if s, ok := rawBody["mimeType"].(string); ok {
		mimeType = strings.TrimSpace(s)
	}
	normalized, _, _ := strings.Cut(mimeType, ";")
	normalized = strings.ToLower(normalized)
	text := func() any {
		if v, ok := rawBody["text"]; ok && v != nil {
			return v
		}
		return ""
	}
	switch {
	case normalized == "application/octet-stream":
		path := rawBody["fileName"]
		if path == nil {
			path = ""
		}
		return Object{"filePath": path}, "binary"
	case normalized == "application/x-www-form-urlencoded":
		return Object{"form": insomniaPairs(rawBody["params"], false)}, "application/x-www-form-urlencoded"
	case normalized == "multipart/form-data":
		form := insomniaPairs(rawBody["params"], false)
		for i, param := range objects(asArray(rawBody["params"])) {
			file := param["fileName"]
			form[i].(Object)["file"] = file
		}
		return Object{"form": form}, "multipart/form-data"
	case normalized == "application/graphql":
		body, _ := rawBody["text"].(string)
		var parsed any
		if err := json.Unmarshal([]byte(body), &parsed, jsontext.AllowDuplicateNames(true)); err == nil {
			if envelope, ok := parsed.(map[string]any); ok {
				if query, ok := envelope["query"].(string); ok {
					result := Object{"query": query, "variables": graphQLVariables(body, envelope["variables"])}
					if name, ok := envelope["operationName"].(string); ok {
						result["operationName"] = name
					}
					return result, "graphql"
				}
			}
		} else if recovered := recoverGraphQLEnvelope(body); recovered != nil {
			return recovered, "graphql"
		}
		return Object{"query": body, "variables": ""}, "graphql"
	case normalized == "application/json" || strings.HasSuffix(normalized, "+json"):
		return Object{"text": text()}, "application/json"
	case normalized == "text/xml" || normalized == "application/xml" || strings.HasSuffix(normalized, "+xml"):
		return Object{"text": text()}, "text/xml"
	}
	if s, ok := rawBody["text"].(string); ok {
		return Object{"text": s}, "other"
	}
	return Object{}, nil
}

func graphQLVariables(envelope string, variables any) string {
	switch v := variables.(type) {
	case string:
		return v
	case nil:
		return ""
	}
	raw := rawJSONMember(envelope, "variables")
	value := jsontext.Value(raw)
	if raw == nil || value.Indent(jsontext.WithIndentPrefix(""), jsontext.WithIndent("  ")) != nil {
		data, _ := json.Marshal(variables, jsontext.WithIndent("  "))
		return string(data)
	}
	return jsStringify(value)
}

func rawJSONMember(text, key string) []byte {
	dec := jsontext.NewDecoder(strings.NewReader(text), jsontext.AllowDuplicateNames(true))
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != '{' {
		return nil
	}
	var found []byte
	for dec.PeekKind() != '}' {
		token, err := dec.ReadToken()
		if err != nil {
			return nil
		}
		name := token.String()
		value, err := dec.ReadValue()
		if err != nil {
			return nil
		}
		if name == key {
			found = bytes.Clone(value)
		}
	}
	return found
}

func jsStringify(value jsontext.Value) string {
	dec := jsontext.NewDecoder(bytes.NewReader(value), jsontext.AllowDuplicateNames(true))
	node, err := readJSNode(dec)
	if err != nil {
		return string(value)
	}
	var b strings.Builder
	node.write(&b, 0)
	return b.String()
}

type jsNode struct {
	kind   jsontext.Kind
	scalar string
	keys   []string
	items  []*jsNode
}

func readJSNode(dec *jsontext.Decoder) (*jsNode, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	n := &jsNode{kind: tok.Kind()}
	switch n.kind {
	case '{':
		index := map[string]int{}
		for dec.PeekKind() != '}' {
			name, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			key := name.String()
			item, err := readJSNode(dec)
			if err != nil {
				return nil, err
			}
			if at, seen := index[key]; seen {
				n.items[at] = item
				continue
			}
			index[key] = len(n.keys)
			n.keys = append(n.keys, key)
			n.items = append(n.items, item)
		}
		_, err = dec.ReadToken()
	case '[':
		for dec.PeekKind() != ']' {
			item, err := readJSNode(dec)
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, item)
		}
		_, err = dec.ReadToken()
	case '"':
		data, _ := json.Marshal(tok.String(), jsontext.EscapeForHTML(false), jsontext.EscapeForJS(false))
		n.scalar = string(data)
	case '0':
		f, _ := strconv.ParseFloat(tok.String(), 64)
		n.scalar = jsNumber(f)
	default:
		n.scalar = tok.String()
	}
	return n, err
}

func (n *jsNode) write(b *strings.Builder, depth int) {
	if n.kind != '{' && n.kind != '[' {
		b.WriteString(n.scalar)
		return
	}
	open, closing := "{", "}"
	if n.kind == '[' {
		open, closing = "[", "]"
	}
	b.WriteString(open)
	for i, item := range n.items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("\n" + strings.Repeat("  ", depth+1))
		if n.kind == '{' {
			key, _ := json.Marshal(n.keys[i], jsontext.EscapeForHTML(false), jsontext.EscapeForJS(false))
			b.Write(key)
			b.WriteString(": ")
		}
		item.write(b, depth+1)
	}
	if len(n.items) > 0 {
		b.WriteString("\n" + strings.Repeat("  ", depth))
	}
	b.WriteString(closing)
}

func recoverGraphQLEnvelope(text string) Object {
	members := readTopLevelMembers(text)
	if members == nil {
		return nil
	}
	query, ok := parseJSONString(members["query"])
	if !ok {
		return nil
	}
	variables := members["variables"]
	if variables == "null" {
		variables = ""
	}
	result := Object{"query": query, "variables": variables}
	if name, ok := parseJSONString(members["operationName"]); ok {
		result["operationName"] = name
	}
	return result
}

func parseJSONString(raw string) (string, bool) {
	if !strings.HasPrefix(raw, `"`) {
		return "", false
	}
	var s string
	if json.Unmarshal([]byte(raw), &s) != nil {
		return "", false
	}
	return s, true
}

func jsSpace(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' }

func readTopLevelMembers(text string) map[string]string {
	t := []rune(strings.TrimFunc(text, jsSpace))
	if len(t) == 0 || t[0] != '{' {
		return nil
	}
	members := map[string]string{}
	at := func(i int) rune {
		if i < len(t) {
			return t[i]
		}
		return 0
	}
	offset := 1
	for offset < len(t) {
		for offset < len(t) && (jsSpace(t[offset]) || t[offset] == ',') {
			offset++
		}
		if at(offset) == '}' {
			return members
		}
		keyEnd, ok := scanJSONString(t, offset)
		if !ok {
			return nil
		}
		key, ok := parseJSONString(string(t[offset:keyEnd]))
		if !ok {
			return nil
		}
		offset = keyEnd
		for offset < len(t) && jsSpace(t[offset]) {
			offset++
		}
		if at(offset) != ':' {
			return nil
		}
		offset++
		for offset < len(t) && jsSpace(t[offset]) {
			offset++
		}
		valueEnd, ok := scanJSONValue(t, offset)
		if !ok {
			return nil
		}
		members[key] = strings.TrimFunc(string(t[offset:valueEnd]), jsSpace)
		offset = valueEnd
	}
	return nil
}

func scanJSONString(t []rune, start int) (int, bool) {
	if start >= len(t) || t[start] != '"' {
		return 0, false
	}
	for i := start + 1; i < len(t); i++ {
		switch t[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

func scanJSONValue(t []rune, start int) (int, bool) {
	depth := 0
	for i := start; i < len(t); i++ {
		switch c := t[i]; {
		case c == '"':
			end, ok := scanJSONString(t, i)
			if !ok {
				return 0, false
			}
			i = end - 1
		case c == '{' || c == '[':
			depth++
		case c == '}' || c == ']':
			if depth == 0 {
				return i, true
			}
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case depth == 0 && c == ',':
			return i, true
		}
	}
	return 0, false
}

func insomniaEnvironmentVariables(data any) []any {
	vars := []any{}
	m, _ := data.(map[string]any)
	for _, name := range orderedKeys(m) {
		vars = append(vars, Object{"enabled": true, "name": name, "value": jsString(m[name])})
	}
	return vars
}

func convertInsomniaV4(parsed Object) *insomniaResources {
	list, ok := parsed["resources"].([]any)
	if !ok {
		return nil
	}
	resources := &insomniaResources{}
	all := objects(list)
	for _, w := range all {
		if str(w, "_type") != "workspace" {
			continue
		}
		wid := str(w, "_id")
		workspace := Object{"id": insomniaID(wid), "model": "workspace"}
		insomniaDates(workspace, w["created"], w["updated"])
		set(workspace, "name", w["name"], w["name"] != nil)
		insomniaDescription(workspace, w["description"])
		resources.workspaces = append(resources.workspaces, workspace)
		for _, e := range all {
			if str(e, "_type") == "environment" {
				resources.environments = append(resources.environments, insomniaV4Environment(e, wid))
			}
		}
		var nextFolder func(parent string)
		nextFolder = func(parent string) {
			for _, child := range all {
				if str(child, "parentId") != parent || child["parentId"] == nil {
					continue
				}
				switch str(child, "_type") {
				case "request_group":
					resources.folders = append(resources.folders, insomniaV4Folder(child, wid))
					nextFolder(str(child, "_id"))
				case "request":
					resources.httpRequests = append(resources.httpRequests, insomniaV4Request(child, wid))
				case "grpc_request":
					resources.grpcRequests = append(resources.grpcRequests, insomniaV4Grpc(child, wid))
				}
			}
		}
		nextFolder(wid)
	}
	convertInsomniaTemplates(resources)
	return resources
}

func insomniaV4Request(r Object, wid string) Object {
	id, _ := insomniaMeta(r, "id", "_id")
	m := Object{"id": insomniaID(jsString(id)), "workspaceId": insomniaID(wid), "folderId": insomniaFolderID(str(r, "parentId"), wid), "model": "http_request"}
	insomniaDates(m, r["created"], r["modified"])
	set(m, "sortPriority", r["metaSortKey"], r["metaSortKey"] != nil)
	set(m, "name", r["name"], r["name"] != nil)
	insomniaDescription(m, r["description"])
	set(m, "url", r["url"], r["url"] != nil)
	m["urlParameters"] = insomniaPairs(r["parameters"], false)
	insomniaBodyAndHeaders(m, r)
	insomniaAuthentication(m, r)
	set(m, "method", r["method"], r["method"] != nil)
	return m
}

func insomniaGrpcParts(r Object) (any, any) {
	var parts []string
	for _, part := range strings.Split(str(r, "protoMethodName"), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	var service, method any
	if len(parts) > 0 {
		service = parts[0]
	}
	if len(parts) > 1 {
		method = parts[1]
	}
	return service, method
}

func insomniaGrpcMessage(r Object) any {
	if v, ok := obj(r, "body")["text"]; ok && v != nil {
		return v
	}
	return ""
}

func insomniaV4Grpc(r Object, wid string) Object {
	id, _ := insomniaMeta(r, "id", "_id")
	service, method := insomniaGrpcParts(r)
	m := Object{"id": insomniaID(jsString(id)), "workspaceId": insomniaID(wid), "folderId": insomniaFolderID(str(r, "parentId"), wid), "model": "grpc_request", "service": service, "method": method, "message": insomniaGrpcMessage(r), "metadata": insomniaPairs(r["metadata"], true)}
	insomniaDates(m, r["created"], r["modified"])
	set(m, "sortPriority", r["metaSortKey"], r["metaSortKey"] != nil)
	set(m, "name", r["name"], r["name"] != nil)
	insomniaDescription(m, r["description"])
	set(m, "url", r["url"], r["url"] != nil)
	return m
}

func insomniaV4Folder(f Object, wid string) Object {
	m := Object{"id": insomniaID(str(f, "_id")), "folderId": insomniaFolderID(str(f, "parentId"), wid), "workspaceId": insomniaID(wid), "model": "folder"}
	insomniaDates(m, f["created"], f["modified"])
	insomniaDescription(m, f["description"])
	set(m, "name", f["name"], f["name"] != nil)
	return m
}

func insomniaV4Environment(e Object, wid string) Object {
	parent := "environment"
	if str(e, "parentId") == wid {
		parent = "workspace"
	}
	m := Object{"id": insomniaID(str(e, "_id")), "workspaceId": insomniaID(wid), "parentModel": parent, "parentId": nil, "model": "environment", "variables": insomniaEnvironmentVariables(e["data"])}
	insomniaDates(m, e["created"], e["modified"])
	set(m, "sortPriority", e["metaSortKey"], e["metaSortKey"] != nil)
	set(m, "name", e["name"], e["name"] != nil)
	return m
}

func convertInsomniaV5(parsed Object) *insomniaResources {
	collection, ok := parsed["collection"].([]any)
	if !ok {
		return nil
	}
	resources := &insomniaResources{}
	meta := obj(parsed, "meta")
	wid := str(meta, "id")
	workspace := Object{"id": insomniaID(cmpOr(wid, "collection")), "model": "workspace", "headers": insomniaHeaders(parsed)}
	insomniaDates(workspace, meta["created"], meta["modified"])
	set(workspace, "name", parsed["name"], parsed["name"] != nil)
	insomniaDescription(workspace, meta["description"])
	insomniaAuthentication(workspace, parsed)
	resources.workspaces = append(resources.workspaces, workspace)

	if environments := obj(parsed, "environments"); environments != nil {
		resources.environments = append(resources.environments, insomniaV5Environment(environments, wid, true))
		for _, sub := range objects(asArray(environments["subEnvironments"])) {
			resources.environments = append(resources.environments, insomniaV5Environment(sub, wid, false))
		}
	}
	var nextFolder func(children []any, parent string)
	nextFolder = func(children []any, parent string) {
		for _, child := range objects(children) {
			switch {
			case child["children"] != nil && asArray(child["children"]) != nil:
				folder, environment := insomniaV5Folder(child, wid, parent)
				resources.folders = append(resources.folders, folder)
				if environment != nil {
					resources.environments = append(resources.environments, environment)
				}
				nextFolder(asArray(child["children"]), str(obj(child, "meta"), "id"))
			case truthy(child["method"]):
				resources.httpRequests = append(resources.httpRequests, insomniaV5Request(child, wid, parent))
			case truthy(child["protoFileId"]):
				resources.grpcRequests = append(resources.grpcRequests, insomniaV5Grpc(child, wid, parent))
			case truthy(child["url"]):
				resources.websocketRequests = append(resources.websocketRequests, insomniaV5WebSocket(child, wid, parent))
			}
		}
	}
	nextFolder(collection, wid)
	convertInsomniaTemplates(resources)
	return resources
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func insomniaV5Common(m, r Object, wid string) string {
	id, _ := insomniaMeta(r, "id", "_id")
	created, _ := insomniaMeta(r, "created", "created")
	updated, _ := insomniaMeta(r, "modified", "updated")
	sortKey, hasSort := insomniaMeta(r, "sortKey", "sortKey")
	m["id"] = insomniaID(jsString(id))
	m["workspaceId"] = insomniaID(wid)
	insomniaDates(m, created, updated)
	set(m, "sortPriority", sortKey, hasSort)
	return jsString(id)
}

func insomniaV5Request(r Object, wid, parent string) Object {
	m := Object{"folderId": insomniaFolderID(parent, wid), "model": "http_request"}
	insomniaV5Common(m, r, wid)
	set(m, "name", r["name"], r["name"] != nil)
	insomniaDescription(m, obj(r, "meta")["description"])
	set(m, "url", r["url"], r["url"] != nil)
	m["urlParameters"] = insomniaPairs(r["parameters"], false)
	insomniaBodyAndHeaders(m, r)
	set(m, "method", r["method"], r["method"] != nil)
	insomniaAuthentication(m, r)
	return m
}

func insomniaV5Grpc(r Object, wid, parent string) Object {
	service, method := insomniaGrpcParts(r)
	m := Object{"model": "grpc_request", "folderId": insomniaFolderID(parent, wid), "service": service, "method": method, "message": insomniaGrpcMessage(r), "metadata": insomniaPairs(r["metadata"], true)}
	insomniaV5Common(m, r, wid)
	set(m, "name", r["name"], r["name"] != nil)
	insomniaDescription(m, r["description"])
	set(m, "url", r["url"], r["url"] != nil)
	return m
}

func insomniaV5WebSocket(r Object, wid, parent string) Object {
	m := Object{"model": "websocket_request", "folderId": insomniaFolderID(parent, wid), "message": insomniaGrpcMessage(r), "headers": insomniaHeaders(r)}
	insomniaV5Common(m, r, wid)
	set(m, "name", r["name"], r["name"] != nil)
	insomniaDescription(m, r["description"])
	set(m, "url", r["url"], r["url"] != nil)
	insomniaAuthentication(m, r)
	return m
}

func insomniaV5Folder(f Object, wid, parent string) (Object, Object) {
	folder := Object{"model": "folder", "folderId": insomniaFolderID(parent, wid), "headers": insomniaHeaders(f)}
	id := insomniaV5Common(folder, f, wid)
	insomniaDescription(folder, f["description"])
	set(folder, "name", f["name"], f["name"] != nil)
	insomniaAuthentication(folder, f)
	data, _ := f["environment"].(map[string]any)
	if len(data) == 0 {
		return folder, nil
	}
	environment := Object{"id": insomniaID(id + "folder"), "workspaceId": insomniaID(wid), "public": true, "parentModel": "folder", "parentId": insomniaID(id), "model": "environment", "name": "Folder Environment", "variables": insomniaEnvironmentVariables(data)}
	created, _ := insomniaMeta(f, "created", "created")
	updated, _ := insomniaMeta(f, "modified", "updated")
	insomniaDates(environment, created, updated)
	return folder, environment
}

func insomniaV5Environment(e Object, wid string, base bool) Object {
	parent := "environment"
	if base {
		parent = "workspace"
	}
	m := Object{"public": !truthy(e["isPrivate"]), "parentModel": parent, "parentId": nil, "model": "environment", "variables": insomniaEnvironmentVariables(e["data"])}
	insomniaV5Common(m, e, wid)
	set(m, "name", e["name"], e["name"] != nil)
	return m
}

var (
	insomniaIdentifier = regexp.MustCompile(`^[\p{L}\p{N}_][\p{L}\p{N}_.-]*$`)
	insomniaVariable   = regexp.MustCompile(`{{\s*([\s\S]*?)\s*}}`)
	insomniaBracketSQ  = regexp.MustCompile(`^_\s*\[\s*'([^'"\\]+)'\s*\]$`)
	insomniaBracketDQ  = regexp.MustCompile(`^_\s*\[\s*"([^'"\\]+)"\s*\]$`)
	insomniaTag        = regexp.MustCompile(`{%\s*(\w+)\s*((?:'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"|[^'"])*?)%}`)
	insomniaArgToken   = regexp.MustCompile(`^\s*(?:'((?:[^'\\]|\\['"\\nrt])*)'|"((?:[^"\\]|\\['"\\nrt])*)"|(-?\d+(?:\.\d+)?)|(true|false))\s*`)
	insomniaArgEscape  = regexp.MustCompile(`\\(['"\\nrt])`)
	insomniaB64Arg     = regexp.MustCompile(`^b64::([A-Za-z0-9+/_-]*={0,2})(?:::[^:]*)?$`)
	insomniaPathFilter = regexp.MustCompile(`^\$(?:\.[\p{L}\p{N}_-]+|\[(?:0|[1-9]\d*)\])*$`)
	insomniaDigits     = regexp.MustCompile(`^\d+$`)
)

func convertInsomniaTemplates(r *insomniaResources) {
	ids := map[string]bool{}
	for _, m := range r.httpRequests {
		ids[str(m, "id")] = true
	}
	for _, list := range []*[]Object{&r.workspaces, &r.environments, &r.folders, &r.httpRequests, &r.grpcRequests, &r.websocketRequests} {
		for i, m := range *list {
			(*list)[i] = convertTemplateSyntax(m, ids).(map[string]any)
		}
	}
}

func convertTemplateSyntax(value any, requestIDs map[string]bool) any {
	switch v := value.(type) {
	case string:
		v = insomniaVariable.ReplaceAllStringFunc(v, func(full string) string {
			expression := strings.TrimSpace(insomniaVariable.FindStringSubmatch(full)[1])
			name := strings.TrimPrefix(expression, "_.")
			if m := insomniaBracketSQ.FindStringSubmatch(expression); m != nil {
				name = m[1]
			} else if m := insomniaBracketDQ.FindStringSubmatch(expression); m != nil {
				name = m[1]
			}
			if insomniaIdentifier.MatchString(name) && name != "true" && name != "false" && name != "null" {
				return "${[ " + name + " ]}"
			}
			return full
		})
		return insomniaTag.ReplaceAllStringFunc(v, func(full string) string {
			m := insomniaTag.FindStringSubmatch(full)
			if converted, ok := convertInsomniaTag(m[1], m[2], requestIDs); ok {
				return "${[ " + converted + " ]}"
			}
			return full
		})
	case map[string]any:
		copy := Object{}
		for k, item := range v {
			copy[k] = convertTemplateSyntax(item, requestIDs)
		}
		return copy
	case []any:
		copy := make([]any, len(v))
		for i, item := range v {
			copy[i] = convertTemplateSyntax(item, requestIDs)
		}
		return copy
	}
	return value
}

func insomniaArgument(value string) string {
	return "b64'" + base64.RawURLEncoding.EncodeToString([]byte(value)) + "'"
}

func decodeInsomniaArgument(value string) (string, bool) {
	if !strings.HasPrefix(value, "b64::") {
		return value, true
	}
	m := insomniaB64Arg.FindStringSubmatch(value)
	if m == nil {
		return "", false
	}
	normalized := strings.NewReplacer("+", "-", "/", "_").Replace(strings.TrimRight(m[1], "="))
	decoded, err := base64.RawURLEncoding.DecodeString(normalized)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != normalized || !utf8.Valid(decoded) {
		return "", false
	}
	return string(decoded), true
}

func parseInsomniaArgs(input string) ([]string, bool) {
	args := []string{}
	offset := 0
	for offset < len(input) {
		m := insomniaArgToken.FindStringSubmatchIndex(input[offset:])
		if m == nil {
			if strings.TrimSpace(input) == "" {
				return []string{}, true
			}
			return nil, false
		}
		literal := ""
		for g := 1; g <= 4; g++ {
			if m[2*g] >= 0 {
				literal = input[offset+m[2*g] : offset+m[2*g+1]]
				break
			}
		}
		literal = insomniaArgEscape.ReplaceAllStringFunc(literal, func(e string) string {
			switch e[1] {
			case 'n':
				return "\n"
			case 'r':
				return "\r"
			case 't':
				return "\t"
			}
			return e[1:]
		})
		decoded, ok := decodeInsomniaArgument(literal)
		if !ok {
			return nil, false
		}
		args = append(args, decoded)
		offset += m[1]
		if offset == len(input) {
			break
		}
		if input[offset] != ',' {
			return nil, false
		}
		offset++
		if strings.TrimSpace(input[offset:]) == "" {
			return nil, false
		}
	}
	return args, true
}

func argOr(args []string, i int, fallback string) string {
	if i < len(args) {
		return args[i]
	}
	return fallback
}

func convertInsomniaTag(name, input string, requestIDs map[string]bool) (string, bool) {
	args, ok := parseInsomniaArgs(input)
	if !ok {
		return "", false
	}
	switch name {
	case "base64":
		return convertInsomniaBase64(args)
	case "cookie":
		return convertInsomniaCookie(args)
	case "faker":
		return "uuid.v4()", len(args) == 1 && args[0] == "randomUUID"
	case "hash":
		return convertInsomniaHash(args)
	case "now":
		return convertInsomniaNow(args)
	case "prompt":
		return convertInsomniaPrompt(args)
	case "response":
		return convertInsomniaResponse(args, requestIDs)
	case "timestamp":
		return "timestamp.unixMillis()", len(args) == 0
	case "uuid":
		if len(args) == 0 {
			return "uuid.v4()", true
		}
		if len(args) == 1 && (args[0] == "v1" || args[0] == "v4") {
			return "uuid." + args[0] + "()", true
		}
	}
	return "", false
}

func convertInsomniaNow(args []string) (string, bool) {
	if len(args) > 2 {
		return "", false
	}
	switch strings.ToLower(argOr(args, 0, "iso-8601")) {
	case "iso-8601":
		return "timestamp.iso8601()", true
	case "millis", "ms":
		return "timestamp.unixMillis()", true
	case "unix", "seconds", "s":
		return "timestamp.unix()", true
	case "custom":
		return "timestamp.format(format=" + insomniaArgument(argOr(args, 1, "")) + ")", true
	}
	return "", false
}

var insomniaBase64Encodings = map[string]string{
	"normal": "base64",
	"url":    "base64url",
	"hex":    "base64",
}

func convertInsomniaBase64(args []string) (string, bool) {
	if len(args) < 2 || len(args) > 3 {
		return "", false
	}
	_, named := insomniaBase64Encodings[args[1]]
	legacy := len(args) == 2 && !named
	kind, value := args[1], argOr(args, 2, "")
	if legacy {
		kind, value = "normal", args[1]
	}
	encoding, ok := insomniaBase64Encodings[kind]
	if !ok {
		return "", false
	}
	switch args[0] {
	case "encode":
		return "base64.encode(encoding='" + encoding + "', value=" + insomniaArgument(value) + ")", true
	case "decode":
		return "base64.decode(value=" + insomniaArgument(value) + ")", true
	}
	return "", false
}

func convertInsomniaHash(args []string) (string, bool) {
	if len(args) > 3 {
		return "", false
	}
	algorithm := strings.ToLower(argOr(args, 0, "md5"))
	if !slices.Contains([]string{"md5", "sha1", "sha256", "sha512"}, algorithm) {
		algorithm = "sha256"
	}
	digest := "hex"
	if argOr(args, 1, "hex") == "base64" {
		digest = "base64"
	}
	return "hash." + algorithm + "(input=" + insomniaArgument(argOr(args, 2, "")) + ", encoding='" + digest + "')", true
}

func convertInsomniaCookie(args []string) (string, bool) {
	if len(args) != 2 || args[1] == "" {
		return "", false
	}
	domain := strings.TrimSpace(args[0])
	if u, err := url.Parse(domain); err == nil && u.Scheme != "" && u.Host != "" && !strings.ContainsAny(u.Host, " ${}[]") {
		domain = strings.ToLower(u.Hostname())
	}
	filter := ""
	if domain != "" {
		filter = ", domain=" + insomniaArgument(domain)
	}
	return "cookie.value(name=" + insomniaArgument(args[1]) + filter + ")", true
}

func convertInsomniaPrompt(args []string) (string, bool) {
	if len(args) > 6 {
		return "", false
	}
	title, label, defaultValue, storageKey, mask := argOr(args, 0, ""), argOr(args, 1, ""), argOr(args, 2, ""), argOr(args, 3, ""), argOr(args, 4, "false")
	if title == "" && label == "" {
		return "", false
	}
	if label == "" {
		label = title
	}
	parts := []string{"label=" + insomniaArgument(label)}
	masked := mask == "true"
	if storageKey != "" && !masked {
		parts = append(parts, "store='forever'", "namespace="+insomniaArgument("${[ctx.workspace()]}"), "key="+insomniaArgument(storageKey))
	}
	if title != "" {
		parts = append(parts, "title="+insomniaArgument(title))
	}
	if defaultValue != "" {
		parts = append(parts, "defaultValue="+insomniaArgument(defaultValue))
	}
	if masked {
		parts = append(parts, "password=true")
	}
	return "prompt.text(" + strings.Join(parts, ", ") + ")", true
}

func convertInsomniaResponse(args []string, requestIDs map[string]bool) (string, bool) {
	if len(args) < 2 || len(args) > 5 {
		return "", false
	}
	attribute, request, filter, behavior, maxAge := args[0], args[1], argOr(args, 2, ""), argOr(args, 3, "never"), argOr(args, 4, "0")
	if request == "" || !requestIDs[insomniaID(request)] {
		return "", false
	}
	converted, ok := map[string]string{"never": "never", "no-history": "smart", "always": "always", "when-expired": "ttl"}[behavior]
	if !ok || !insomniaDigits.MatchString(maxAge) {
		return "", false
	}
	if converted == "ttl" {
		if n, _ := strconv.ParseFloat(maxAge, 64); n == 0 {
			return "", false
		}
	}
	common := "request=" + insomniaArgument(insomniaID(request)) + ", behavior='" + converted + "', ttl='" + maxAge + "'"
	switch attribute {
	case "raw":
		return "response.body.raw(" + common + ")", true
	case "body":
		path := strings.TrimSpace(filter)
		if !insomniaPathFilter.MatchString(path) {
			return "", false
		}
		return "response.body.path(" + common + ", path=" + insomniaArgument(path) + ", result='first')", true
	case "header":
		if filter != "" {
			return "response.header(" + common + ", header=" + insomniaArgument(strings.TrimSpace(filter)) + ")", true
		}
	}
	return "", false
}

var insomniaOrder struct {
	sync.Mutex
	keys map[uintptr][]string
}

func importInsomnia(data []byte) ([]Object, error) {
	insomniaOrder.Lock()
	defer insomniaOrder.Unlock()
	insomniaOrder.keys = map[uintptr][]string{}
	defer func() { insomniaOrder.keys = nil }()
	root, err := parseOrderedDocument(data)
	if err != nil {
		return nil, err
	}
	return parseInsomniaImport(root), nil
}

func parseOrderedDocument(data []byte) (Object, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if len(node.Content) == 0 {
		return nil, errors.New(locales.EmptyDocument())
	}
	root, _ := orderedValue(node.Content[0]).(map[string]any)
	if root == nil {
		return nil, errors.New(locales.NotAnObject())
	}
	return root, nil
}

func orderedValue(n *yaml.Node) any {
	switch n.Kind {
	case yaml.AliasNode:
		return orderedValue(n.Alias)
	case yaml.MappingNode:
		m := map[string]any{}
		var keys []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if _, seen := m[key]; !seen {
				keys = append(keys, key)
			}
			m[key] = orderedValue(n.Content[i+1])
		}
		insomniaOrder.keys[reflect.ValueOf(m).Pointer()] = keys
		return m
	case yaml.SequenceNode:
		list := make([]any, len(n.Content))
		for i, item := range n.Content {
			list[i] = orderedValue(item)
		}
		return list
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return n.Value
	}
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return v
}

func orderedKeys(m map[string]any) []string {
	keys, ok := insomniaOrder.keys[reflect.ValueOf(m).Pointer()]
	if ok && len(keys) == len(m) {
		return keys
	}
	return slices.Sorted(maps.Keys(m))
}
