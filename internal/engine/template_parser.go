package engine

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type templateRunner func(string, map[string]string) (string, error)
type templateValue struct {
	kind, text string
	args       map[string]templateValue
}
type valueParser struct {
	text []rune
	pos  int
}

func (p *valueParser) space() {
	for p.pos < len(p.text) && unicode.IsSpace(p.text[p.pos]) {
		p.pos++
	}
}
func (p *valueParser) consume(r rune) bool {
	p.space()
	if p.pos < len(p.text) && p.text[p.pos] == r {
		p.pos++
		return true
	}
	return false
}
func (p *valueParser) identifier() string {
	p.space()
	start := p.pos
	for p.pos < len(p.text) {
		r := p.text[p.pos]
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '-' {
			break
		}
		p.pos++
	}
	return string(p.text[start:p.pos])
}
func (p *valueParser) value(depth int) (templateValue, error) {
	if depth > 32 {
		return templateValue{}, errors.New("template nesting exceeds 32 levels")
	}
	p.space()
	if p.pos >= len(p.text) {
		return templateValue{}, errors.New("template is missing a value")
	}
	if quote := p.text[p.pos]; quote == '\'' || quote == '"' {
		p.pos++
		var value strings.Builder
		for p.pos < len(p.text) {
			r := p.text[p.pos]
			p.pos++
			if r == quote {
				return templateValue{kind: "string", text: value.String()}, nil
			}
			if r == '\\' {
				rest := string(p.text[p.pos-1:])
				decoded, _, tail, err := strconv.UnquoteChar(rest, byte(quote))
				if err != nil {
					return templateValue{}, errors.New("invalid escape in template string")
				}
				p.pos += utf8.RuneCountInString(rest[:len(rest)-len(tail)]) - 1
				r = decoded
			}
			value.WriteRune(r)
		}
		return templateValue{}, errors.New("template string is missing its closing quote")
	}
	name := p.identifier()
	if name == "" {
		return templateValue{}, fmt.Errorf("unexpected character %q in template", p.text[p.pos])
	}
	if p.consume('(') {
		value := templateValue{kind: "function", text: name, args: map[string]templateValue{}}
		if p.consume(')') {
			return value, nil
		}
		for {
			key := p.identifier()
			if key == "" || !p.consume('=') {
				return templateValue{}, errors.New("template arguments must use name=value")
			}
			if _, exists := value.args[key]; exists {
				return templateValue{}, fmt.Errorf("duplicate template argument %q", key)
			}
			argument, err := p.value(depth + 1)
			if err != nil {
				return templateValue{}, err
			}
			value.args[key] = argument
			if p.consume(')') {
				return value, nil
			}
			if !p.consume(',') {
				return templateValue{}, errors.New("template arguments must be separated by commas")
			}
			if p.consume(')') {
				return value, nil
			}
		}
	}
	if _, err := strconv.ParseFloat(name, 64); err == nil {
		return templateValue{kind: "number", text: name}, nil
	}
	switch name {
	case "true", "false":
		return templateValue{kind: "boolean", text: name}, nil
	case "null":
		return templateValue{kind: "null"}, nil
	}
	return templateValue{kind: "variable", text: name}, nil
}
func renderText(text string, vars map[string]string, stack map[string]bool, depth int) (string, error) {
	return renderWith(text, vars, stack, depth, nil)
}
func renderWith(text string, vars map[string]string, stack map[string]bool, depth int, run templateRunner) (string, error) {
	if depth > 32 {
		return "", errors.New("template nesting exceeds 32 levels")
	}
	var result strings.Builder
	for len(text) > 0 {
		start := strings.Index(text, "${[")
		legacy := false
		at := strings.Index(text, "{{")
		if at >= 0 && (start < 0 || at < start) {
			start = at
			legacy = true
		}
		if start < 0 {
			result.WriteString(text)
			break
		}
		result.WriteString(text[:start])
		prefix, close := 3, "]}"
		if legacy {
			prefix, close = 2, "}}"
		}
		source := text[start+prefix:]
		quote := byte(0)
		escaped := false
		end := -1
		for i := 0; i < len(source)-1; i++ {
			ch := source[i]
			if escaped {
				escaped = false
				continue
			}
			if quote != 0 {
				switch ch {
				case '\\':
					escaped = true
				case quote:
					quote = 0
				}
				continue
			}
			if ch == '\'' || ch == '"' {
				quote = ch
				continue
			}
			if source[i:i+2] == close {
				end = i
				break
			}
		}
		if end < 0 {
			return "", errors.New("template tag is missing its closing delimiter")
		}
		parser := valueParser{text: []rune(source[:end])}
		value, err := parser.value(0)
		if err != nil {
			return "", err
		}
		parser.space()
		if parser.pos != len(parser.text) {
			return "", errors.New("unexpected text after template value")
		}
		rendered, err := evalTemplate(value, vars, stack, depth+1, run)
		if err != nil {
			return "", err
		}
		result.WriteString(rendered)
		text = source[end+2:]
	}
	return result.String(), nil
}
func evalTemplate(value templateValue, vars map[string]string, stack map[string]bool, depth int, run templateRunner) (string, error) {
	if depth > 32 {
		return "", errors.New("template nesting exceeds 32 levels")
	}
	switch value.kind {
	case "string", "number", "boolean", "null":
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
		if run != nil {
			return run(value.text, args)
		}
		return templateFunction(value.text, args)
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
		if stack[name] {
			return "", fmt.Errorf("variable %q refers to itself", name)
		}
		stack[name] = true
		defer delete(stack, name)
		return renderWith(text, vars, stack, depth+1, run)
	default:
		return "", errors.New("unknown template value")
	}
}
