package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// TemplateField describes a function argument for native editors and Go plugins.
type TemplateField struct {
	Name, Label, Kind, Default string
	Options                    []string
	// OptionLabels name Options in a select, when they differ.
	OptionLabels []string
	Placeholder  string
	Secret       bool
	// Advanced fields are under Yaak's Advanced accordion; Visible, when
	// set, shows the field only for some values of the others.
	Advanced bool
	Visible  func(values map[string]string) bool
	// Describe, when set, names the field for the others' values, or hides it.
	Describe func(values map[string]string) (label, description string, secret, visible bool)
	// Dynamic, when set, lists a select's options, and their labels, for
	// the others' values (rendered); it hides the select while it fails.
	Dynamic func(ctx context.Context, values map[string]string) (options, labels []string, err error)
}
type TemplateDefinition struct {
	Name, Description string
	Fields            []TemplateField
	Preview           bool
	// Banner, when set, explains the values below the fields, or what is
	// wrong with them.
	Banner func(values map[string]string) (text string, danger bool)
}

func templateField(name, label string) TemplateField {
	return TemplateField{Name: name, Label: label, Kind: "text"}
}
func templateChoice(name, label, initial string, options ...string) TemplateField {
	return TemplateField{Name: name, Label: label, Kind: "select", Default: initial, Options: options}
}
func builtinTemplateDefinitions() []TemplateDefinition {
	input := TemplateField{Name: "input", Label: "Input", Kind: "multiline"}
	value := TemplateField{Name: "value", Label: "Value", Kind: "multiline"}
	result := templateChoice("result", "Return", "first", "first", "all", "join")
	request := TemplateField{Name: "request", Label: "Request", Kind: "request"}
	fields := []TemplateDefinition{
		{Name: "uuid.v1", Description: "Generate a UUID V1", Preview: true},
		{Name: "uuid.v3", Description: "Generate a UUID V3", Preview: true, Fields: []TemplateField{templateField("name", "Name"), {Name: "namespace", Label: "Namespace UUID", Kind: "text", Placeholder: "24ced880-3bf4-11f0-8329-cd053d577f0e"}}},
		{Name: "uuid.v4", Description: "Generate a UUID V4", Preview: true},
		{Name: "uuid.v5", Description: "Generate a UUID V5", Preview: true, Fields: []TemplateField{templateField("name", "Name"), templateField("namespace", "Namespace")}},
		{Name: "uuid.v6", Description: "Generate a UUID V6", Preview: true, Fields: []TemplateField{{Name: "timestamp", Label: "Timestamp", Kind: "text", Placeholder: "2025-05-28T11:15:00Z"}}},
		{Name: "uuid.v7", Description: "Generate a UUID V7", Preview: true},
		onePasswordDefinition(),
		{Name: "random", Description: "Random hexadecimal text", Fields: []TemplateField{{Name: "length", Label: "Length", Default: "16", Kind: "text"}}, Preview: true},
		{Name: "random.range", Description: "Random number in a range", Fields: []TemplateField{{Name: "min", Label: "Minimum", Default: "0", Kind: "text"}, {Name: "max", Label: "Maximum", Default: "1", Kind: "text"}, templateField("decimals", "Decimal places")}, Preview: true},
		{Name: "base64.encode", Description: "Encode text as Base64", Fields: []TemplateField{value, templateChoice("encoding", "Encoding", "base64", "base64", "base64url")}, Preview: true},
		{Name: "base64.decode", Description: "Decode Base64 text", Fields: []TemplateField{value}, Preview: true},
		{Name: "url.encode", Description: "Encode a URL component", Fields: []TemplateField{value}, Preview: true},
		{Name: "url.decode", Description: "Decode a URL component", Fields: []TemplateField{value}, Preview: true},
		{Name: "json.escape", Description: "Escape text for JSON", Fields: []TemplateField{input}, Preview: true},
		{Name: "json.minify", Description: "Remove JSON whitespace", Fields: []TemplateField{input}, Preview: true},
		{Name: "json.jsonpath", Description: "Extract a JSON value", Fields: []TemplateField{input, templateField("query", "JSONPath"), result, templateField("join", "Separator"), {Name: "formatted", Label: "Pretty print", Kind: "boolean"}}, Preview: true},
		{Name: "xml.xpath", Description: "Extract an XML value", Fields: []TemplateField{input, templateField("query", "XPath"), result, templateField("join", "Separator")}, Preview: true},
		{Name: "regex.match", Description: "Extract a regular-expression match", Fields: []TemplateField{input, templateField("regex", "Regular expression")}, Preview: true},
		{Name: "regex.replace", Description: "Replace regular-expression matches", Fields: []TemplateField{input, templateField("regex", "Regular expression"), templateField("replacement", "Replacement"), {Name: "flags", Label: "Flags", Default: "g", Kind: "text"}}, Preview: true},
		{Name: "fs.readFile", Description: "Read a local file", Fields: []TemplateField{{Name: "path", Label: "File", Kind: "file"}, templateChoice("encoding", "Encoding", "utf8", "utf8", "base64", "hex"), {Name: "trim", Label: "Trim whitespace", Kind: "boolean"}}},
		{Name: "secure", Description: "Encrypt a value using the workspace key", Fields: []TemplateField{{Name: "value", Label: "Value", Kind: "multiline", Secret: true}}},
		promptTextDefinition(),
		{Name: "keychain", Description: "Read a value from the OS keychain", Fields: []TemplateField{templateField("service", "Service"), templateField("account", "Account")}},
		{Name: "cookie.value", Description: "Read a stored cookie", Fields: []TemplateField{templateField("name", "Cookie name"), templateField("domain", "Domain")}, Preview: true},
		{Name: "ctx.workspace", Description: "Current workspace ID", Preview: true},
		{Name: "ctx.environment", Description: "Selected environment ID", Preview: true},
		{Name: "ctx.request", Description: "Current request ID", Preview: true},
		{Name: "request.name", Description: "Name of another request", Fields: []TemplateField{{Name: "requestId", Label: "Request", Kind: "request"}}, Preview: true},
		{Name: "request.header", Description: "Request header value", Fields: []TemplateField{{Name: "requestId", Label: "Request", Kind: "request"}, templateField("header", "Header name")}, Preview: true},
		{Name: "request.param", Description: "Request query parameter", Fields: []TemplateField{{Name: "requestId", Label: "Request", Kind: "request"}, templateField("param", "Parameter name")}, Preview: true},
		{Name: "request.body.raw", Description: "Request body", Fields: []TemplateField{{Name: "requestId", Label: "Request", Kind: "request"}}, Preview: true},
		{Name: "request.body.path", Description: "Extract a value from a request body", Fields: []TemplateField{{Name: "requestId", Label: "Request", Kind: "request"}, templateField("path", "JSONPath or XPath"), result, templateField("join", "Separator")}, Preview: true},
		{Name: "response.header", Description: "Response header value", Fields: []TemplateField{request, templateField("header", "Header name")}, Preview: true},
		{Name: "response.body.raw", Description: "Response body", Fields: []TemplateField{request}, Preview: true},
		{Name: "response.body.path", Description: "Extract a value from a response", Fields: []TemplateField{request, templateField("path", "JSONPath or XPath"), result, templateField("join", "Separator")}, Preview: true},
	}
	for _, name := range []string{"unix", "unixMillis", "iso8601", "format", "offset"} {
		d := TemplateDefinition{Name: "timestamp." + name, Description: "Format a timestamp", Fields: []TemplateField{templateField("date", "Date (empty for now)")}, Preview: true}
		if name == "format" {
			d.Fields = append(d.Fields, TemplateField{Name: "format", Label: "Format", Default: "yyyy-MM-dd", Kind: "text"})
		}
		if name == "offset" {
			d.Fields = append(d.Fields, templateField("expression", "Offset expression"))
		}
		fields = append(fields, d)
	}
	for _, algorithm := range []string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512", "sha3-256", "sha3-512"} {
		for _, prefix := range []string{"hash.", "hmac."} {
			f := []TemplateField{input}
			if prefix == "hmac." {
				f = append(f, TemplateField{Name: "key", Label: "Key", Kind: "text", Secret: true})
			}
			f = append(f, templateChoice("encoding", "Encoding", "hex", "hex", "base64", "base64url"))
			fields = append(fields, TemplateDefinition{Name: prefix + algorithm, Description: "Hash the input", Fields: f, Preview: true})
		}
	}
	for i := range fields {
		if strings.HasPrefix(fields[i].Name, "response.") {
			fields[i].Fields = append(fields[i].Fields, templateChoice("behavior", "Send referenced request", "smart", "smart", "always", "ttl", "never"), TemplateField{Name: "ttl", Label: "Cache duration (seconds)", Default: "0", Kind: "text"})
		}
	}
	return fields
}
func (e *Engine) TemplateDefinitions() []TemplateDefinition {
	result := builtinTemplateDefinitions()
	seen := map[string]bool{}
	for _, d := range result {
		seen[d.Name] = true
	}
	if e != nil {
		for _, p := range e.pluginList() {
			if !p.info.Enabled || p.info.Error != "" {
				continue
			}
			for _, f := range p.definition.Templates {
				if seen[f.Name] {
					continue
				}
				seen[f.Name] = true
				d := TemplateDefinition{Name: f.Name, Description: f.Description}
				for _, field := range f.Fields {
					kind := "text"
					if len(field.Options) > 0 {
						kind = "select"
					}
					d.Fields = append(d.Fields, TemplateField{Name: field.Name, Label: cmp.Or(field.Label, field.Name), Kind: kind, Default: field.Default, Options: slices.Clone(field.Options), Secret: field.Secret})
				}
				result = append(result, d)
			}
		}
	}
	slices.SortFunc(result, func(a, b TemplateDefinition) int { return strings.Compare(a.Name, b.Name) })
	return result
}

