package engine

import (
	"errors"
	"strings"
	"unicode"
)

func parseBruno(text string) ([]Object, error) {
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
	return nil, errors.New("bruno file contains no supported request or environment")
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
			return nil, errors.New("bruno block is missing an opening brace")
		}
		name := strings.TrimSpace(text[:open])
		if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
			return nil, errors.New("invalid Bruno block name")
		}
		depth := 1
		quote := byte(0)
		escaped := false
		end := -1
		for i := open + 1; i < len(text); i++ {
			ch := text[i]
			if escaped {
				escaped = false
				continue
			}
			if quote != 0 {
				switch ch {
				case '\\':
					escaped = true
				case quote:
					quote = 0
				}
				continue
			}
			switch ch {
			case '\'', '"':
				quote = ch
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return nil, errors.New("bruno block is missing a closing brace")
		}
		result[name] = strings.TrimSpace(text[open+1 : end])
		text = text[end+1:]
	}
	return result, nil
}
