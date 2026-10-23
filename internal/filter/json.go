package filter

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const maxJSONDepth = 128

type jsonKind uint8

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

type jsonNode struct {
	kind   jsonKind
	b      bool
	n      float64
	s      string
	items  []*jsonNode
	keys   []string
	values map[string]*jsonNode
}

func parseJSON(text string) (*jsonNode, error) {
	p := &jsonParser{text: text}
	p.skipWS()
	value, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipWS()
	if p.pos < len(p.text) {
		return nil, p.error("trailing characters")
	}
	return value, nil
}

type jsonParser struct {
	text string
	pos  int
}

func (p *jsonParser) error(message string) error {
	end := min(p.pos, len(p.text))
	for end < len(p.text) && end > 0 && !utf8.RuneStart(p.text[end]) {
		end--
	}
	before := p.text[:end]
	line := strings.Count(before, "\n") + 1
	column := utf8.RuneCountInString(before[strings.LastIndexByte(before, '\n')+1:]) + 1
	return fmt.Errorf("%s at line %d column %d", message, line, column)
}

func (p *jsonParser) peek() byte {
	if p.pos < len(p.text) {
		return p.text[p.pos]
	}
	return 0
}

func (p *jsonParser) skipWS() {
	for p.pos < len(p.text) {
		switch p.text[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) word(word string, value *jsonNode) (*jsonNode, error) {
	if strings.HasPrefix(p.text[p.pos:], word) {
		p.pos += len(word)
		return value, nil
	}
	return nil, p.error("expected value")
}

func (p *jsonParser) value(depth int) (*jsonNode, error) {
	if depth > maxJSONDepth {
		return nil, p.error("recursion limit exceeded")
	}
	if p.pos >= len(p.text) {
		return nil, p.error("EOF while parsing a value")
	}
	switch c := p.peek(); {
	case c == 'n':
		return p.word("null", &jsonNode{kind: jsonNull})
	case c == 't':
		return p.word("true", &jsonNode{kind: jsonBool, b: true})
	case c == 'f':
		return p.word("false", &jsonNode{kind: jsonBool})
	case c == '"':
		s, err := p.string()
		if err != nil {
			return nil, err
		}
		return &jsonNode{kind: jsonString, s: s}, nil
	case c == '[':
		return p.array(depth)
	case c == '{':
		return p.object(depth)
	case c == '-' || c >= '0' && c <= '9':
		return p.number()
	}
	return nil, p.error("expected value")
}

func (p *jsonParser) array(depth int) (*jsonNode, error) {
	p.pos++
	node := &jsonNode{kind: jsonArray, items: []*jsonNode{}}
	p.skipWS()
	if p.peek() == ']' {
		p.pos++
		return node, nil
	}
	for {
		p.skipWS()
		item, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		node.items = append(node.items, item)
		p.skipWS()
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return node, nil
		default:
			return nil, p.error("expected `,` or `]`")
		}
	}
}

func (p *jsonParser) object(depth int) (*jsonNode, error) {
	p.pos++
	node := &jsonNode{kind: jsonObject, values: map[string]*jsonNode{}}
	p.skipWS()
	if p.peek() == '}' {
		p.pos++
		return node, nil
	}
	for {
		p.skipWS()
		if p.peek() != '"' {
			return nil, p.error("key must be a string")
		}
		key, err := p.string()
		if err != nil {
			return nil, err
		}
		p.skipWS()
		if p.peek() != ':' {
			return nil, p.error("expected `:`")
		}
		p.pos++
		p.skipWS()
		value, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, exists := node.values[key]; !exists {
			node.keys = append(node.keys, key)
		}
		node.values[key] = value
		p.skipWS()
		switch p.peek() {
		case ',':
			p.pos++
		case '}':
			p.pos++
			jsKeyOrder(node)
			return node, nil
		default:
			return nil, p.error("expected `,` or `}`")
		}
	}
}

func (p *jsonParser) number() (*jsonNode, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	switch c := p.peek(); {
	case c == '0':
		p.pos++
	case c >= '1' && c <= '9':
		p.digits()
	default:
		return nil, p.error("invalid number")
	}
	if p.peek() == '.' {
		p.pos++
		if c := p.peek(); c < '0' || c > '9' {
			return nil, p.error("invalid number")
		}
		p.digits()
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.pos++
		if c := p.peek(); c == '+' || c == '-' {
			p.pos++
		}
		if c := p.peek(); c < '0' || c > '9' {
			return nil, p.error("invalid number")
		}
		p.digits()
	}
	return &jsonNode{kind: jsonNumber, n: parseFloat(p.text[start:p.pos])}, nil
}

func parseFloat(text string) float64 {
	n, err := strconv.ParseFloat(text, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return n
}

func (p *jsonParser) digits() {
	for c := p.peek(); c >= '0' && c <= '9'; c = p.peek() {
		p.pos++
	}
}

func (p *jsonParser) hex4() (uint16, error) {
	if p.pos+4 > len(p.text) {
		return 0, p.error("invalid escape")
	}
	unit, err := strconv.ParseUint(p.text[p.pos:p.pos+4], 16, 16)
	if err != nil {
		return 0, p.error("invalid escape")
	}
	p.pos += 4
	return uint16(unit), nil
}

func (p *jsonParser) string() (string, error) {
	p.pos++
	var out strings.Builder
	for {
		run := p.pos
		for p.pos < len(p.text) {
			if b := p.text[p.pos]; b == '"' || b == '\\' || b < 0x20 {
				break
			}
			p.pos++
		}
		out.WriteString(p.text[run:p.pos])
		if p.pos >= len(p.text) {
			return "", p.error("EOF while parsing a string")
		}
		switch p.text[p.pos] {
		case '"':
			p.pos++
			return out.String(), nil
		case '\\':
			p.pos++
			if p.pos >= len(p.text) {
				return "", p.error("EOF while parsing a string")
			}
			escape := p.text[p.pos]
			p.pos++
			switch escape {
			case '"', '\\', '/':
				out.WriteByte(escape)
			case 'b':
				out.WriteByte('\b')
			case 'f':
				out.WriteByte('\f')
			case 'n':
				out.WriteByte('\n')
			case 'r':
				out.WriteByte('\r')
			case 't':
				out.WriteByte('\t')
			case 'u':
				unit, err := p.hex4()
				if err != nil {
					return "", err
				}
				units := []uint16{unit}
				if unit >= 0xD800 && unit < 0xDC00 && strings.HasPrefix(p.text[p.pos:], `\u`) {
					save := p.pos
					p.pos += 2
					low, err := p.hex4()
					if err != nil {
						return "", err
					}
					if low >= 0xDC00 && low < 0xE000 {
						units = append(units, low)
					} else {
						p.pos = save
					}
				}
				out.WriteString(fromUTF16(units))
			default:
				return "", p.error("invalid escape")
			}
		default:
			return "", p.error("control character in string")
		}
	}
}

func jsKeyOrder(node *jsonNode) {
	if !slices.ContainsFunc(node.keys, func(k string) bool { _, ok := arrayIndex(k); return ok }) {
		return
	}
	var indexes, rest []string
	for _, k := range node.keys {
		if _, ok := arrayIndex(k); ok {
			indexes = append(indexes, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.SortStableFunc(indexes, func(a, b string) int {
		x, _ := arrayIndex(a)
		y, _ := arrayIndex(b)
		return int(int64(x) - int64(y))
	})
	node.keys = append(indexes, rest...)
}

func arrayIndex(key string) (uint32, bool) {
	if key == "" || len(key) > 1 && key[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 32)
	if err != nil || n == math.MaxUint32 {
		return 0, false
	}
	return uint32(n), true
}

func fromUTF16(units []uint16) string { return string(utf16.Decode(units)) }

func stringifyResults(values []jsValue) string {
	return stringify(jsArrayValue(values), "  ")
}

func stringify(v jsValue, indent string) string {
	var out strings.Builder
	w := jsonWriter{out: &out, indent: indent}
	if !w.js(v, 0) {
		return ""
	}
	return out.String()
}

type jsonWriter struct {
	out    *strings.Builder
	indent string
}

func (w jsonWriter) line(depth int) {
	if w.indent == "" {
		return
	}
	w.out.WriteByte('\n')
	for range depth {
		w.out.WriteString(w.indent)
	}
}

func (w jsonWriter) values(items []jsValue, depth int) {
	if len(items) == 0 {
		w.out.WriteString("[]")
		return
	}
	w.out.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			w.out.WriteByte(',')
		}
		w.line(depth + 1)
		if !w.js(item, depth+1) {
			w.out.WriteString("null")
		}
	}
	w.line(depth)
	w.out.WriteByte(']')
}

func (w jsonWriter) js(v jsValue, depth int) bool {
	switch v.kind {
	case jsUndefined, jsFunction:
		return false
	case jsNull:
		w.out.WriteString("null")
	case jsBool:
		w.out.WriteString(strconv.FormatBool(v.b))
	case jsNumber:
		writeNumber(w.out, v.n)
	case jsString:
		writeString(w.out, v.s)
	case jsDoc:
		w.json(v.doc, depth)
	case jsArrayKind:
		w.values(v.arr.items, depth)
	case jsRegexKind:
		w.out.WriteString("{}")
	}
	return true
}

func (w jsonWriter) json(node *jsonNode, depth int) {
	switch node.kind {
	case jsonNull:
		w.out.WriteString("null")
	case jsonBool:
		w.out.WriteString(strconv.FormatBool(node.b))
	case jsonNumber:
		writeNumber(w.out, node.n)
	case jsonString:
		writeString(w.out, node.s)
	case jsonArray:
		if len(node.items) == 0 {
			w.out.WriteString("[]")
			return
		}
		w.out.WriteByte('[')
		for i, item := range node.items {
			if i > 0 {
				w.out.WriteByte(',')
			}
			w.line(depth + 1)
			w.json(item, depth+1)
		}
		w.line(depth)
		w.out.WriteByte(']')
	case jsonObject:
		if len(node.keys) == 0 {
			w.out.WriteString("{}")
			return
		}
		w.out.WriteByte('{')
		for i, key := range node.keys {
			if i > 0 {
				w.out.WriteByte(',')
			}
			w.line(depth + 1)
			writeString(w.out, key)
			w.out.WriteByte(':')
			if w.indent != "" {
				w.out.WriteByte(' ')
			}
			w.json(node.values[key], depth+1)
		}
		w.line(depth)
		w.out.WriteByte('}')
	}
}

func writeNumber(out *strings.Builder, n float64) {
	if math.IsInf(n, 0) || math.IsNaN(n) {
		out.WriteString("null")
		return
	}
	out.WriteString(numberToString(n))
}

func writeString(out *strings.Builder, s string) {
	out.WriteByte('"')
	for _, c := range s {
		switch {
		case c == '"':
			out.WriteString(`\"`)
		case c == '\\':
			out.WriteString(`\\`)
		case c == '\b':
			out.WriteString(`\b`)
		case c == '\f':
			out.WriteString(`\f`)
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\t':
			out.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(out, `\u%04x`, c)
		default:
			out.WriteRune(c)
		}
	}
	out.WriteByte('"')
}

func numberToString(n float64) string {
	switch {
	case math.IsNaN(n):
		return "NaN"
	case n == 0:
		return "0"
	case math.IsInf(n, 1):
		return "Infinity"
	case math.IsInf(n, -1):
		return "-Infinity"
	}
	sign := ""
	if n < 0 {
		sign = "-"
	}
	sci := strconv.FormatFloat(math.Abs(n), 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(sci, "e")
	digits := strings.ReplaceAll(mantissa, ".", "")
	k := len(digits)
	e, _ := strconv.Atoi(exponent)
	nExp := e + 1
	var body string
	switch {
	case k <= nExp && nExp <= 21:
		body = digits + strings.Repeat("0", nExp-k)
	case 0 < nExp && nExp <= 21:
		body = digits[:nExp] + "." + digits[nExp:]
	case -6 < nExp && nExp <= 0:
		body = "0." + strings.Repeat("0", -nExp) + digits
	default:
		e := nExp - 1
		eSign := "+"
		if e < 0 {
			eSign, e = "-", -e
		}
		rest := ""
		if k > 1 {
			rest = "." + digits[1:]
		}
		body = digits[:1] + rest + "e" + eSign + strconv.Itoa(e)
	}
	return sign + body
}
