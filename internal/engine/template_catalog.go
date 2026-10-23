package engine

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"yeek/locales"
)

type TemplateField struct {
	Name, Label, Kind, Default string
	Options                    []string
	OptionLabels               []string
	Placeholder                string
	Secret                     bool
	Advanced                   bool
	Visible                    func(values map[string]string) bool
	Describe                   func(values map[string]string) (label, description string, secret, visible bool)
	Dynamic                    func(ctx context.Context, values map[string]string) (options, labels []string, err error)
}
type TemplateDefinition struct {
	Name, Description string
	Fields            []TemplateField
	Preview           bool
	Banner            func(values map[string]string) (text string, danger bool)
}

func templateField(name, label string) TemplateField {
	return TemplateField{Name: name, Label: label, Kind: "text"}
}
func templateChoice(name, label, initial string, options ...string) TemplateField {
	return TemplateField{Name: name, Label: label, Kind: "select", Default: initial, Options: options}
}
func builtinTemplateDefinitions() []TemplateDefinition {
	input := TemplateField{Name: "input", Label: locales.Input(), Kind: "multiline"}
	value := TemplateField{Name: "value", Label: locales.ValueTitle(), Kind: "multiline"}
	result := templateChoice("result", locales.Return(), "first", "first", "all", "join")
	request := TemplateField{Name: "request", Label: locales.Request(), Kind: "request"}
	fields := []TemplateDefinition{
		{Name: "uuid.v1", Description: locales.GenerateAUUIDV1(), Preview: true},
		{Name: "uuid.v3", Description: locales.GenerateAUUIDV3(), Preview: true, Fields: []TemplateField{templateField("name", locales.NameTitle()), {Name: "namespace", Label: locales.NamespaceUUID(), Kind: "text", Placeholder: "24ced880-3bf4-11f0-8329-cd053d577f0e"}}},
		{Name: "uuid.v4", Description: locales.GenerateAUUIDV4(), Preview: true},
		{Name: "uuid.v5", Description: locales.GenerateAUUIDV5(), Preview: true, Fields: []TemplateField{templateField("name", locales.NameTitle()), templateField("namespace", locales.Namespace())}},
		{Name: "uuid.v6", Description: locales.GenerateAUUIDV6(), Preview: true, Fields: []TemplateField{{Name: "timestamp", Label: locales.Timestamp(), Kind: "text", Placeholder: "2025-05-28T11:15:00Z"}}},
		{Name: "uuid.v7", Description: locales.GenerateAUUIDV7(), Preview: true},
		onePasswordDefinition(),
		{Name: "random", Description: locales.RandomHexadecimalText(), Fields: []TemplateField{{Name: "length", Label: locales.Length(), Default: "16", Kind: "text"}}, Preview: true},
		{Name: "random.range", Description: locales.RandomNumberInARange(), Fields: []TemplateField{{Name: "min", Label: locales.Minimum(), Default: "0", Kind: "text"}, {Name: "max", Label: locales.Maximum(), Default: "1", Kind: "text"}, templateField("decimals", locales.DecimalPlaces())}, Preview: true},
		{Name: "base64.encode", Description: locales.EncodeTextAsBase64(), Fields: []TemplateField{value, templateChoice("encoding", locales.Encoding(), "base64", "base64", "base64url")}, Preview: true},
		{Name: "base64.decode", Description: locales.DecodeBase64Text(), Fields: []TemplateField{value}, Preview: true},
		{Name: "url.encode", Description: locales.EncodeAURLComponent(), Fields: []TemplateField{value}, Preview: true},
		{Name: "url.decode", Description: locales.DecodeAURLComponent(), Fields: []TemplateField{value}, Preview: true},
		{Name: "json.escape", Description: locales.EscapeTextForJSON(), Fields: []TemplateField{input}, Preview: true},
		{Name: "json.minify", Description: locales.RemoveJSONWhitespace(), Fields: []TemplateField{input}, Preview: true},
		{Name: "json.jsonpath", Description: locales.ExtractAJSONValue(), Fields: []TemplateField{input, templateField("query", locales.JSONPath()), result, templateField("join", locales.Separator()), {Name: "formatted", Label: locales.PrettyPrint(), Kind: "boolean"}}, Preview: true},
		{Name: "xml.xpath", Description: locales.ExtractAnXMLValue(), Fields: []TemplateField{input, templateField("query", locales.XPath()), result, templateField("join", locales.Separator())}, Preview: true},
		{Name: "regex.match", Description: locales.ExtractARegularExpressionMatch(), Fields: []TemplateField{input, templateField("regex", locales.RegularExpressionTitle())}, Preview: true},
		{Name: "regex.replace", Description: locales.ReplaceRegularExpressionMatches(), Fields: []TemplateField{input, templateField("regex", locales.RegularExpressionTitle()), templateField("replacement", locales.Replacement()), {Name: "flags", Label: locales.Flags(), Default: "g", Kind: "text"}}, Preview: true},
		{Name: "fs.readFile", Description: locales.ReadALocalFile(), Fields: []TemplateField{{Name: "path", Label: locales.File(), Kind: "file"}, templateChoice("encoding", locales.Encoding(), "utf8", "utf8", "base64", "hex"), {Name: "trim", Label: locales.TrimWhitespace(), Kind: "boolean"}}},
		{Name: "secure", Description: locales.EncryptAValueUsingTheWorkspace(), Fields: []TemplateField{{Name: "value", Label: locales.ValueTitle(), Kind: "multiline", Secret: true}}},
		promptTextDefinition(),
		{Name: "keychain", Description: locales.ReadAValueFromTheOS(), Fields: []TemplateField{templateField("service", locales.Service()), templateField("account", locales.Account())}},
		{Name: "cookie.value", Description: locales.ReadAStoredCookie(), Fields: []TemplateField{templateField("name", locales.CookieName()), templateField("domain", locales.Domain())}, Preview: true},
		{Name: "ctx.workspace", Description: locales.CurrentWorkspaceID(), Preview: true},
		{Name: "ctx.environment", Description: locales.SelectedEnvironmentID(), Preview: true},
		{Name: "ctx.request", Description: locales.CurrentRequestID(), Preview: true},
		{Name: "request.name", Description: locales.NameOfAnotherRequest(), Fields: []TemplateField{{Name: "requestId", Label: locales.Request(), Kind: "request"}}, Preview: true},
		{Name: "request.header", Description: locales.RequestHeaderValue(), Fields: []TemplateField{{Name: "requestId", Label: locales.Request(), Kind: "request"}, templateField("header", locales.HeaderNameTitle())}, Preview: true},
		{Name: "request.param", Description: locales.RequestQueryParameter(), Fields: []TemplateField{{Name: "requestId", Label: locales.Request(), Kind: "request"}, templateField("param", locales.ParameterName())}, Preview: true},
		{Name: "request.body.raw", Description: locales.RequestBodyTitle(), Fields: []TemplateField{{Name: "requestId", Label: locales.Request(), Kind: "request"}}, Preview: true},
		{Name: "request.body.path", Description: locales.ExtractAValueFromARequest(), Fields: []TemplateField{{Name: "requestId", Label: locales.Request(), Kind: "request"}, templateField("path", locales.JSONPathOrXPath()), result, templateField("join", locales.Separator())}, Preview: true},
		{Name: "response.header", Description: locales.ResponseHeaderValue(), Fields: []TemplateField{request, templateField("header", locales.HeaderNameTitle())}, Preview: true},
		{Name: "response.body.raw", Description: locales.ResponseBody(), Fields: []TemplateField{request}, Preview: true},
		{Name: "response.body.path", Description: locales.ExtractAValueFromAResponse(), Fields: []TemplateField{request, templateField("path", locales.JSONPathOrXPath()), result, templateField("join", locales.Separator())}, Preview: true},
	}
	for _, name := range []string{"unix", "unixMillis", "iso8601", "format", "offset"} {
		d := TemplateDefinition{Name: "timestamp." + name, Description: locales.FormatATimestamp(), Fields: []TemplateField{templateField("date", locales.DateEmptyForNow())}, Preview: true}
		if name == "format" {
			d.Fields = append(d.Fields, TemplateField{Name: "format", Label: locales.Format(), Default: "yyyy-MM-dd", Kind: "text"})
		}
		if name == "offset" {
			d.Fields = append(d.Fields, templateField("expression", locales.OffsetExpression()))
		}
		fields = append(fields, d)
	}
	for _, algorithm := range []string{"md5", "sha1", "sha224", "sha256", "sha384", "sha512", "sha3-256", "sha3-512"} {
		for _, prefix := range []string{"hash.", "hmac."} {
			f := []TemplateField{input}
			if prefix == "hmac." {
				f = append(f, TemplateField{Name: "key", Label: locales.Key(), Kind: "text", Secret: true})
			}
			f = append(f, templateChoice("encoding", locales.Encoding(), "hex", "hex", "base64", "base64url"))
			fields = append(fields, TemplateDefinition{Name: prefix + algorithm, Description: locales.HashTheInput(), Fields: f, Preview: true})
		}
	}
	for i := range fields {
		if strings.HasPrefix(fields[i].Name, "response.") {
			fields[i].Fields = append(fields[i].Fields, templateChoice("behavior", locales.SendReferencedRequest(), "smart", "smart", "always", "ttl", "never"), TemplateField{Name: "ttl", Label: locales.CacheDurationSeconds(), Default: "0", Kind: "text"})
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

type TemplateExpression struct {
	Kind, Value string
	Arguments   map[string]TemplateExpression
}

func ParseTemplateExpression(source string) (TemplateExpression, error) {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "${[") {
		if !strings.HasSuffix(source, "]}") {
			return TemplateExpression{}, errors.New(locales.TemplateTagIsIncomplete())
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
		return TemplateExpression{}, errors.New(locales.UnexpectedTextInTemplateValue())
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

func promptTextDefinition() TemplateDefinition {
	storing := func(v map[string]string) bool { return promptStore(v) != promptStoreNone }
	return TemplateDefinition{
		Name:        "prompt.text",
		Description: locales.PromptTheUserForInputWhen(),
		Fields: []TemplateField{
			templateField("label", locales.Label()),
			{Name: "store", Label: locales.StoreInput(), Kind: "select", Default: promptStoreNone, Options: []string{promptStoreNone, promptStoreExpire, promptStoreForever}, OptionLabels: []string{locales.Never(), locales.Expire(), locales.Forever()}},
			{Name: "namespace", Label: locales.Namespace(), Kind: "text", Default: "${[ctx.workspace()]}", Visible: storing},
			{Name: "key", Label: locales.KeyDefaultsToLabel(), Kind: "text", Visible: storing},
			{Name: "ttl", Label: locales.TTLSeconds(), Kind: "text", Default: "0", Placeholder: "0", Visible: func(v map[string]string) bool { return promptStore(v) == promptStoreExpire }},
			{Name: "title", Label: locales.PromptTitle(), Kind: "text", Placeholder: "Enter Value", Advanced: true},
			{Name: "defaultValue", Label: locales.DefaultValue(), Kind: "text", Advanced: true},
			{Name: "placeholder", Label: locales.InputPlaceholder(), Kind: "text", Advanced: true},
			{Name: "password", Label: locales.MaskValue(), Kind: "boolean", Advanced: true},
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
