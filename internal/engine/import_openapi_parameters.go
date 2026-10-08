package engine

import (
	"cmp"
	"encoding/json/v2"
	"maps"
	"net/url"
	"slices"
	"strings"
)

func openAPIParameters(root Object, groups ...[]any) []Object {
	result := []Object{}
	positions := map[string]int{}
	for _, group := range groups {
		for _, raw := range objects(group) {
			parameter := importRef(root, raw)
			key := str(parameter, "in") + "\n" + str(parameter, "name")
			if i, exists := positions[key]; exists {
				result[i] = parameter
			} else {
				positions[key] = len(result)
				result = append(result, parameter)
			}
		}
	}
	return result
}
func openAPIValueText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, _ := json.Marshal(value, json.Deterministic(true))
	return string(data)
}
func (s *openAPIImport) parameterValue(parameter Object) any {
	if value, exists := parameter["example"]; exists {
		return value
	}
	if value, exists := s.firstExample(parameter["examples"]); exists {
		return value
	}
	if content := obj(parameter, "content"); len(content) > 0 {
		return s.mediaExample(obj(content, openAPIContentType(slices.Sorted(maps.Keys(content)))))
	}
	schema := obj(parameter, "schema")
	if len(schema) == 0 {
		schema = parameter
	}
	return s.schemaExample(schema)
}
func (s *openAPIImport) parameterRows(path string, parameters []Object) (string, []any, []any) {
	query, headers := []any{}, []any{}
	for _, p := range parameters {
		name, location := str(p, "name"), str(p, "in")
		if name == "" {
			continue
		}
		value := s.parameterValue(p)
		if content := obj(p, "content"); len(content) > 0 {
			ct := openAPIContentType(slices.Sorted(maps.Keys(content)))
			if strings.Contains(ct, "json") {
				data, _ := json.Marshal(value, json.Deterministic(true))
				value = string(data)
			} else {
				value = openAPIValueText(value)
			}
		}
		on := boolean(p, "required") || location == "path"
		row := func(key, value string) Object { return Object{"name": key, "value": value, "enabled": on} }
		switch location {
		case "path":
			token := "{" + name + "}"
			whole := false
			for segment := range strings.SplitSeq(path, "/") {
				if strings.Contains(segment, token) {
					whole = segment == token || strings.HasPrefix(segment, token+":")
					if !whole {
						break
					}
				}
			}
			_, array := value.([]any)
			_, object := value.(map[string]any)
			style := str(p, "style")
			if whole && !array && !object && style != "label" && style != "matrix" {
				path = strings.ReplaceAll(path, token, ":"+name)
				query = append(query, row(":"+name, cmp.Or(openAPIValueText(value), name)))
			} else {
				encode := func(v any) string { return strings.ReplaceAll(url.QueryEscape(openAPIValueText(v)), "+", "%20") }
				text := openAPIPathValue(name, value, p, encode)
				if text != "" {
					path = strings.ReplaceAll(path, token, text)
				}
			}
		case "query":
			switch v := value.(type) {
			case map[string]any:
				style := cmp.Or(str(p, "style"), "form")
				if style == "deepObject" || style == "form" && p["explode"] != false {
					for _, key := range slices.Sorted(maps.Keys(v)) {
						param := key
						if style == "deepObject" {
							param = name + "[" + key + "]"
						}
						query = append(query, row(param, openAPIValueText(v[key])))
					}
				} else {
					separator := ","
					if style == "spaceDelimited" {
						separator = " "
					}
					if style == "pipeDelimited" {
						separator = "|"
					}
					query = append(query, row(name, strings.Join(openAPIFlatValues(v, false, openAPIValueText), separator)))
				}
			case []any:
				separator := ","
				if collection := str(p, "collectionFormat"); collection != "" || p["schema"] == nil {
					separator = cmp.Or(map[string]string{"csv": ",", "ssv": " ", "tsv": "\t", "pipes": "|"}[collection], ",")
					if collection == "multi" {
						separator = ""
					}
				} else {
					style := str(p, "style")
					switch {
					case style == "spaceDelimited":
						separator = " "
					case style == "pipeDelimited":
						separator = "|"
					case p["explode"] != false:
						separator = ""
					}
				}
				if separator == "" {
					for _, entry := range v {
						query = append(query, row(name, openAPIValueText(entry)))
					}
				} else {
					query = append(query, row(name, strings.Join(openAPIFlatValues(v, false, openAPIValueText), separator)))
				}
			default:
				query = append(query, row(name, openAPIValueText(value)))
			}
		case "header":
			if !slices.Contains([]string{"accept", "content-type", "authorization"}, strings.ToLower(name)) {
				headers = append(headers, row(name, strings.Join(openAPIFlatValues(value, boolean(p, "explode"), openAPIValueText), ",")))
			}
		case "cookie":
			text := name + "=" + strings.Join(openAPIFlatValues(value, false, openAPIValueText), ",")
			if p["explode"] != false {
				switch v := value.(type) {
				case map[string]any:
					text = strings.Join(openAPIFlatValues(v, true, openAPIValueText), "; ")
				case []any:
					pairs := []string{}
					for _, entry := range v {
						pairs = append(pairs, name+"="+openAPIValueText(entry))
					}
					text = strings.Join(pairs, "; ")
				}
			}
			headers = append(headers, row("Cookie", text))
		}
	}
	return path, query, headers
}
func openAPIFlatValues(value any, explode bool, encode func(any) string) []string {
	parts := []string{}
	switch v := value.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(v)) {
			if explode {
				parts = append(parts, encode(key)+"="+encode(v[key]))
			} else {
				parts = append(parts, encode(key), encode(v[key]))
			}
		}
	case []any:
		for _, entry := range v {
			parts = append(parts, encode(entry))
		}
	default:
		parts = append(parts, encode(v))
	}
	return parts
}
func openAPIPathValue(name string, value any, parameter Object, encode func(any) string) string {
	style, explode := str(parameter, "style"), boolean(parameter, "explode")
	parts := openAPIFlatValues(value, explode, encode)
	switch style {
	case "label":
		separator := ","
		if explode {
			separator = "."
		}
		return "." + strings.Join(parts, separator)
	case "matrix":
		if explode {
			switch v := value.(type) {
			case map[string]any:
				return ";" + strings.Join(parts, ";")
			case []any:
				result := ""
				for _, entry := range v {
					result += ";" + name + "=" + encode(entry)
				}
				return result
			}
		}
		return ";" + name + "=" + strings.Join(parts, ",")
	default:
		return strings.Join(parts, ",")
	}
}
func openAPIMergeHeaders(groups ...[]any) []any {
	result := []any{}
	seen := map[string]bool{}
	for _, group := range groups {
		for _, row := range objects(group) {
			key := strings.ToLower(str(row, "name"))
			if key == "cookie" || !seen[key] {
				result = append(result, row)
				seen[key] = true
			}
		}
	}
	return result
}
func (s *openAPIImport) acceptHeader(operation Object) []any {
	produces := array(operation, "produces")
	if operation["produces"] == nil {
		produces = array(s.root, "produces")
	}
	contentType := firstImportString(produces)
	if contentType == "" {
		for _, status := range slices.Sorted(maps.Keys(obj(operation, "responses"))) {
			if !strings.HasPrefix(status, "2") && status != "default" {
				continue
			}
			response := importRef(s.root, obj(obj(operation, "responses"), status))
			contentType = openAPIContentType(slices.Sorted(maps.Keys(obj(response, "content"))))
			if contentType != "" {
				break
			}
		}
	}
	if contentType == "" || contentType == "*/*" {
		return nil
	}
	return []any{Object{"name": "Accept", "value": contentType, "enabled": true}}
}
