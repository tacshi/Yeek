package desktop

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"yeek/internal/engine"
)

// A gRPC message is completed and checked against its method's input, as
// Yaak's codemirror-json-schema does: property names in an object, and the
// values of enums and booleans.

// jsonFrame is an object or array open at the caret: the entry it holds
// in its parent (a key, or "[]" for an array's item), and for an object
// the key last read and whether a key or a value comes next.
type jsonFrame struct {
	object    bool
	entry     string
	key       string
	keys      []string
	expectKey bool
	afterKey  bool // a key was read; its colon and value come next
}

// jsonCaret is where the caret is in a JSON text.
type jsonCaret struct {
	frames []jsonFrame
	// key is set when a property name is typed at the caret, which then
	// replaces from up to to, quoted when quoted.
	key, value, quoted bool
	from, to           int
	prefix             string
}

func isJSONWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.+-$", r)
}

// scanJSONCaret reads the text up to the caret.
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
				// The caret is in this string.
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

// jsonPlace is what the innermost container at the caret is: an object
// of a message (message), an array of a repeated field's items, or an
// object of a map's values (field).
type jsonPlace struct {
	message *engine.GRPCMessage
	field   *engine.GRPCField
}

// jsonContainer follows the containers open at the caret from the root
// message; ok is false where the schema does not reach.
func jsonContainer(root *engine.GRPCMessage, frames []jsonFrame) (jsonPlace, bool) {
	place := jsonPlace{message: root}
	for _, f := range frames[1:] {
		var field *engine.GRPCField
		item := place.message == nil // in an array, or a map's object
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

// jsonSchemaOptions are the property names or values that fit at the caret.
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
			return nil // a map's keys are the user's
		}
		// A colon follows the name unless one is there already.
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
	// A value: of the object's field, or an item of the array or map.
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

// fieldType names a field's type, as the completion shows it.
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

// jsonValue is a value of a JSON text, where it is in runes.
type jsonValue struct {
	kind       string // object, array, string, number, true, false, null, tag
	start, end int
	members    []jsonMember
	items      []*jsonValue
}

type jsonMember struct {
	key              string
	keyStart, keyEnd int
	value            *jsonValue
}

// jsonParser reads what it can of a JSON text, leaving syntax errors to
// the editor.
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
		// A template tag stands for any value.
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

// jsonProblem is where a message does not fit its schema.
type jsonProblem struct {
	start, end int
	message    string
}

// checkJSONMessage lists the fields a message does not have, and the
// values of the wrong kind.
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
				problems = append(problems, jsonProblem{n.start, n.end, "Expected object"})
			}
			return
		}
		for _, member := range n.members {
			field := m.Field(member.key)
			if field == nil {
				problems = append(problems, jsonProblem{member.keyStart, member.keyEnd, fmt.Sprintf("Property %q is not expected", member.key)})
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
				problems = append(problems, jsonProblem{n.start, n.end, "Expected array"})
				return
			}
			for _, it := range n.items {
				value(f, it, true)
			}
			return
		}
		if f.Map && !item {
			if n.kind != "object" {
				problems = append(problems, jsonProblem{n.start, n.end, "Expected object"})
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
				problems = append(problems, jsonProblem{n.start, n.end, "Expected one of " + strings.Join(f.Enum, ", ")})
				return
			}
		case "object":
			if f.Message != nil {
				object(f.Message, n)
			}
			return
		}
		if !ok {
			problems = append(problems, jsonProblem{n.start, n.end, "Expected " + f.Kind})
		}
	}
	object(root, n)
	return problems
}
