package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/url"
	"strings"
	"unicode"
)

// prettyBodyJSON is JSON.stringify(value, null, 2). Object members keep
// their order: an orderedBody lists them, and JSON text given as a
// jsontext.Value is reindented as it stands.
func prettyBodyJSON(value any) string {
	data, _ := json.Marshal(value, jsontext.WithIndent("  "), json.Deterministic(true))
	return string(data)
}

// orderedBody is a JSON object that keeps its members in the order added,
// as a JavaScript object does.
type orderedBody struct {
	keys   []string
	values map[string]any
}

func (o *orderedBody) set(key string, value any) {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o orderedBody) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, key := range o.keys {
		if err := enc.WriteToken(jsontext.String(key)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, o.values[key]); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// jsonText is text parsed as JSON, keeping its members' order, or false
// when it is not JSON.
func jsonText(text string) (jsontext.Value, bool) {
	value := jsontext.Value(strings.TrimSpace(text))
	if !value.IsValid(jsontext.AllowDuplicateNames(true)) {
		return nil, false
	}
	return value, true
}

// formURLEncode is URLSearchParams' serializer: it leaves letters, digits
// and *-._ as they are and writes spaces as +.
func formURLEncode(s string) string {
	return strings.NewReplacer("%2A", "*", "~", "%7E").Replace(url.QueryEscape(s))
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
					if object["variables"] != nil {
						var variables struct {
							Variables jsontext.Value `json:"variables"`
						}
						_ = json.Unmarshal([]byte(text), &variables)
						result["variables"] = prettyBodyJSON(variables.Variables)
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
	if text, exists := body["text"].(string); exists {
		result := Object{"text": text}
		if comments, ok := body["sendJsonComments"].(bool); ok {
			result["sendJsonComments"] = comments
		}
		return result
	}
	if _, exists := body["form"].([]any); exists {
		var result orderedBody
		var pairs []string
		for _, row := range objects(array(body, "form")) {
			if !enabled(row) || str(row, "name") == "" {
				continue
			}
			name, value := str(row, "name"), str(row, "value")
			if IsFileFormField(row) {
				value = str(row, "file")
			}
			pairs = append(pairs, formURLEncode(name)+"="+formURLEncode(value))
			if old, exists := result.values[name]; exists {
				if list, ok := old.([]any); ok {
					result.set(name, append(list, value))
				} else {
					result.set(name, []any{old, value})
				}
			} else {
				result.set(name, value)
			}
		}
		if to == "application/json" {
			return Object{"text": prettyBodyJSON(result)}
		}
		return Object{"text": strings.Join(pairs, "&")}
	}
	if query, exists := body["query"].(string); exists {
		if to == "application/json" || from == "graphql" {
			var result orderedBody
			result.set("query", query)
			if text := str(body, "variables"); strings.TrimSpace(text) != "" {
				if value, ok := jsonText(StripJSONComments(text)); ok {
					result.set("variables", value)
				} else {
					result.set("variables", text)
				}
			}
			if op := str(body, "operationName"); strings.TrimSpace(op) != "" {
				result.set("operationName", op)
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

// StripJSONComments is Yaak's strip_json_comments: it removes // and /* */
// comments from JSONC, outside strings and template tags, with the spaces
// before them, then the lines left empty and trailing commas before } or ].
// Like Rust's str::lines, it ends lines at \n and drops a \r before one.
func StripJSONComments(text string) string {
	r := []rune(text)
	var result []rune
	var quoted tagStrings
	inString, inTag := false, false
	trim := func() {
		for len(result) > 0 && (result[len(result)-1] == ' ' || result[len(result)-1] == '\t') {
			result = result[:len(result)-1]
		}
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case inString:
			result = append(result, c)
			if c == '"' {
				inString = false
			} else if c == '\\' && i+1 < len(r) {
				i++
				result = append(result, r[i])
			}
		case inTag:
			result = append(result, c)
			// A quoted argument is allowed to contain ]}, so skip over strings whole.
			if c == '\'' {
				if end, ok := quoted.take(r, i+1); ok {
					result = append(result, r[i+1:end]...)
					i = end - 1
				}
				continue
			}
			if c == ']' && i+1 < len(r) && r[i+1] == '}' {
				i++
				result = append(result, '}')
				inTag = false
			}
		case c == '$' && i+2 < len(r) && r[i+1] == '{' && r[i+2] == '[':
			inTag = true
			result = append(result, '$', '{', '[')
			i += 2
		case c == '/' && i+1 < len(r) && r[i+1] == '/':
			i += 2
			for i < len(r) && r[i] != '\n' {
				i++
			}
			i--
			trim()
		case c == '/' && i+1 < len(r) && r[i+1] == '*':
			i += 2
			for i < len(r) && (r[i] != '*' || i+1 >= len(r) || r[i+1] != '/') {
				i++
			}
			i++ // the closing /
			trim()
		default:
			if c == '"' {
				inString = true
			}
			result = append(result, c)
		}
	}
	// Remove lines that are now empty (were comment-only lines).
	var lines []string
	for _, line := range rustLines(string(result)) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return stripTrailingCommas([]rune(strings.Join(lines, "\n")))
}

// tagStrings is Yaak's TagStrings: it reads the single-quoted strings in
// template tags, left to right through one text, remembering when a scan
// ran off its end, after which no string can close.
type tagStrings struct{ noneLeft bool }

// take reads the body of a string whose opening quote is before from: it
// returns the index just past the closing quote, or false when the quote
// never closes, which leaves it an ordinary character. \ escapes what
// follows it.
func (t *tagStrings) take(r []rune, from int) (int, bool) {
	if t.noneLeft {
		return 0, false
	}
	for i := from; i < len(r); i++ {
		switch r[i] {
		case '\\':
			if i+1 >= len(r) {
				t.noneLeft = true
				return 0, false
			}
			i++
		case '\'':
			return i + 1, true
		}
	}
	t.noneLeft = true
	return 0, false
}

// rustLines is Rust's str::lines.
func rustLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// stripTrailingCommas is Yaak's strip_trailing_commas: a comma before } or
// ], past whitespace, goes, outside strings.
func stripTrailingCommas(r []rune) string {
	var result []rune
	inString := false
	for i := 0; i < len(r); i++ {
		c := r[i]
		if inString {
			result = append(result, c)
			if c == '"' {
				inString = false
			} else if c == '\\' && i+1 < len(r) {
				i++
				result = append(result, r[i])
			}
			continue
		}
		if c == '"' {
			inString = true
		}
		if c == ',' {
			j := i + 1
			for j < len(r) && unicode.IsSpace(r[j]) {
				j++
			}
			if j < len(r) && (r[j] == '}' || r[j] == ']') {
				continue
			}
		}
		result = append(result, c)
	}
	return string(result)
}

func FixJSONBody(text string) string {
	fixed := StripJSONComments(text)
	if jsontext.Value(fixed).IsValid(jsontext.AllowDuplicateNames(true)) {
		return fixed
	}
	return text
}