// TemplateExpression is an editable expression tree for a template argument or tag.
type TemplateExpression struct {
	Kind, Value string
	Arguments   map[string]TemplateExpression
}

func ParseTemplateExpression(source string) (TemplateExpression, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "${[") {
		if !strings.HasSuffix(source, "]}") {
			return TemplateExpression{}, errors.New("template tag is incomplete")
		}
		source = strings.TrimSpace(source[3 : len(source)-2])
	}
	p := templateParser{chars: []rune(source)}
	value, err := p.value()
	if err != nil {
		return TemplateExpression{}, err
	}
	p.space()
	if value == nil || p.pos != len(p.chars) {
		return TemplateExpression{}, errors.New("unexpected text in template value")
	}
	var convert func(templateValue) TemplateExpression
	convert = func(v templateValue) TemplateExpression {
		out := TemplateExpression{Kind: v.kind, Value: v.text}
		if v.kind == "function" {
			out.Arguments = map[string]TemplateExpression{}
			for k, arg := range v.args {
				out.Arguments[k] = convert(arg)
			}
		}
		return out
	}
	return convert(*value), nil
}
func FormatTemplateExpression(value TemplateExpression) string {
	switch value.Kind {
	case "variable":
		return value.Value
	case "number", "boolean":
		return value.Value
	case "null":
		return "null"
	case "function":
		parts := []string{}
		for _, key := range slices.Sorted(maps.Keys(value.Arguments)) {
			// Like Yaak, null arguments are left out.
			if value.Arguments[key].Kind == "null" {
				continue
			}
			parts = append(parts, key+"="+FormatTemplateExpression(value.Arguments[key]))
		}
		return value.Value + "(" + strings.Join(parts, ", ") + ")"
	default:
		return formatTemplateString(value.Value)
	}
}
func FormatTemplateTag(value TemplateExpression) string {
	return "${[ " + FormatTemplateExpression(value) + " ]}"
}

