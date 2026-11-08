package engine

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"yeek/locales"
)

var (
	brunoVariable     = regexp.MustCompile(`{{\s*([^{}]+?)\s*}}`)
	brunoVariableName = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_.-]*$`)
	brunoPlainKeys    = []string{"id", "model", "workspaceId", "parentModel", "parentId", "name", "description", "method", "bodyType", "authenticationType"}
)

func brunoTemplate(text string) string {
	return brunoVariable.ReplaceAllStringFunc(text, func(match string) string {
		name := brunoVariable.FindStringSubmatch(match)[1]
		if strings.HasPrefix(name, "process.env.") || name == "true" || name == "false" || name == "null" || !brunoVariableName.MatchString(name) {
			return match
		}
		return "${[ " + name + " ]}"
	})
}

func convertBrunoTemplates(value any) any {
	switch v := value.(type) {
	case string:
		return brunoTemplate(v)
	case Object:
		for key, item := range v {
			v[key] = convertBrunoTemplates(item)
		}
	case []any:
		for i, item := range v {
			v[i] = convertBrunoTemplates(item)
		}
	}
	return value
}

func parseBruno(text string) ([]Object, error) {
	models, err := parseBrunoModels(text)
	for _, m := range models {
		for key, value := range m {
			if !slices.Contains(brunoPlainKeys, key) {
				m[key] = convertBrunoTemplates(value)
			}
		}
	}
	return models, err
}

func parseBrunoModels(text string) ([]Object, error) {
	blocks, err := brunoBlocks(text)
	if err != nil {
		return nil, err
	}
	meta := brunoFields(blocks["meta"])
	name := str(meta, "name")
	if name == "" {
		name = "Bruno Collection"
	}
	workspace := "bruno_workspace"
	result := []Object{{"model": "workspace", "id": workspace, "name": name}}
	if variables := brunoPairs(blocks["vars"]); len(variables) > 0 {
		result = append(result, Object{"model": "environment", "id": "bruno_environment", "workspaceId": workspace, "parentModel": "workspace", "parentId": workspace, "name": "Global Variables", "variables": variables})
	}
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "connect", "trace"} {
		section, ok := blocks[method]
		if !ok {
			continue
		}
		fields := brunoFields(section)
		request := Object{"model": "http_request", "id": "bruno_request", "workspaceId": workspace, "name": name, "method": strings.ToUpper(method), "url": str(fields, "url"), "headers": brunoPairs(blocks["headers"]), "urlParameters": brunoPairs(blocks["params:query"]), "description": blocks["docs"]}
		for _, p := range objects(brunoPairs(blocks["params:path"])) {
			p["name"] = ":" + str(p, "name")
			request["urlParameters"] = append(array(request, "urlParameters"), p)
		}
		for block, kind := range map[string]string{"body:json": "application/json", "body:text": "text/plain", "body:xml": "application/xml", "body:graphql": "graphql", "body:form-urlencoded": "application/x-www-form-urlencoded", "body:multipart-form": "multipart/form-data"} {
			if raw, ok := blocks[block]; ok {
				request["bodyType"] = kind
				switch kind {
				case "graphql":
					request["body"] = Object{"query": raw, "variables": blocks["body:graphql:vars"]}
				case "application/x-www-form-urlencoded", "multipart/form-data":
					request["body"] = Object{"form": brunoPairs(raw)}
				default:
					request["body"] = Object{"text": raw}
				}
			}
		}
		for block, kind := range map[string]string{"auth:basic": "basic", "auth:bearer": "bearer", "auth:apikey": "apikey", "auth:oauth2": "oauth2", "auth:awsv4": "awsv4", "auth:digest": "digest"} {
			if raw, ok := blocks[block]; ok {
				request["authenticationType"] = kind
				request["authentication"] = brunoFields(raw)
			}
		}
		result = append(result, request)
		return result, nil
	}
	if len(result) > 1 {
		return result, nil
	}
	return nil, errors.New(locales.BrunoFileContainsNoSupportedRequest())
}
func brunoFields(text string) Object {
	m := Object{}
	for _, p := range objects(brunoPairs(text)) {
		m[str(p, "name")] = p["value"]
	}
	return m
}
func brunoPairs(text string) []any {
	result := []any{}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		enabled := !strings.HasPrefix(key, "~")
		result = append(result, Object{"name": strings.TrimSpace(strings.TrimPrefix(key, "~")), "value": strings.TrimSpace(value), "enabled": enabled})
	}
	return result
}
func brunoBlocks(text string) (map[string]string, error) {
	result := map[string]string{}
	for len(strings.TrimSpace(text)) > 0 {
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "//") {
			_, text, _ = strings.Cut(text, "\n")
			continue
		}
		open := strings.IndexByte(text, '{')
		if open < 0 {
			return nil, errors.New(locales.BrunoBlockIsMissingAnOpening())
		}
		name := strings.TrimSpace(text[:open])
		if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
			return nil, errors.New(locales.InvalidBrunoBlockName())
		}
		body, rest, ok := brunoBlockBody(text[open+1:])
		if !ok {
			return nil, errors.New(locales.BrunoBlockIsMissingAClosing())
		}
		result[name] = strings.TrimSpace(body)
		text = rest
	}
	return result, nil
}

func brunoBlockBody(text string) (body, rest string, ok bool) {
	first, after, multiline := strings.Cut(text, "\n")
	if line := strings.TrimSpace(first); line != "" || !multiline {
		inner, closed := strings.CutSuffix(line, "}")
		if !closed {
			return "", "", false
		}
		return inner, after, true
	}
	offset := 0
	for line := range strings.SplitAfterSeq(after, "\n") {
		if strings.TrimRight(line, " \t\r\n") == "}" {
			return after[:offset], after[offset+len(line):], true
		}
		offset += len(line)
	}
	return "", "", false
}
