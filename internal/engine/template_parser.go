package engine

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"yeek/locales"
)

const (
	maxTemplateDepth  = 50
	maxTemplateOutput = 128 << 20
)

var errTemplateStackExceeded error = &localizedError{locales.TemplateNestingIsTooDeep}

var errTemplateOutputTooLarge error = &localizedError{locales.TemplateOutputIsTooLarge}

type templateRunner func(string, map[string]string) (string, error)

type templateValue struct {
	kind, text string
	args       map[string]templateValue
	order      []string
}

type templateToken struct {
	raw string
	tag *templateValue
}

type templateParser struct {
	chars  []rune
	pos    int
	depth  int
	tokens []templateToken
	text   strings.Builder
}

func parseTemplate(text string) ([]templateToken, error) {
	p := &templateParser{chars: []rune(text)}
	for p.pos < len(p.chars) {
		switch {
		case p.match(`\\`):
			p.text.WriteString(`\\`)
		case p.match(`\${[`):
			p.text.WriteString("${[")
		case p.match("${["):
			start := p.pos
			value, err := p.tag()
			if err != nil {
				return nil, err
			}
			if value == nil {
				p.pos = start
				p.text.WriteString("${[")
				continue
			}
			p.flush()
			p.tokens = append(p.tokens, templateToken{tag: value})
		default:
			p.text.WriteRune(p.chars[p.pos])
			p.pos++
		}
	}
	p.flush()
	return p.tokens, nil
}

func (p *templateParser) flush() {
	if p.text.Len() > 0 {
		p.tokens = append(p.tokens, templateToken{raw: p.text.String()})
		p.text.Reset()
	}
}

func (p *templateParser) match(s string) bool {
	r := []rune(s)
	if p.pos+len(r) > len(p.chars) || string(p.chars[p.pos:p.pos+len(r)]) != s {
		return false
	}
	p.pos += len(r)
	return true
}

func (p *templateParser) space() {
	for p.pos < len(p.chars) && unicode.IsSpace(p.chars[p.pos]) {
		p.pos++
	}
}

func (p *templateParser) tag() (*templateValue, error) {
	p.space()
	value, err := p.value()
	if err != nil || value == nil {
		return nil, err
	}
	p.space()
	if !p.match("]}") {
		return nil, nil
	}
	return value, nil
}

func (p *templateParser) value() (*templateValue, error) {
	if fn, err := p.function(); err != nil || fn != nil {
		return fn, err
	}
	if text, ok, err := p.str(); err != nil || ok {
		return &templateValue{kind: "string", text: text}, err
	}
	name := p.ident()
	switch name {
	case "":
		return nil, nil
	case "null":
		return &templateValue{kind: "null"}, nil
	case "true", "false":
		return &templateValue{kind: "boolean", text: name}, nil
	}
	return &templateValue{kind: "variable", text: name}, nil
}

func (p *templateParser) function() (*templateValue, error) {
	start := p.pos
	name := p.scan(func(r rune, _ bool) bool { return isAlnum(r) || r == '_' || r == '.' })
	if name == "" || !p.match("(") {
		p.pos = start
		return nil, nil
	}
	if p.depth >= maxTemplateDepth {
		return nil, errTemplateStackExceeded
	}
	p.depth++
	defer func() { p.depth-- }()
	fn := &templateValue{kind: "function", text: name, args: map[string]templateValue{}}
	p.space()
	if p.match(")") {
		return fn, nil
	}
	for p.pos < len(p.chars) {
		p.space()
		arg := p.ident()
		p.space()
		p.match("=")
		p.space()
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		p.space()
		if arg == "" || value == nil {
			p.pos = start
			return nil, nil
		}
		if _, seen := fn.args[arg]; !seen {
			fn.order = append(fn.order, arg)
		}
		fn.args[arg] = *value
		if p.match(")") {
			return fn, nil
		}
		p.space()
		if !p.match(",") {
			p.pos = start
			return nil, nil
		}
	}
	return fn, nil
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func (p *templateParser) scan(valid func(r rune, first bool) bool) string {
	start := p.pos
	for p.pos < len(p.chars) && valid(p.chars[p.pos], p.pos == start) {
		p.pos++
	}
	return string(p.chars[start:p.pos])
}

func (p *templateParser) ident() string {
	return p.scan(func(r rune, first bool) bool {
		return isAlnum(r) || r == '_' || !first && (r == '-' || r == '.')
	})
}

func (p *templateParser) str() (string, bool, error) {
	start := p.pos
	quote, b64 := '\'', false
	switch {
	case p.match("b64'"):
		b64 = true
	case p.match("'"):
	case p.match(`"`):
		quote = '"'
	default:
		return "", false, nil
	}
	var text strings.Builder
	for p.pos < len(p.chars) {
		r := p.chars[p.pos]
		p.pos++
		if r == '\\' && p.pos < len(p.chars) {
			if quote == '"' {
				rest := string(p.chars[p.pos-1:])
				decoded, _, tail, err := strconv.UnquoteChar(rest, '"')
				if err != nil {
					break
				}
				text.WriteRune(decoded)
				p.pos += utf8.RuneCountInString(rest[:len(rest)-len(tail)]) - 1
				continue
			}
			text.WriteRune(p.chars[p.pos])
			p.pos++
			continue
		}
		if r == quote {
			if !b64 {
				return text.String(), true, nil
			}
			decoded, err := base64.RawURLEncoding.DecodeString(text.String())
			if err != nil {
				return "", false, errors.New(locales.FailedToDecodeStringString(text.String())) //nolint:staticcheck
			}
			if !utf8.Valid(decoded) {
				return "", false, errors.New(locales.FailedToDecodeUtf8StringString(text.String())) //nolint:staticcheck
			}
			return string(decoded), true, nil
		}
		text.WriteRune(r)
	}
	p.pos = start
	return "", false, nil
}

func formatTemplateString(text string) string {
	if strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return "b64'" + base64.RawURLEncoding.EncodeToString([]byte(text)) + "'"
	}
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(text) + "'"
}

