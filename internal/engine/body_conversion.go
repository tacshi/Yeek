package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/url"
	"strings"
)

func prettyBodyJSON(value any) string {
	data, _ := json.Marshal(value, jsontext.WithIndent("  "), json.Deterministic(true))
	return string(data)
}

// ConvertRequestBody keeps input that cannot be represented by the new editor.
func ConvertRequestBody(body Object, from, to string) Object {
	body = clone(body)
	if to == "" {
		return Object{}
	}
	if to == "graphql" {
		if _, exists := body["query"].(string); exists {
			return body
		}
		if text, exists := body["text"].(string); exists {
			var parsed any
			if err := json.Unmarshal([]byte(text), &parsed); err != nil {
				return Object{"query": text}
			}
			if object, ok := parsed.(map[string]any); ok {
				if query, ok := object["query"].(string); ok {
					result := Object{"query": query}
					if variables := object["variables"]; variables != nil {
						result["variables"] = prettyBodyJSON(variables)
					}
					if operation, ok := object["operationName"].(string); ok {
						result["operationName"] = operation
					}
					return result
				}
			}
		}
		return body
	}
	if to == "application/x-www-form-urlencoded" || to == "multipart/form-data" {
		if _, exists := body["form"].([]any); exists {
			for _, row := range objects(array(body, "form")) {
				if to == "application/x-www-form-urlencoded" && IsFileFormField(row) {
					row["value"] = str(row, "file")
					delete(row, "file")
					delete(row, "type")
					delete(row, "filename")
				}
			}
			return Object{"form": body["form"]}
		}
		return body
	}
	if to == "binary" {
		if _, exists := body["filePath"]; exists {
			return Object{"filePath": body["filePath"]}
		}
		return body
	}
	if _, exists := body["text"].(string); exists {
		return body
	}
	if _, exists := body["form"].([]any); exists {
		result, values := Object{}, url.Values{}
		for _, row := range objects(array(body, "form")) {
			if !enabled(row) || str(row, "name") == "" {
				continue
			}
			name, value := str(row, "name"), str(row, "value")
			if IsFileFormField(row) {
				value = str(row, "file")
			}
			values.Add(name, value)
			if old, exists := result[name]; exists {
				if list, ok := old.([]any); ok {
					result[name] = append(list, value)
				} else {
					result[name] = []any{old, value}
				}
			} else {
				result[name] = value
			}
		}
		if to == "application/json" {
			return Object{"text": prettyBodyJSON(result)}
		}
		return Object{"text": values.Encode()}
	}
	if query, exists := body["query"].(string); exists {
		if to == "application/json" || from == "graphql" {
			result := Object{"query": query}
			if text := str(body, "variables"); strings.TrimSpace(text) != "" {
				var value any
				if json.Unmarshal([]byte(StripJSONComments(text)), &value) != nil {
					value = text
				}
				result["variables"] = value
			}
			if op := str(body, "operationName"); strings.TrimSpace(op) != "" {
				result["operationName"] = op
			}
			return Object{"text": prettyBodyJSON(result)}
		}
		return Object{"text": query}
	}
	if path, exists := body["filePath"].(string); exists {
		return Object{"text": path}
	}
	return body
}

func StripJSONComments(text string) string {
	output := make([]byte, 0, len(text))
	quoted, escaped, tagQuote := false, false, byte(0)
	inTag := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if quoted || inTag {
			output = append(output, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if quoted {
				if ch == '"' {
					quoted = false
				}
				continue
			}
			if tagQuote != 0 {
				if ch == tagQuote {
					tagQuote = 0
				}
				continue
			}
			if ch == '\'' || ch == '"' {
				tagQuote = ch
				continue
			}
			if ch == ']' && i+1 < len(text) && text[i+1] == '}' {
				output = append(output, '}')
				i++
				inTag = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
			output = append(output, ch)
			continue
		}
		if strings.HasPrefix(text[i:], "${[") {
			output = append(output, '$', '{', '[')
			i += 2
			inTag = true
			continue
		}
		if ch == '/' && i+1 < len(text) && text[i+1] == '/' {
			for len(output) > 0 && (output[len(output)-1] == ' ' || output[len(output)-1] == '\t') {
				output = output[:len(output)-1]
			}
			i += 2
			for i < len(text) && text[i] != '\n' && text[i] != '\r' {
				i++
			}
			i--
			continue
		}
		if ch == '/' && i+1 < len(text) && text[i+1] == '*' {
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				return text
			}
			output = append(output, ' ')
			i += end + 3
			continue
		}
		output = append(output, ch)
	}
	quoted, escaped = false, false
	for i, ch := range output {
		if quoted {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
			continue
		}
		if ch == ',' {
			j := i + 1
			for j < len(output) && strings.ContainsRune(" \t\r\n", rune(output[j])) {
				j++
			}
			if j < len(output) && (output[j] == '}' || output[j] == ']') {
				output[i] = ' '
			}
		}
	}
	return string(output)
}
func FixJSONBody(text string) string {
	fixed := StripJSONComments(text)
	if jsontext.Value(fixed).IsValid(jsontext.AllowDuplicateNames(true)) {
		return fixed
	}
	return text
}
