// Package filter is Yaak's yaak-filter crate: JSONPath (jsonpath-plus, with
// RFC 9535 additions) and XPath 1.0 filtering of response text.
package filter

import "strings"

type Language uint8

const (
	JSONPath Language = iota
	XPath
)

// LanguageFor is the language that filters a body of the content type: XML
// and HTML bodies take XPath; everything else is treated as JSON.
func LanguageFor(contentType string) Language {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "xml") || strings.Contains(contentType, "html") {
		return XPath
	}
	return JSONPath
}

// Filter is the filtered text, or the reason it could not be filtered.
func Filter(language Language, text, query string) (string, error) {
	if language == XPath {
		return FilterXPath(text, query)
	}
	return FilterJSONPath(text, query)
}

// FilterJSONPath is every match, as a pretty-printed JSON array.
func FilterJSONPath(text, query string) (string, error) { return evaluateJSONPath(text, query) }

// TemplateJSONPath is Yaak's filterJSONPath (template-function-json), which
// the json.jsonpath and response body path template functions share: the
// first match ("first"), every match ("all"), or the matches joined
// ("join"), objects and arrays as JSON (indented when formatted) and the
// rest as JavaScript's String gives them.
func TemplateJSONPath(body, path, result, join string, formatted bool) (string, error) {
	root, err := parseJSON(body)
	if err != nil {
		return "", err
	}
	items, err := newEngine(root).run(path, fromDoc(root))
	if err != nil {
		return "", err
	}
	text := func(v jsValue, formatted bool) string {
		if v.isDocArray() || v.isDocObject() || v.kind == jsArrayKind {
			indent := ""
			if formatted {
				indent = "  "
			}
			return stringify(v, indent)
		}
		return v.toJSString()
	}
	switch result {
	case "all":
		return text(jsArrayValue(items), formatted), nil
	case "join":
		parts := make([]string, len(items))
		for i, item := range items {
			parts[i] = text(item, false)
		}
		return strings.Join(parts, join), nil
	}
	if len(items) == 0 {
		return "", nil
	}
	return text(items[0], formatted), nil
}