func renderText(text string, vars map[string]string, stack map[string]bool, depth int) (string, error) {
	return renderWith(text, vars, stack, depth, nil)
}

type templateRender struct {
	vars   map[string]string
	stack  map[string]bool
	run    templateRunner
	budget int
}

func renderWith(text string, vars map[string]string, stack map[string]bool, depth int, run templateRunner) (string, error) {
	r := &templateRender{vars: vars, stack: stack, run: run, budget: maxTemplateOutput}
	return r.render(text, depth)
}

func (r *templateRender) write(out *strings.Builder, text string) error {
	r.budget -= len(text)
	if r.budget < 0 {
		return errTemplateOutputTooLarge
	}
	out.WriteString(text)
	return nil
}

func (r *templateRender) render(text string, depth int) (string, error) {
	tokens, err := parseTemplate(text)
	if err != nil {
		return "", err
	}
	depth += 2
	if depth > maxTemplateDepth {
		return "", errTemplateStackExceeded
	}
	var result strings.Builder
	for _, token := range tokens {
		text := token.raw
		if token.tag != nil {
			if text, err = r.eval(*token.tag, depth); err != nil {
				return "", err
			}
		}
		if err = r.write(&result, text); err != nil {
			return "", err
		}
	}
	return result.String(), nil
}

func serverTemplateResult(name string) bool {
	return name == "response" || name == "cookie.value" || strings.HasPrefix(name, "response.")
}

func (r *templateRender) eval(value templateValue, depth int) (string, error) {
	if depth > maxTemplateDepth {
		return "", errTemplateStackExceeded
	}
	switch value.kind {
	case "string":
		return r.render(value.text, depth)
	case "boolean", "null":
		return value.text, nil
	case "function":
		args := map[string]string{}
		for name, argument := range value.args {
			rendered, err := r.eval(argument, depth+1)
			if err != nil {
				return "", err
			}
			args[name] = rendered
		}
		var result string
		var err error
		if r.run != nil {
			result, err = r.run(value.text, args)
		} else {
			result, err = templateFunction(value.text, args)
		}
		if err != nil {
			return "", err
		}
		if serverTemplateResult(value.text) {
			return result, nil
		}
		return r.render(result, depth)
	case "variable":
		name := value.text
		text, ok := r.vars[name]
		if !ok && strings.HasPrefix(name, "_.") {
			name = strings.TrimPrefix(name, "_.")
			text, ok = r.vars[name]
		}
		if !ok {
			return "", errors.New(locales.VariableNameIsNotDefined(strconv.Quote(name)))
		}
		if r.stack[name] {
			return "", errors.New(locales.VariableNameRefersToItself(strconv.Quote(name)))
		}
		r.stack[name] = true
		defer delete(r.stack, name)
		return r.render(text, depth)
	default:
		return "", errors.New(locales.UnknownTemplateValue())
	}
}
