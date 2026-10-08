package engine

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file ports Yaak's yaak-templates parser and renderer
// (crates/yaak-templates/src/{parser,renderer}.rs).

// maxTemplateDepth is Yaak's MAX_DEPTH.
const maxTemplateDepth = 50

var errTemplateStackExceeded = errors.New("template nesting is too deep")

type templateRunner func(string, map[string]string) (string, error)

// templateValue is Yaak's Val: a string, variable, boolean, null or function.
type templateValue struct {
	kind, text string
	args       map[string]templateValue
	// order keeps the arguments as written, like Yaak's Vec<FnArg>.
	order []string
}

// templateToken is raw text, or a tag when tag is set.
type templateToken struct {
	raw string
	tag *templateValue
}

type templateParser struct {
	chars  []rune
	pos    int
	tokens []templateToken
	text   strings.Builder
}

// parseTemplate splits text into raw text and tags. Like Yaak, \${[ is a
// literal "${[", a double backslash stays as written, and a tag that
// doesn't parse is kept as text.
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
	// Yaak accepts arguments that run to the end of the text unclosed; the
	// tag's missing "]}" then turns it back into text.
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

// ident is a variable or argument name: a letter, digit or underscore, then
// those plus - and . too.
func (p *templateParser) ident() string {
	return p.scan(func(r rune, first bool) bool {
		return isAlnum(r) || r == '_' || !first && (r == '-' || r == '.')
	})
}

// str reads '...' or b64'...' (URL-safe base64 without padding). Yeek also
// reads "...", which it wrote before matching Yaak's quoting.
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
				// Yeek's old double-quoted strings used Go escapes.
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
				return "", false, fmt.Errorf("Failed to decode string %s", text.String())
			}
			if !utf8.Valid(decoded) {
				return "", false, fmt.Errorf("Failed to decode utf8 string %s", text.String())
			}
			return string(decoded), true, nil
		}
		text.WriteRune(r)
	}
	p.pos = start
	return "", false, nil
}

// formatTemplateString is Yaak's Val::Str display: single quotes, or b64
// when the text holds control characters a one-line tag can't.
func formatTemplateString(text string) string {
	if strings.IndexFunc(text, unicode.IsControl) >= 0 {
		return "b64'" + base64.RawURLEncoding.EncodeToString([]byte(text)) + "'"
	}
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(text) + "'"
}

func renderText(text string, vars map[string]string, stack map[string]bool, depth int) (string, error) {
	return renderWith(text, vars, stack, depth, nil)
}

// renderWith is Yaak's parse_and_render.
func renderWith(text string, vars map[string]string, stack map[string]bool, depth int, run templateRunner) (string, error) {
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
		if token.tag == nil {
			result.WriteString(token.raw)
			continue
		}
		rendered, err := evalTemplate(*token.tag, vars, stack, depth, run)
		if err != nil {
			return "", err
		}
		result.WriteString(rendered)
	}
	return result.String(), nil
}

// evalTemplate is Yaak's render_value: strings, variable values and
// function results are themselves rendered as templates.
func evalTemplate(value templateValue, vars map[string]string, stack map[string]bool, depth int, run templateRunner) (string, error) {
	if depth > maxTemplateDepth {
		return "", errTemplateStackExceeded
	}
	switch value.kind {
	case "string":
		return renderWith(value.text, vars, stack, depth, run)
	case "boolean", "null":
		return value.text, nil
	case "function":
		args := map[string]string{}
		for name, argument := range value.args {
			rendered, err := evalTemplate(argument, vars, stack, depth+1, run)
			if err != nil {
				return "", err
			}
			args[name] = rendered
		}
		var result string
		var err error
		if run != nil {
			result, err = run(value.text, args)
		} else {
			result, err = templateFunction(value.text, args)
		}
		if err != nil {
			return "", err
		}
		return renderWith(result, vars, stack, depth, run)
	case "variable":
		name := value.text
		text, ok := vars[name]
		if !ok && strings.HasPrefix(name, "_.") {
			name = strings.TrimPrefix(name, "_.")
			text, ok = vars[name]
		}
		if !ok {
			return "", fmt.Errorf("variable %q is not defined", name)
		}
		// Yaak lets a self-reference run into its depth limit; naming the
		// variable is a clearer error.
		if stack[name] {
			return "", fmt.Errorf("variable %q refers to itself", name)
		}
		stack[name] = true
		defer delete(stack, name)
		return renderWith(text, vars, stack, depth, run)
	default:
		return "", errors.New("unknown template value")
	}
}