type templatePreviewKey struct{}
type templateRequestKey struct{}

func (e *Engine) PreviewTemplate(ctx context.Context, source, workspace, folder, environment, request string, cookieJar ...string) (string, error) {
	ctx = context.WithValue(ctx, templatePreviewKey{}, true)
	ctx = context.WithValue(ctx, templateRequestKey{}, request)
	if len(cookieJar) > 0 {
		ctx = context.WithValue(ctx, selectedCookieJarKey{}, cookieJar[0])
	}
	return e.Render(ctx, source, workspace, folder, environment)
}
func isTemplatePreview(ctx context.Context) bool {
	value, _ := ctx.Value(templatePreviewKey{}).(bool)
	return value
}
func (e *Engine) templatePreviewAllowed(name string) error {
	aliases := map[string]string{"uuid": "uuid.v4", "base64": "base64.encode", "encode.base64": "base64.encode", "decode.base64": "base64.decode", "encode.uri": "url.encode", "encode.url": "url.encode", "decode.uri": "url.decode", "decode.url": "url.decode", "timestamp": "timestamp.iso8601", "json": "json.jsonpath", "request.body": "request.body.raw", "response.body": "response.body.raw", "response.url": "response.body.raw", "response": "response.body.raw"}
	name = cmp.Or(aliases[name], name)
	for _, definition := range e.TemplateDefinitions() {
		if definition.Name == name {
			if definition.Preview {
				return nil
			}
			return fmt.Errorf("%s is evaluated when the request is sent", name)
		}
	}
	return fmt.Errorf("%s is evaluated when the request is sent", name)
}

// promptTextDefinition is the prompt.text of Yaak's prompt plugin.
func promptTextDefinition() TemplateDefinition {
	storing := func(v map[string]string) bool { return promptStore(v) != promptStoreNone }
	return TemplateDefinition{
		Name:        "prompt.text",
		Description: "Prompt the user for input when sending a request",
		Fields: []TemplateField{
			templateField("label", "Label"),
			{Name: "store", Label: "Store Input", Kind: "select", Default: promptStoreNone, Options: []string{promptStoreNone, promptStoreExpire, promptStoreForever}, OptionLabels: []string{"Never", "Expire", "Forever"}},
			{Name: "namespace", Label: "Namespace", Kind: "text", Default: "${[ctx.workspace()]}", Visible: storing},
			{Name: "key", Label: "Key (defaults to Label)", Kind: "text", Visible: storing},
			{Name: "ttl", Label: "TTL (seconds)", Kind: "text", Default: "0", Placeholder: "0", Visible: func(v map[string]string) bool { return promptStore(v) == promptStoreExpire }},
			{Name: "title", Label: "Prompt Title", Kind: "text", Placeholder: "Enter Value", Advanced: true},
			{Name: "defaultValue", Label: "Default Value", Kind: "text", Advanced: true},
			{Name: "placeholder", Label: "Input Placeholder", Kind: "text", Advanced: true},
			{Name: "password", Label: "Mask Value", Kind: "boolean", Advanced: true},
		},
		Banner: func(v map[string]string) (string, bool) {
			if !storing(v) {
				return "", false
			}
			key, err := promptKey(v)
			if err != nil {
				return err.Error(), true
			}
			return "Value will be saved under: " + key, false
		},
	}
}
