package filter

import "strings"

type Language uint8

const (
	JSONPath Language = iota
	XPath
)

func LanguageFor(contentType string) Language {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "xml") || strings.Contains(contentType, "html") {
		return XPath
	}
	return JSONPath
}

func Filter(language Language, text, query string) (string, error) {
	if language == XPath {
		return FilterXPath(text, query)
	}
	return FilterJSONPath(text, query)
}

func FilterJSONPath(text, query string) (string, error) { return evaluateJSONPath(text, query) }

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
