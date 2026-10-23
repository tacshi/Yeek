package filter

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxRFCDepth = 100

type query struct {
	root     bool
	path     string
	singular bool
}

type rfcFunction uint8

const (
	rfcLength rfcFunction = iota
	rfcCount
	rfcMatch
	rfcSearch
	rfcValue
)

var rfcFunctionNames = []string{"length", "count", "match", "search", "value"}

func rfcFunctionNamed(name string) (rfcFunction, bool) {
	for i, n := range rfcFunctionNames {
		if n == name {
			return rfcFunction(i), true // #nosec G115 -- one of five functions.
		}
	}
	return 0, false
}

func (f rfcFunction) name() string     { return rfcFunctionNames[f] }
func (f rfcFunction) takesNodes() bool { return f == rfcCount || f == rfcValue }

func (f rfcFunction) arity() int {
	if f == rfcMatch || f == rfcSearch {
		return 2
	}
	return 1
}

func (f rfcFunction) returnsLogical() bool { return f == rfcMatch || f == rfcSearch }

type optional struct {
	value jsValue
	ok    bool
}

func some(v jsValue) optional { return optional{v, true} }

func (f rfcFunction) call(args []optional, nodes []jsValue, e *engine) optional {
	argument := func(i int) optional {
		if i < len(args) {
			return args[i]
		}
		return optional{}
	}
	switch f {
	case rfcLength:
		a := argument(0)
		if !a.ok {
			return optional{}
		}
		switch v := a.value; {
		case v.kind == jsString:
			return some(jsNumberValue(float64(utf8.RuneCountInString(v.s))))
		case v.isDocArray():
			return some(jsNumberValue(float64(len(v.doc.items))))
		case v.isDocObject():
			return some(jsNumberValue(float64(len(v.doc.keys))))
		case v.kind == jsArrayKind:
			return some(jsNumberValue(float64(len(v.arr.items))))
		}
		return optional{}
	case rfcCount:
		return some(jsNumberValue(float64(len(nodes))))
	case rfcValue:
		if len(nodes) == 1 {
			return some(nodes[0])
		}
		return optional{}
	}
	text, pattern := argument(0), argument(1)
	matched := false
	if text.ok && pattern.ok && text.value.kind == jsString && pattern.value.kind == jsString {
		if re := e.iRegexp(pattern.value.s, f == rfcMatch); re != nil {
			matched, _ = re.isMatch(text.value.s)
		}
	}
	return some(jsBoolValue(matched))
}

func (f rfcFunction) callJS(args [][]jsValue, e *engine) jsValue {
	var nodes []jsValue
	var values []optional
	if f.takesNodes() {
		if len(args) > 0 {
			nodes = args[0]
		}
	} else {
		for _, a := range args {
			if len(a) > 0 && a[0].kind != jsUndefined {
				values = append(values, some(a[0]))
			} else {
				values = append(values, optional{})
			}
		}
	}
	if r := f.call(values, nodes, e); r.ok {
		return r.value
	}
	return undefined
}

