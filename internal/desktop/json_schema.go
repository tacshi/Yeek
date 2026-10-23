package desktop

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"yeek/internal/engine"
	"yeek/locales"
)

type jsonFrame struct {
	object    bool
	entry     string
	key       string
	keys      []string
	expectKey bool
	afterKey  bool
}

type jsonCaret struct {
	frames             []jsonFrame
	key, value, quoted bool
	from, to           int
	prefix             string
}

func isJSONWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.+-$", r)
}

func scanJSONCaret(r []rune, caret int) (jsonCaret, bool) {
	var stack []jsonFrame
	push := func(object bool) {
		entry := ""
		if n := len(stack); n > 0 {
			entry = "[]"
			if stack[n-1].object {
				entry = stack[n-1].key
				stack[n-1].afterKey = false
			}
		}
		stack = append(stack, jsonFrame{object: object, entry: entry, expectKey: object})
	}
	top := func() *jsonFrame {
		if len(stack) == 0 {
			return nil
		}
		return &stack[len(stack)-1]
	}
	for i := 0; i < caret && i < len(r); {
		ch := r[i]
		switch {
		case unicode.IsSpace(ch):
			i++
		case ch == '{' || ch == '[':
			push(ch == '{')
			i++
		case ch == '}' || ch == ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			i++
		case ch == ',':
			if f := top(); f != nil && f.object {
				f.expectKey, f.afterKey = true, false
			}
			i++
		case ch == ':':
			i++
		case ch == '"':
			start := i
			i++
			for i < len(r) && r[i] != '"' {
				if r[i] == '\\' {
					i++
				}
				i++
			}
			if i >= caret || i >= len(r) {
				end := min(i+1, len(r))
				c := jsonCaret{frames: stack, quoted: true, from: start, to: end, prefix: string(r[start+1 : min(caret, len(r))])}
				f := top()
				c.key = f != nil && f.object && f.expectKey
				c.value = !c.key
				return c, f != nil
			}
			text := string(r[start+1 : i])
			i++
			if f := top(); f != nil && f.object && f.expectKey {
				f.key, f.keys, f.expectKey, f.afterKey = text, append(f.keys, text), false, true
			} else if f != nil && f.object {
				f.afterKey = false
			}
		case isJSONWord(ch):
			start := i
			for i < len(r) && isJSONWord(r[i]) {
				i++
			}
			if i >= caret {
				f := top()
				c := jsonCaret{frames: stack, from: start, to: i, prefix: string(r[start:caret])}
				c.key = f != nil && f.object && f.expectKey
				c.value = !c.key
				return c, f != nil
			}
			if f := top(); f != nil && f.object {
				f.afterKey = false
			}
		default:
			i++
		}
	}
	f := top()
	if f == nil {
		return jsonCaret{}, false
	}
	c := jsonCaret{frames: stack, from: caret, to: caret}
	c.key = f.object && f.expectKey
	c.value = !c.key && (!f.object || f.afterKey)
	return c, c.key || c.value
}

type jsonPlace struct {
	message *engine.GRPCMessage
	field   *engine.GRPCField
}

func jsonContainer(root *engine.GRPCMessage, frames []jsonFrame) (jsonPlace, bool) {
	place := jsonPlace{message: root}
	for _, f := range frames[1:] {
		var field *engine.GRPCField
		item := place.message == nil
		if item {
			field = place.field
		} else if field = place.message.Field(f.entry); field == nil {
			return jsonPlace{}, false
		}
		switch {
		case !f.object && !item && field.Repeated:
			place = jsonPlace{field: field}
		case f.object && !item && field.Map:
			place = jsonPlace{field: field}
		case f.object && field.Message != nil && (item || !field.Repeated && !field.Map):
			place = jsonPlace{message: field.Message}
		default:
			return jsonPlace{}, false
		}
	}
	return place, true
}

func jsonSchemaOptions(root *engine.GRPCMessage, source string, caret int) []templateOption {
	r := []rune(source)
	at, ok := scanJSONCaret(r, caret)
	if !ok {
		return nil
	}
	place, ok := jsonContainer(root, at.frames)
	if !ok {
		return nil
	}
	top := at.frames[len(at.frames)-1]
	prefix := strings.ToLower(at.prefix)
	var options []templateOption
	add := func(name, apply, detail string) {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			options = append(options, templateOption{name: name, apply: apply, detail: detail, generic: true, from: at.from, end: at.to})
		}
	}
	if at.key {
		if place.message == nil {
			return nil
		}
		colon := ": "
		for i := at.to; i < len(r); i++ {
			if !unicode.IsSpace(r[i]) {
				if r[i] == ':' {
					colon = ""
				}
				break
			}
		}
		for _, f := range place.message.Fields {
			if slices.Contains(top.keys, f.Name) || slices.Contains(top.keys, f.ProtoName) {
				continue
			}
			add(f.Name, `"`+f.Name+`"`+colon, fieldType(f))
		}
		return options
	}
	field := place.field
	if place.message != nil {
		if field = place.message.Field(top.key); field == nil || field.Repeated || field.Map {
			return nil
		}
	}
	if field == nil {
		return nil
	}
	switch field.Kind {
	case "enum":
		for _, name := range field.Enum {
			add(name, `"`+name+`"`, "enum")
		}
	case "boolean":
		add("true", "true", "boolean")
		add("false", "false", "boolean")
	}
	return options
}

func fieldType(f engine.GRPCField) string {
	name := f.Kind
	if f.Message != nil {
		name = f.Message.Name[strings.LastIndex(f.Message.Name, ".")+1:]
	}
	switch {
	case f.Repeated:
		return name + "[]"
	case f.Map:
		return "map<" + name + ">"
	}
	return name
}

