package engine

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"encoding/xml"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

func openAPIContentType(types []string) string {
	for _, preferred := range []string{"application/json", "application/x-www-form-urlencoded", "multipart/form-data", "application/xml", "text/plain"} {
		for _, ct := range types {
			media := openAPIMediaType(ct)
			if media == preferred {
				return ct
			}
		}
		if preferred == "application/json" {
			for _, ct := range types {
				if strings.HasSuffix(openAPIMediaType(ct), "+json") {
					return ct
				}
			}
		}
	}
	return firstImportStringStrings(types)
}
func firstImportStringStrings(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
func openAPIMediaType(ct string) string {
	media, _, _ := strings.Cut(ct, ";")
	return strings.ToLower(strings.TrimSpace(media))
}
func (s *openAPIImport) firstExample(value any) (any, bool) {
	if values, ok := value.([]any); ok && len(values) > 0 {
		return values[0], true
	}
	if examples, ok := value.(map[string]any); ok {
		for _, key := range slices.Sorted(maps.Keys(examples)) {
			example := importRef(s.root, obj(examples, key))
			if v, exists := example["value"]; exists {
				return v, true
			}
		}
	}
	return nil, false
}
func (s *openAPIImport) mediaExample(media Object) any {
	if v, exists := media["example"]; exists {
		return v
	}
	if v, exists := s.firstExample(media["examples"]); exists {
		return v
	}
	return s.schemaExample(obj(media, "schema"))
}
func openAPISchema(root, raw Object) Object {
	return importRef(root, raw)
}
func mergeOpenAPISchema(base, overlay Object, depth int) Object {
	result := maps.Clone(base)
	if result == nil {
		result = Object{}
	}
	for key, value := range overlay {
		result[key] = mergeOpenAPIKeyword(key, result[key], value, depth)
	}
	return result
}
func mergeOpenAPIKeyword(key string, base, overlay any, depth int) any {
	if depth > 8 {
		return overlay
	}
	switch key {
	case "properties", "$defs", "definitions":
		a, _ := base.(map[string]any)
		b, ok := overlay.(map[string]any)
		if !ok {
			return overlay
		}
		result := maps.Clone(a)
		if result == nil {
			result = Object{}
		}
		for name, value := range b {
			if child, ok := value.(map[string]any); ok {
				result[name] = mergeOpenAPISchema(obj(result, name), child, depth+1)
			} else {
				result[name] = value
			}
		}
		return result
	case "items":
		if b, ok := overlay.(map[string]any); ok {
			a, _ := base.(map[string]any)
			return mergeOpenAPISchema(a, b, depth+1)
		}
	case "required", "allOf":
		a, _ := base.([]any)
		b, ok := overlay.([]any)
		if ok {
			result := slices.Clone(a)
			for _, value := range b {
				if key == "required" {
					name, ok := value.(string)
					if !ok {
						continue
					}
					if slices.Contains(result, any(name)) {
						continue
					}
				}
				result = append(result, value)
			}
			return result
		}
	}
	return overlay
}
func openAPISchemaType(schema Object) string {
	if kind := str(schema, "type"); kind != "" {
		return kind
	}
	for _, value := range array(schema, "type") {
		if kind, ok := value.(string); ok && kind != "null" {
			return kind
		}
	}
	if schema["properties"] != nil || schema["additionalProperties"] != nil {
		return "object"
	}
	if schema["items"] != nil {
		return "array"
	}
	return "string"
}
func openAPICoerceExample(value any, schema Object) any {
	switch openAPISchemaType(schema) {
	case "string":
		switch value.(type) {
		case float64, int, bool:
			return openAPIValueText(value)
		}
	case "number", "integer":
		if text, ok := value.(string); ok {
			if number, err := strconv.ParseFloat(text, 64); err == nil {
				return number
			}
		}
	}
	return value
}
func (s *openAPIImport) schemaExample(schema Object) any {
	budget := 2048
	var example func(Object, int, map[string]bool) any
	example = func(raw Object, depth int, refs map[string]bool) any {
		budget--
		if depth > 8 || budget < 0 {
			return Object{}
		}
		refs = maps.Clone(refs)
		if ref := str(raw, "$ref"); ref != "" {
			if refs[ref] {
				return Object{}
			}
			refs[ref] = true
		}
		schema := openAPISchema(s.root, raw)
		for _, key := range []string{"example", "examples", "const", "default"} {
			if key == "examples" {
				if v, exists := s.firstExample(schema[key]); exists {
					return openAPICoerceExample(v, schema)
				}
				continue
			}
			if v, exists := schema[key]; exists {
				return openAPICoerceExample(v, schema)
			}
		}
		if values := array(schema, "enum"); len(values) > 0 {
			return values[0]
		}
		properties := Object{}
		keys := slices.Sorted(maps.Keys(obj(schema, "properties")))
		slices.SortStableFunc(keys, func(a, b string) int {
			required := array(schema, "required")
			return cmp.Compare(boolInt(slices.Contains(required, any(b))), boolInt(slices.Contains(required, any(a))))
		})
		for _, name := range keys {
			if len(properties) == 25 {
				break
			}
			child := obj(obj(schema, "properties"), name)
			if boolean(openAPISchema(s.root, child), "readOnly") {
				continue
			}
			properties[name] = example(child, depth+1, refs)
		}
		if children := objects(array(schema, "allOf")); len(children) > 0 {
			result := Object{}
			for _, child := range children {
				if v, ok := example(child, depth+1, refs).(map[string]any); ok {
					mergeOpenAPIExample(result, v)
				}
			}
			mergeOpenAPIExample(result, properties)
			return result
		}
		for _, kind := range []string{"oneOf", "anyOf"} {
			if children := objects(array(schema, kind)); len(children) > 0 {
				value := example(children[0], depth+1, refs)
				if len(properties) == 0 {
					return value
				}
				if m, ok := value.(map[string]any); ok {
					mergeOpenAPIExample(m, properties)
					return m
				}
				return properties
			}
		}
		switch openAPISchemaType(schema) {
		case "object":
			return properties
		case "array":
			return []any{example(obj(schema, "items"), depth+1, refs)}
		case "number", "integer":
			return 0
		case "boolean":
			return false
		default:
			return map[string]string{"date-time": "2026-01-01T00:00:00Z", "date": "2026-01-01", "email": "user@example.com", "hostname": "example.com", "ipv4": "127.0.0.1", "ipv6": "::1", "uri": "https://example.com", "url": "https://example.com", "uuid": "00000000-0000-0000-0000-000000000000"}[str(schema, "format")]
		}
	}
	return example(schema, 0, map[string]bool{})
}
func mergeOpenAPIExample(target, source Object) {
	for key, value := range source {
		if a, ok := target[key].(map[string]any); ok {
			if b, ok := value.(map[string]any); ok {
				mergeOpenAPIExample(a, b)
				continue
			}
		}
		target[key] = value
	}
}
func (s *openAPIImport) formSchema(raw Object) Object {
	budget := 2048
	var visit func(Object, int) Object
	visit = func(raw Object, depth int) Object {
		budget--
		if depth > 8 || budget < 0 {
			return Object{}
		}
		schema := maps.Clone(openAPISchema(s.root, raw))
		properties, required := maps.Clone(obj(schema, "properties")), slices.Clone(array(schema, "required"))
		for _, child := range objects(array(schema, "allOf")) {
			child = visit(child, depth+1)
			maps.Copy(properties, obj(child, "properties"))
			required = append(required, array(child, "required")...)
		}
		for _, key := range []string{"oneOf", "anyOf"} {
			if children := objects(array(schema, key)); len(children) > 0 {
				child := visit(children[0], depth+1)
				maps.Copy(properties, obj(child, "properties"))
				required = append(required, array(child, "required")...)
			}
		}
		schema["properties"], schema["required"] = properties, required
		return schema
	}
	return visit(raw, 0)
}
func (s *openAPIImport) requestBody(op Object, parameters []Object) (Object, string, []any) {
	content := obj(importRef(s.root, obj(op, "requestBody")), "content")
	ct := openAPIContentType(slices.Sorted(maps.Keys(content)))
	media := obj(content, ct)
	schema := obj(media, "schema")
	var example any
	if ct != "" {
		example = s.mediaExample(media)
	} else {
		consumes := array(op, "consumes")
		if op["consumes"] == nil {
			consumes = array(s.root, "consumes")
		}
		for _, p := range parameters {
			if str(p, "in") == "body" {
				ct = cmp.Or(firstImportString(consumes), "application/json")
				schema = obj(p, "schema")
				example = s.parameterValue(p)
				break
			}
		}
		if ct == "" {
			form := []any{}
			file := false
			for _, p := range parameters {
				if str(p, "in") == "formData" {
					row := Object{"name": str(p, "name"), "enabled": boolean(p, "required")}
					if str(p, "type") == "file" {
						row["file"], row["type"] = "", "file"
						file = true
					} else {
						row["value"] = strings.Join(openAPIFlatValues(s.parameterValue(p), false, openAPIValueText), ",")
					}
					form = append(form, row)
				}
			}
			if len(form) == 0 {
				return Object{}, "", nil
			}
			fallback := "application/x-www-form-urlencoded"
			if file {
				fallback = "multipart/form-data"
			}
			ct = cmp.Or(firstImportString(consumes), fallback)
			return Object{"form": form}, openAPIMediaType(ct), []any{Object{"name": "Content-Type", "value": ct, "enabled": true}}
		}
	}
	headers := []any{Object{"name": "Content-Type", "value": ct, "enabled": true}}
	mediaType := openAPIMediaType(ct)
	if mediaType == "multipart/form-data" || mediaType == "application/x-www-form-urlencoded" {
		schema = s.formSchema(schema)
		form := []any{}
		values, _ := example.(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(obj(schema, "properties"))) {
			if len(form) == 25 {
				break
			}
			p := openAPISchema(s.root, obj(obj(schema, "properties"), key))
			if boolean(p, "readOnly") {
				continue
			}
			row := Object{"name": key, "enabled": slices.Contains(array(schema, "required"), any(key))}
			if str(p, "format") == "binary" || str(p, "type") == "file" {
				row["file"], row["type"] = "", "file"
			} else {
				value, exists := values[key]
				if !exists {
					value = s.schemaExample(p)
				}
				row["value"] = openAPIValueText(value)
			}
			form = append(form, row)
		}
		return Object{"form": form}, mediaType, headers
	}
	schema = openAPISchema(s.root, schema)
	if mediaType == "application/octet-stream" || str(schema, "format") == "binary" || str(schema, "type") == "file" {
		return Object{"filePath": ""}, "binary", headers
	}
	text := openAPIValueText(example)
	if strings.Contains(mediaType, "json") {
		var decoded any
		if raw, ok := example.(string); !ok || json.Unmarshal([]byte(raw), &decoded) != nil {
			data, _ := json.Marshal(example, jsontext.WithIndent("  "), json.Deterministic(true))
			text = string(data)
		}
	} else if strings.Contains(mediaType, "xml") {
		if _, ok := example.(string); !ok {
			text = s.xmlExample(example, schema)
		}
	}
	bodyType := "text/plain"
	if strings.Contains(mediaType, "json") {
		bodyType = "application/json"
	} else if strings.Contains(mediaType, "xml") {
		bodyType = "application/xml"
	}
	return Object{"text": text}, bodyType, headers
}

func (s *openAPIImport) xmlExample(value any, schema Object) string {
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	encoder.Indent("", "  ")
	var element func(string, any, Object, int) error
	element = func(fallback string, value any, raw Object, depth int) error {
		if depth > 16 {
			return nil
		}
		schema := s.formSchema(raw)
		info := obj(schema, "xml")
		name := cmp.Or(str(info, "name"), fallback)
		if !openAPIXMLName(name) {
			name = "item"
		}
		if prefix := str(info, "prefix"); prefix != "" && openAPIXMLName(prefix) {
			name = prefix + ":" + name
		}
		start := xml.StartElement{Name: xml.Name{Local: name}}
		namespaces := map[string]string{}
		addNamespace := func(prefix, namespace string) string {
			base := prefix
			for i := 2; namespaces[prefix] != "" && namespaces[prefix] != namespace; i++ {
				prefix = base + strconv.Itoa(i)
			}
			if namespaces[prefix] != namespace {
				namespaces[prefix] = namespace
				key := "xmlns"
				if prefix != "" {
					key += ":" + prefix
				}
				start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: key}, Value: namespace})
			}
			return prefix
		}
		if namespace := str(info, "namespace"); namespace != "" {
			prefix := str(info, "prefix")
			if !openAPIXMLName(prefix) {
				prefix = ""
			}
			addNamespace(prefix, namespace)
		}
		if rows, ok := value.([]any); ok {
			wrapped := depth == 0 || boolean(info, "wrapped")
			if wrapped {
				if err := encoder.EncodeToken(start); err != nil {
					return err
				}
			}
			for _, row := range rows {
				if err := element(fallback, row, obj(schema, "items"), depth+1); err != nil {
					return err
				}
			}
			if wrapped {
				return encoder.EncodeToken(start.End())
			}
			return nil
		}
		properties := obj(schema, "properties")
		if object, ok := value.(map[string]any); ok {
			for _, key := range slices.Sorted(maps.Keys(object)) {
				child := openAPISchema(s.root, obj(properties, key))
				info := obj(child, "xml")
				if boolean(info, "attribute") {
					attrName := cmp.Or(str(info, "name"), key)
					if !openAPIXMLName(attrName) {
						attrName = "attribute"
					}
					prefix := str(info, "prefix")
					if namespace := str(info, "namespace"); namespace != "" {
						if !openAPIXMLName(prefix) {
							prefix = "ns"
						}
						prefix = addNamespace(prefix, namespace)
					}
					if prefix != "" && openAPIXMLName(prefix) {
						attrName = prefix + ":" + attrName
					}
					start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: attrName}, Value: openAPIValueText(object[key])})
				}
			}
		}
		if err := encoder.EncodeToken(start); err != nil {
			return err
		}
		if object, ok := value.(map[string]any); ok {
			for _, key := range slices.Sorted(maps.Keys(object)) {
				child := obj(properties, key)
				if boolean(obj(openAPISchema(s.root, child), "xml"), "attribute") {
					continue
				}
				if err := element(key, object[key], child, depth+1); err != nil {
					return err
				}
			}
		} else {
			if err := encoder.EncodeToken(xml.CharData(openAPIValueText(value))); err != nil {
				return err
			}
		}
		return encoder.EncodeToken(start.End())
	}
	if err := element("root", value, schema, 0); err != nil {
		return ""
	}
	if err := encoder.Flush(); err != nil {
		return ""
	}
	return output.String()
}
func openAPIXMLName(name string) bool {
	for i, r := range name {
		if unicode.IsLetter(r) || r == '_' || i > 0 && (unicode.IsNumber(r) || r == '-' || r == '.') {
			continue
		}
		return false
	}
	return name != ""
}