func isNameFirst(c rune) bool { return isASCIILetter(c) || c == '_' || c >= 0x80 }
func isNameChar(c rune) bool  { return isNameFirst(c) || isDigit(c) }
func isBlank(c rune) bool     { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func closingBracket(chars []rune, open int) (int, bool) {
	depth := 0
	for i := open; i < len(chars); i++ {
		switch c := chars[i]; c {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case '\'', '"':
			i++
			for i < len(chars) && chars[i] != c {
				if chars[i] == '\\' {
					i++
				}
				i++
			}
		}
	}
	return 0, false
}

func scanQuery(chars []rune, start int, root bool) (int, query) {
	at := func(i int) rune {
		if i < len(chars) {
			return chars[i]
		}
		return 0
	}
	end, singular := start, true
	for {
		i := end
		for isBlank(at(i)) && i < len(chars) {
			i++
		}
		switch {
		case at(i) == '.' && at(i+1) == '.':
			i += 2
			singular = false
			switch c := at(i); {
			case c == '*':
				i++
			case c == '[':
				close, ok := closingBracket(chars, i)
				if !ok {
					return finishQuery(chars, start, end, root, singular)
				}
				i = close
			case i < len(chars) && isNameFirst(c):
				i = nameEnd(chars, i)
			default:
				return finishQuery(chars, start, end, root, singular)
			}
		case at(i) == '.':
			i++
			switch c := at(i); {
			case c == '*':
				singular = false
				i++
			case i < len(chars) && isNameFirst(c):
				i = nameEnd(chars, i)
			default:
				return finishQuery(chars, start, end, root, singular)
			}
		case at(i) == '[':
			close, ok := closingBracket(chars, i)
			if !ok {
				return finishQuery(chars, start, end, root, singular)
			}
			selector := strings.TrimFunc(string(chars[i+1:close-1]), isBlank)
			singular = singular && isSingularSelector(selector)
			i = close
		default:
			return finishQuery(chars, start, end, root, singular)
		}
		end = i
	}
}

func finishQuery(chars []rune, start, end int, root, singular bool) (int, query) {
	return end, query{root: root, path: "$" + string(chars[start:end]), singular: singular}
}

func nameEnd(chars []rune, i int) int {
	for i < len(chars) && isNameChar(chars[i]) {
		i++
	}
	return i
}

func isSingularSelector(selector string) bool {
	digits := strings.TrimPrefix(selector, "-")
	index := digits != "" && strings.Trim(digits, "0123456789") == ""
	chars := []rune(selector)
	_, end, ok := quotedString(chars, 0)
	return index || ok && end == len(chars)
}

type logicalKind uint8

const (
	logicalOr logicalKind = iota
	logicalAnd
	logicalNot
	logicalCompare
	logicalExists
	logicalTest
)

type logical struct {
	kind        logicalKind
	items       []*logical
	left, right *comparable
	op          string
	query       query
	call        *rfcCall
}

type comparableKind uint8

const (
	comparableLiteral comparableKind = iota
	comparableQuery
	comparableCall
)

type comparable struct {
	kind    comparableKind
	literal jsValue
	query   query
	call    *rfcCall
}

type rfcCall struct {
	function rfcFunction
	args     []rfcArg
}

type rfcArg struct {
	value *comparable
	nodes *query
}

func parseRFC(text string) (*logical, error) {
	p := &rfcParser{chars: []rune(text)}
	p.blank()
	l, err := p.or()
	if err != nil {
		return nil, err
	}
	p.blank()
	if p.index < len(p.chars) {
		return nil, p.error("unexpected")
	}
	return l, nil
}

type rfcParser struct {
	chars        []rune
	index, depth int
}

func (p *rfcParser) peek() rune {
	if p.index < len(p.chars) {
		return p.chars[p.index]
	}
	return 0
}

func (p *rfcParser) eat(s string) bool {
	r := []rune(s)
	if p.index+len(r) > len(p.chars) || string(p.chars[p.index:p.index+len(r)]) != s {
		return false
	}
	p.index += len(r)
	return true
}

func (p *rfcParser) blank() {
	for p.index < len(p.chars) && isBlank(p.chars[p.index]) {
		p.index++
	}
}

func (p *rfcParser) error(message string) error {
	rest := string(p.chars[min(p.index, len(p.chars)):])
	if rest == "" {
		return fmt.Errorf("%s end of filter expression", message)
	}
	return fmt.Errorf("%s \"%s\" in filter expression", message, rest)
}

func (p *rfcParser) or() (*logical, error) {
	p.depth++
	if p.depth > maxRFCDepth {
		return nil, errors.New("filter expression is nested too deeply")
	}
	first, err := p.and()
	if err != nil {
		return nil, err
	}
	items := []*logical{first}
	for {
		p.blank()
		if !p.eat("||") {
			break
		}
		p.blank()
		item, err := p.and()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	p.depth--
	if len(items) == 1 {
		return items[0], nil
	}
	return &logical{kind: logicalOr, items: items}, nil
}

func (p *rfcParser) and() (*logical, error) {
	first, err := p.basic()
	if err != nil {
		return nil, err
	}
	items := []*logical{first}
	for {
		p.blank()
		if !p.eat("&&") {
			break
		}
		p.blank()
		item, err := p.basic()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) == 1 {
		return items[0], nil
	}
	return &logical{kind: logicalAnd, items: items}, nil
}

func (p *rfcParser) basic() (*logical, error) {
	if p.peek() == '!' && runeAt(p.chars, p.index+1) != '=' {
		p.index++
		p.blank()
		var inner *logical
		var err error
		if p.peek() == '(' {
			inner, err = p.paren()
		} else {
			inner, err = p.test()
		}
		if err != nil {
			return nil, err
		}
		return &logical{kind: logicalNot, items: []*logical{inner}}, nil
	}
	if p.peek() == '(' {
		return p.paren()
	}
	left, err := p.comparable()
	if err != nil {
		return nil, err
	}
	save := p.index
	p.blank()
	op := ""
	for _, candidate := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if p.eat(candidate) {
			op = candidate
			break
		}
	}
	if op == "" {
		p.index = save
		return p.asTest(left)
	}
	p.blank()
	right, err := p.comparable()
	if err != nil {
		return nil, err
	}
	for _, side := range []*comparable{left, right} {
		if err := checkComparable(side); err != nil {
			return nil, err
		}
	}
	return &logical{kind: logicalCompare, left: left, op: op, right: right}, nil
}

func (p *rfcParser) paren() (*logical, error) {
	p.index++
	p.blank()
	inner, err := p.or()
	if err != nil {
		return nil, err
	}
	p.blank()
	if !p.eat(")") {
		return nil, p.error("expected `)` before")
	}
	return inner, nil
}

func (p *rfcParser) test() (*logical, error) {
	c, err := p.comparable()
	if err != nil {
		return nil, err
	}
	return p.asTest(c)
}

func (p *rfcParser) asTest(c *comparable) (*logical, error) {
	switch c.kind {
	case comparableQuery:
		return &logical{kind: logicalExists, query: c.query}, nil
	case comparableCall:
		if c.call.function.returnsLogical() {
			return &logical{kind: logicalTest, call: c.call}, nil
		}
		return nil, fmt.Errorf("%s() gives a value; compare it rather than test it", c.call.function.name())
	}
	return nil, p.error("a literal is not a test; expected a comparison before")
}

func (p *rfcParser) comparable() (*comparable, error) {
	switch c := p.peek(); {
	case p.index < len(p.chars) && (c == '@' || c == '$'):
		end, q := scanQuery(p.chars, p.index+1, c == '$')
		p.index = end
		return &comparable{kind: comparableQuery, query: q}, nil
	case c == '\'' || c == '"':
		s, err := p.string()
		if err != nil {
			return nil, err
		}
		return &comparable{kind: comparableLiteral, literal: jsStringValue(s)}, nil
	case c == '-' || isDigit(c):
		n, err := p.number()
		if err != nil {
			return nil, err
		}
		return &comparable{kind: comparableLiteral, literal: jsNumberValue(n)}, nil
	case c >= 'a' && c <= 'z':
		start := p.index
		for c := p.peek(); c >= 'a' && c <= 'z' || isDigit(c) || c == '_'; c = p.peek() {
			p.index++
		}
		name := string(p.chars[start:p.index])
		if p.peek() == '(' {
			call, err := p.call(name)
			if err != nil {
				return nil, err
			}
			return &comparable{kind: comparableCall, call: call}, nil
		}
		switch name {
		case "true":
			return &comparable{kind: comparableLiteral, literal: jsBoolValue(true)}, nil
		case "false":
			return &comparable{kind: comparableLiteral, literal: jsBoolValue(false)}, nil
		case "null":
			return &comparable{kind: comparableLiteral, literal: null}, nil
		}
		p.index = start
		return nil, p.error("unexpected")
	}
	return nil, p.error("expected a query, literal or function at")
}

func (p *rfcParser) call(name string) (*rfcCall, error) {
	function, ok := rfcFunctionNamed(name)
	if !ok {
		return nil, fmt.Errorf("unknown function %s()", name)
	}
	p.depth++
	if p.depth > maxRFCDepth {
		return nil, errors.New("filter expression is nested too deeply")
	}
	p.index++
	var args []rfcArg
	for {
		p.blank()
		if len(args) == 0 && p.eat(")") {
			break
		}
		a, err := p.comparable()
		if err != nil {
			return nil, err
		}
		if function.takesNodes() {
			if a.kind != comparableQuery {
				return nil, fmt.Errorf("%s() takes a query", function.name())
			}
			args = append(args, rfcArg{nodes: &a.query})
		} else {
			if err := checkComparable(a); err != nil {
				return nil, err
			}
			args = append(args, rfcArg{value: a})
		}
		p.blank()
		if p.eat(")") {
			break
		}
		if !p.eat(",") {
			return nil, p.error("expected `,` or `)` before")
		}
	}
	if len(args) != function.arity() {
		return nil, fmt.Errorf("%s() takes %d argument(s)", function.name(), function.arity())
	}
	p.depth--
	return &rfcCall{function: function, args: args}, nil
}

func (p *rfcParser) number() (float64, error) {
	start := p.index
	p.eat("-")
	digits := func() int {
		from := p.index
		for isDigit(p.peek()) && p.index < len(p.chars) {
			p.index++
		}
		return p.index - from
	}
	intStart := p.index
	if digits() == 0 || p.chars[intStart] == '0' && p.index-intStart > 1 {
		return 0, p.error("invalid number at")
	}
	if p.eat(".") && digits() == 0 {
		return 0, p.error("invalid number at")
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.index++
		if !p.eat("+") {
			p.eat("-")
		}
		if digits() == 0 {
			return 0, p.error("invalid number at")
		}
	}
	return parseFloat(string(p.chars[start:p.index])), nil
}

func (p *rfcParser) string() (string, error) {
	value, end, ok := quotedString(p.chars, p.index)
	if !ok {
		return "", p.error("invalid string literal at")
	}
	p.index = end
	return value, nil
}

func quotedString(chars []rune, start int) (string, int, bool) {
	if start >= len(chars) || chars[start] != '\'' && chars[start] != '"' {
		return "", 0, false
	}
	quote := chars[start]
	var out strings.Builder
	i := start + 1
	for {
		if i >= len(chars) {
			return "", 0, false
		}
		c := chars[i]
		i++
		if c == quote {
			return out.String(), i, true
		}
		if c != '\\' {
			out.WriteRune(c)
			continue
		}
		if i >= len(chars) {
			return "", 0, false
		}
		escaped := chars[i]
		i++
		switch escaped {
		case '"', '\'', '\\', '/':
			out.WriteRune(escaped)
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
			if !hexRun(chars, i, 4) {
				return "", 0, false
			}
			units := []uint16{uint16(hexValue(chars[i : i+4]))} // #nosec G115 -- four hex digits.
			i += 4
			paired := units[0] >= 0xD800 && units[0] < 0xDC00 && i+1 < len(chars) && chars[i] == '\\' && chars[i+1] == 'u'
			if paired && hexRun(chars, i+2, 4) {
				if low := hexValue(chars[i+2 : i+6]); low >= 0xDC00 && low < 0xE000 {
					units = append(units, uint16(low)) // #nosec G115 -- four hex digits.
					i += 6
				}
			}
			out.WriteString(fromUTF16(units))
		default:
			return "", 0, false
		}
	}
}

func checkComparable(c *comparable) error {
	switch {
	case c.kind == comparableQuery && !c.query.singular:
		prefix := "@"
		if c.query.root {
			prefix = "$"
		}
		return fmt.Errorf("%s%s can select several nodes, so it cannot be compared", prefix, c.query.path[1:])
	case c.kind == comparableCall && c.call.function.returnsLogical():
		return fmt.Errorf("%s() gives a test result, not a value to compare", c.call.function.name())
	}
	return nil
}

func (l *logical) test(current jsValue, e *engine) (bool, error) {
	switch l.kind {
	case logicalOr:
		for _, item := range l.items {
			if ok, err := item.test(current, e); err != nil || ok {
				return ok, err
			}
		}
		return false, nil
	case logicalAnd:
		for _, item := range l.items {
			if ok, err := item.test(current, e); err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	case logicalNot:
		ok, err := l.items[0].test(current, e)
		return !ok && err == nil, err
	case logicalExists:
		nodes, err := e.query(l.query, current)
		return len(nodes) > 0, err
	case logicalTest:
		r, err := l.call.eval(current, e)
		return r.ok && r.value.truthy(), err
	}
	left, err := l.left.eval(current, e)
	if err != nil {
		return false, err
	}
	right, err := l.right.eval(current, e)
	if err != nil {
		return false, err
	}
	switch l.op {
	case "==":
		return rfcEqual(left, right), nil
	case "!=":
		return !rfcEqual(left, right), nil
	case "<":
		return rfcLess(left, right), nil
	case "<=":
		return rfcLess(left, right) || rfcEqual(left, right), nil
	case ">":
		return rfcLess(right, left), nil
	}
	return rfcLess(right, left) || rfcEqual(left, right), nil
}

func (c *comparable) eval(current jsValue, e *engine) (optional, error) {
	switch c.kind {
	case comparableLiteral:
		return some(c.literal), nil
	case comparableQuery:
		nodes, err := e.query(c.query, current)
		if err != nil || len(nodes) != 1 {
			return optional{}, err
		}
		return some(nodes[0]), nil
	}
	return c.call.eval(current, e)
}

func (c *rfcCall) eval(current jsValue, e *engine) (optional, error) {
	var values []optional
	var nodes []jsValue
	for _, a := range c.args {
		if a.value != nil {
			v, err := a.value.eval(current, e)
			if err != nil {
				return optional{}, err
			}
			values = append(values, v)
		} else {
			n, err := e.query(*a.nodes, current)
			if err != nil {
				return optional{}, err
			}
			nodes = n
		}
	}
	return c.function.call(values, nodes, e), nil
}

func rfcEqual(a, b optional) bool {
	switch {
	case !a.ok && !b.ok:
		return true
	case a.ok && b.ok:
		return deepEqual(a.value, b.value)
	}
	return false
}

func deepEqual(a, b jsValue) bool {
	switch {
	case a.kind == jsDoc && b.kind == jsDoc:
		return jsonEqual(a.doc, b.doc)
	case a.kind == jsNull && b.kind == jsNull:
		return true
	case a.kind == jsBool && b.kind == jsBool:
		return a.b == b.b
	case a.kind == jsNumber && b.kind == jsNumber:
		return a.n == b.n
	case a.kind == jsString && b.kind == jsString:
		return a.s == b.s
	}
	return false
}

func jsonEqual(a, b *jsonNode) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case jsonArray:
		if len(a.items) != len(b.items) {
			return false
		}
		for i := range a.items {
			if !jsonEqual(a.items[i], b.items[i]) {
				return false
			}
		}
		return true
	case jsonObject:
		if len(a.keys) != len(b.keys) {
			return false
		}
		for _, k := range a.keys {
			w, ok := b.values[k]
			if !ok || !jsonEqual(a.values[k], w) {
				return false
			}
		}
		return true
	case jsonNull:
		return true
	case jsonBool:
		return a.b == b.b
	case jsonNumber:
		return a.n == b.n
	}
	return a.s == b.s
}

func rfcLess(a, b optional) bool {
	if !a.ok || !b.ok {
		return false
	}
	switch {
	case a.value.kind == jsNumber && b.value.kind == jsNumber:
		return a.value.n < b.value.n
	case a.value.kind == jsString && b.value.kind == jsString:
		return a.value.s < b.value.s
	}
	return false
}