type jsonValue struct {
	kind       string
	start, end int
	members    []jsonMember
	items      []*jsonValue
}

type jsonMember struct {
	key              string
	keyStart, keyEnd int
	value            *jsonValue
}

type jsonParser struct {
	r []rune
	i int
}

func (p *jsonParser) space() {
	for p.i < len(p.r) && unicode.IsSpace(p.r[p.i]) {
		p.i++
	}
}

func (p *jsonParser) value(depth int) *jsonValue {
	p.space()
	if p.i >= len(p.r) || depth > 64 {
		return nil
	}
	start := p.i
	switch ch := p.r[p.i]; {
	case ch == '{':
		n := &jsonValue{kind: "object", start: start}
		p.i++
		for {
			p.space()
			if p.i >= len(p.r) {
				return nil
			}
			if p.r[p.i] == '}' {
				p.i++
				n.end = p.i
				return n
			}
			if p.r[p.i] != '"' {
				return nil
			}
			keyStart := p.i
			key, ok := p.string()
			if !ok {
				return nil
			}
			m := jsonMember{key: key, keyStart: keyStart, keyEnd: p.i}
			p.space()
			if p.i >= len(p.r) || p.r[p.i] != ':' {
				return nil
			}
			p.i++
			if m.value = p.value(depth + 1); m.value == nil {
				return nil
			}
			n.members = append(n.members, m)
			p.space()
			if p.i < len(p.r) && p.r[p.i] == ',' {
				p.i++
			}
		}
	case ch == '[':
		n := &jsonValue{kind: "array", start: start}
		p.i++
		for {
			p.space()
			if p.i >= len(p.r) {
				return nil
			}
			if p.r[p.i] == ']' {
				p.i++
				n.end = p.i
				return n
			}
			item := p.value(depth + 1)
			if item == nil {
				return nil
			}
			n.items = append(n.items, item)
			p.space()
			if p.i < len(p.r) && p.r[p.i] == ',' {
				p.i++
			}
		}
	case ch == '"':
		if _, ok := p.string(); !ok {
			return nil
		}
		return &jsonValue{kind: "string", start: start, end: p.i}
	case ch == '$' && p.i+2 < len(p.r) && string(p.r[p.i:p.i+3]) == "${[":
		for p.i+1 < len(p.r) && string(p.r[p.i:p.i+2]) != "]}" {
			p.i++
		}
		p.i = min(p.i+2, len(p.r))
		return &jsonValue{kind: "tag", start: start, end: p.i}
	default:
		for p.i < len(p.r) && isJSONWord(p.r[p.i]) {
			p.i++
		}
		word := string(p.r[start:p.i])
		switch word {
		case "true", "false", "null":
			return &jsonValue{kind: word, start: start, end: p.i}
		case "":
			return nil
		}
		return &jsonValue{kind: "number", start: start, end: p.i}
	}
}

func (p *jsonParser) string() (string, bool) {
	p.i++
	var b strings.Builder
	for p.i < len(p.r) && p.r[p.i] != '"' {
		if p.r[p.i] == '\\' && p.i+1 < len(p.r) {
			p.i++
		}
		b.WriteRune(p.r[p.i])
		p.i++
	}
	if p.i >= len(p.r) {
		return "", false
	}
	p.i++
	return b.String(), true
}

type jsonProblem struct {
	start, end int
	message    string
}

func checkJSONMessage(root *engine.GRPCMessage, source string) []jsonProblem {
	p := &jsonParser{r: []rune(source)}
	n := p.value(0)
	if n == nil {
		return nil
	}
	var problems []jsonProblem
	var object func(m *engine.GRPCMessage, n *jsonValue)
	var value func(f *engine.GRPCField, n *jsonValue, item bool)
	object = func(m *engine.GRPCMessage, n *jsonValue) {
		if n.kind != "object" {
			if n.kind != "null" && n.kind != "tag" {
				problems = append(problems, jsonProblem{n.start, n.end, locales.ExpectedObject()})
			}
			return
		}
		for _, member := range n.members {
			field := m.Field(member.key)
			if field == nil {
				problems = append(problems, jsonProblem{member.keyStart, member.keyEnd, locales.PropertyNameIsNotExpected(strconv.Quote(member.key))})
				continue
			}
			value(field, member.value, false)
		}
	}
	value = func(f *engine.GRPCField, n *jsonValue, item bool) {
		if n.kind == "null" || n.kind == "tag" {
			return
		}
		if f.Repeated && !item {
			if n.kind != "array" {
				problems = append(problems, jsonProblem{n.start, n.end, locales.ExpectedArray()})
				return
			}
			for _, it := range n.items {
				value(f, it, true)
			}
			return
		}
		if f.Map && !item {
			if n.kind != "object" {
				problems = append(problems, jsonProblem{n.start, n.end, locales.ExpectedObject()})
				return
			}
			for _, member := range n.members {
				value(f, member.value, true)
			}
			return
		}
		ok := true
		switch f.Kind {
		case "string", "bytes":
			ok = n.kind == "string"
		case "number":
			ok = n.kind == "number" || n.kind == "string"
		case "boolean":
			ok = n.kind == "true" || n.kind == "false"
		case "enum":
			ok = n.kind == "number" || n.kind == "string" && slices.Contains(f.Enum, strings.Trim(string(p.r[n.start:n.end]), `"`))
			if !ok && n.kind == "string" {
				problems = append(problems, jsonProblem{n.start, n.end, locales.ExpectedOneOfValues(strings.Join(f.Enum, ", "))})
				return
			}
		case "object":
			if f.Message != nil {
				object(f.Message, n)
			}
			return
		}
		if !ok {
			problems = append(problems, jsonProblem{n.start, n.end, locales.ExpectedKind(f.Kind)})
		}
	}
	object(root, n)
	return problems
}
