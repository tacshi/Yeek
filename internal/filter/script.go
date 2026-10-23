package filter

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// jsonpath-plus's safe evaluator: jsep's grammar (with its regex plugin,
// typeof, void, undefined) and Safe-Script's semantics, minus what it would
// let a filter do beyond reading the document. Yaak's
// crates/yaak-filter/src/jsonpath/script.rs.

const maxScriptDepth = 100

type exprKind uint8

const (
	exprLiteral exprKind = iota
	exprIdent
	exprMember
	exprCall
	exprRFC
	exprArray
	exprUnary
	exprBinary
	exprConditional
)

type expr struct {
	kind    exprKind
	literal jsValue
	name    string
	// computed is a member's computed property; name its named one.
	computed *expr
	op       string
	args     []*expr
	// rfcArgs are an RFC function's arguments: a value, or a query.
	function rfcFunction
	rfcArgs  []scriptRFCArg
}

type scriptRFCArg struct {
	value *expr
	nodes *query
}

// binaryOps are matched longest first; a nil precedence is unsupported.
var binaryOps = []struct {
	text       string
	precedence int
}{
	{">>>=", 0}, {"===", 6}, {"!==", 6}, {">>>", 8}, {"**=", 0}, {"<<=", 0}, {">>=", 0},
	{"||=", 0}, {"&&=", 0}, {"??=", 0}, {"||", 1}, {"&&", 2}, {"==", 6}, {"!=", 6}, {"<=", 7},
	{">=", 7}, {"<<", 8}, {">>", 8}, {"**", 0}, {"??", 0}, {"*=", 0}, {"/=", 0}, {"%=", 0},
	{"+=", 0}, {"-=", 0}, {"&=", 0}, {"^=", 0}, {"|=", 0}, {"|", 3}, {"^", 4}, {"&", 5},
	{"<", 7}, {">", 7}, {"+", 9}, {"-", 9}, {"*", 10}, {"/", 10}, {"%", 10}, {"=", 0},
}

func precedence(op string) int {
	for _, o := range binaryOps {
		if o.text == op {
			return o.precedence
		}
	}
	return 0
}

var unaryOps = []string{"typeof", "void", "-", "!", "~", "+"}

func parseScript(code string) (*expr, error) {
	p := &scriptParser{chars: []rune(code)}
	nodes, err := p.expressions(0)
	if err != nil {
		return nil, err
	}
	switch len(nodes) {
	case 0:
		return &expr{kind: exprLiteral, literal: undefined}, nil
	case 1:
		return nodes[0], nil
	}
	if n := nodes[0]; n.kind == exprIdent && (n.name == "var" || n.name == "let" || n.name == "const") {
		return nil, fmt.Errorf("variable declarations (%s) are not supported", n.name)
	}
	return nil, errors.New("multiple expressions (separated by `;` or `,`) are not supported")
}

func isIdentifierStart(c rune) bool { return isASCIILetter(c) || c == '$' || c == '_' || c >= 128 }
func isIdentifierPart(c rune) bool  { return isIdentifierStart(c) || isDigit(c) }

type scriptParser struct {
	chars        []rune
	index, depth int
}

func (p *scriptParser) peek() rune { return p.peekAt(0) }

func (p *scriptParser) peekAt(offset int) rune {
	if p.index+offset < len(p.chars) {
		return p.chars[p.index+offset]
	}
	return 0
}

func (p *scriptParser) atEnd() bool { return p.index >= len(p.chars) }

func (p *scriptParser) restStartsWith(s string) bool {
	r := []rune(s)
	return p.index+len(r) <= len(p.chars) && string(p.chars[p.index:p.index+len(r)]) == s
}

func (p *scriptParser) error(message string) error {
	return fmt.Errorf("%s at character %d", message, p.index)
}

func (p *scriptParser) unexpected() error {
	next := ""
	if !p.atEnd() {
		next = string(p.peek())
	}
	return p.error(`Unexpected "` + next + `"`)
}

func (p *scriptParser) spaces() {
	for !p.atEnd() && (p.peek() == ' ' || p.peek() == '\t' || p.peek() == '\n' || p.peek() == '\r') {
		p.index++
	}
}

func (p *scriptParser) enter() error {
	p.depth++
	if p.depth > maxScriptDepth {
		return errors.New("expression is nested too deeply")
	}
	return nil
}

// expressions reads expressions separated by ; or , until the end, or
// until the character until (0 for none).
func (p *scriptParser) expressions(until rune) ([]*expr, error) {
	var nodes []*expr
	for !p.atEnd() {
		c := p.peek()
		if c == ';' || c == ',' {
			p.index++
			continue
		}
		node, err := p.expression()
		if err != nil {
			return nil, err
		}
		if node != nil {
			nodes = append(nodes, node)
		} else if !p.atEnd() {
			if until != 0 && c == until {
				break
			}
			return nil, p.unexpected()
		}
	}
	return nodes, nil
}

func (p *scriptParser) expression() (*expr, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	node, err := p.binaryExpression()
	if err != nil {
		return nil, err
	}
	p.spaces()
	if node != nil && p.peek() == '?' && !p.atEnd() {
		p.index++
		consequent, err := p.expression()
		if err != nil {
			return nil, err
		}
		if consequent == nil {
			return nil, p.error("Expected expression")
		}
		p.spaces()
		if p.peek() != ':' || p.atEnd() {
			return nil, p.error("Expected :")
		}
		p.index++
		alternate, err := p.expression()
		if err != nil {
			return nil, err
		}
		if alternate == nil {
			return nil, p.error("Expected expression")
		}
		node = &expr{kind: exprConditional, args: []*expr{node, consequent, alternate}}
	}
	p.depth--
	return node, nil
}

func (p *scriptParser) binaryOp() (string, error) {
	p.spaces()
	for _, o := range binaryOps {
		if !p.restStartsWith(o.text) {
			continue
		}
		if o.precedence > 0 {
			p.index += len([]rune(o.text))
			return o.text, nil
		}
		if strings.HasSuffix(o.text, "=") {
			return "", fmt.Errorf("assignment (%s) is not supported", o.text)
		}
		return "", fmt.Errorf("the %s operator is not supported", o.text)
	}
	return "", nil
}

func (p *scriptParser) binaryExpression() (*expr, error) {
	left, err := p.token()
	if err != nil || left == nil {
		return left, err
	}
	op, err := p.binaryOp()
	if err != nil {
		return nil, err
	}
	if op == "" {
		return left, nil
	}
	right, err := p.token()
	if err != nil {
		return nil, err
	}
	if right == nil {
		return nil, p.error("Expected expression after operator")
	}
	nodes, ops := []*expr{left, right}, []string{op}
	operators := 1
	for {
		op, err := p.binaryOp()
		if err != nil {
			return nil, err
		}
		if op == "" {
			break
		}
		operators++
		if operators > maxScriptDepth {
			return nil, errors.New("expression has too many operators")
		}
		for len(ops) > 0 && precedence(op) <= precedence(ops[len(ops)-1]) {
			prev := ops[len(ops)-1]
			l, r := nodes[len(nodes)-2], nodes[len(nodes)-1]
			ops, nodes = ops[:len(ops)-1], nodes[:len(nodes)-2]
			nodes = append(nodes, &expr{kind: exprBinary, op: prev, args: []*expr{l, r}})
		}
		node, err := p.token()
		if err != nil {
			return nil, err
		}
		if node == nil {
			return nil, p.error("Expected expression after operator")
		}
		ops, nodes = append(ops, op), append(nodes, node)
	}
	node := nodes[len(nodes)-1]
	for i := len(ops) - 1; i >= 0; i-- {
		node = &expr{kind: exprBinary, op: ops[i], args: []*expr{nodes[i], node}}
	}
	return node, nil
}

func (p *scriptParser) afterToken(node *expr) (*expr, error) {
	if node != nil && (p.restStartsWith("++") || p.restStartsWith("--")) {
		return nil, errors.New("increment and decrement (++, --) are not supported")
	}
	return node, nil
}

func (p *scriptParser) token() (*expr, error) {
	p.spaces()
	if err := p.enter(); err != nil {
		return nil, err
	}
	node, err := p.tokenInner()
	p.depth--
	return node, err
}

func (p *scriptParser) tokenInner() (*expr, error) {
	if p.peek() == '/' && !p.atEnd() {
		re, err := p.regexLiteral()
		if err != nil {
			return nil, err
		}
		node, err := p.tokenProperty(re)
		if err != nil {
			return nil, err
		}
		return p.afterToken(node)
	}
	if p.restStartsWith("++") || p.restStartsWith("--") {
		return nil, errors.New("increment and decrement (++, --) are not supported")
	}
	if p.atEnd() {
		return nil, nil
	}
	c := p.peek()
	if isDigit(c) || c == '.' {
		return p.number()
	}
	var node *expr
	switch c {
	case '\'', '"':
		s, err := p.stringLiteral()
		if err != nil {
			return nil, err
		}
		node = &expr{kind: exprLiteral, literal: jsStringValue(s)}
	case '[':
		p.index++
		items, err := p.arguments(']')
		if err != nil {
			return nil, err
		}
		node = &expr{kind: exprArray, args: items}
	default:
		for _, text := range unaryOps {
			length := len([]rune(text))
			word := isIdentifierStart(c)
			if p.restStartsWith(text) && (!word || p.index+length < len(p.chars) && !isIdentifierPart(p.peekAt(length))) {
				p.index += length
				argument, err := p.token()
				if err != nil {
					return nil, err
				}
				if argument == nil {
					return nil, p.error("missing unaryOp argument")
				}
				return p.afterToken(&expr{kind: exprUnary, op: text, args: []*expr{argument}})
			}
		}
		switch {
		case isIdentifierStart(c):
			name, err := p.identifier()
			if err != nil {
				return nil, err
			}
			switch name {
			case "true":
				node = &expr{kind: exprLiteral, literal: jsBoolValue(true)}
			case "false":
				node = &expr{kind: exprLiteral, literal: jsBoolValue(false)}
			case "null":
				node = &expr{kind: exprLiteral, literal: null}
			case "undefined":
				node = &expr{kind: exprLiteral, literal: undefined}
			case "this":
				return nil, errors.New("`this` is not supported")
			default:
				node = &expr{kind: exprIdent, name: name}
			}
		case c == '(':
			group, err := p.group()
			if err != nil || group == nil {
				return nil, err
			}
			node = group
		default:
			return nil, nil
		}
	}
	node, err := p.tokenProperty(node)
	if err != nil {
		return nil, err
	}
	return p.afterToken(node)
}

func (p *scriptParser) tokenProperty(node *expr) (*expr, error) {
	p.spaces()
	depth := p.depth
	for !p.atEnd() {
		c := p.peek()
		if c != '.' && c != '[' && c != '(' && c != '?' {
			break
		}
		if err := p.enter(); err != nil {
			return nil, err
		}
		optional := false
		if c == '?' {
			if p.peekAt(1) != '.' {
				break
			}
			optional = true
			p.index += 2
			p.spaces()
			c = p.peek()
			if p.atEnd() {
				c = ' '
			}
		}
		p.index++
		switch {
		case c == '[':
			property, err := p.expression()
			if err != nil {
				return nil, err
			}
			if property == nil {
				return nil, p.unexpected()
			}
			p.spaces()
			if p.peek() != ']' || p.atEnd() {
				return nil, p.error("Unclosed [")
			}
			p.index++
			node = &expr{kind: exprMember, args: []*expr{node}, computed: property}
		case c == '(':
			call, err := p.call(node)
			if err != nil {
				return nil, err
			}
			node = call
		case c == '.' || optional:
			if optional {
				p.index--
			}
			p.spaces()
			name, err := p.identifier()
			if err != nil {
				return nil, err
			}
			node = &expr{kind: exprMember, args: []*expr{node}, name: name}
		}
		p.spaces()
	}
	p.depth = depth
	return node, nil
}

func (p *scriptParser) call(callee *expr) (*expr, error) {
	var function rfcFunction
	switch {
	case callee.kind == exprIdent:
		f, ok := rfcFunctionNamed(callee.name)
		if !ok {
			return nil, fmt.Errorf("unknown function %s()", callee.name)
		}
		function = f
	case callee.kind == exprMember && callee.computed == nil && !isAllowedMethodName(callee.name):
		return nil, fmt.Errorf("unsupported method %s()", callee.name)
	default:
		args, err := p.arguments(')')
		if err != nil {
			return nil, err
		}
		return &expr{kind: exprCall, args: append([]*expr{callee}, args...)}, nil
	}
	if !function.takesNodes() {
		args, err := p.arguments(')')
		if err != nil {
			return nil, err
		}
		if len(args) != function.arity() {
			return nil, fmt.Errorf("%s() takes %d argument(s)", function.name(), function.arity())
		}
		rfcArgs := make([]scriptRFCArg, len(args))
		for i, a := range args {
			rfcArgs[i] = scriptRFCArg{value: a}
		}
		return &expr{kind: exprRFC, function: function, rfcArgs: rfcArgs}, nil
	}
	p.spaces()
	start := p.index
	var prefix int
	var root bool
	switch {
	case p.restStartsWith("_$_root"):
		prefix, root = 7, true
	case p.restStartsWith("_$_v"):
		prefix = 4
	case p.restStartsWith("@"):
		prefix = 1
	case p.restStartsWith("$"):
		prefix, root = 1, true
	default:
		return nil, p.error(function.name() + "() takes a query (@ or $)")
	}
	if c := p.peekAt(prefix); p.index+prefix < len(p.chars) && isIdentifierPart(c) && c != '$' {
		return nil, p.error(function.name() + "() takes a query (@ or $)")
	}
	end, q := scanQuery(p.chars, start+prefix, root)
	p.index = end
	p.spaces()
	if p.peek() != ')' || p.atEnd() {
		return nil, p.error(function.name() + "() takes one query")
	}
	p.index++
	return &expr{kind: exprRFC, function: function, rfcArgs: []scriptRFCArg{{nodes: &q}}}, nil
}

func (p *scriptParser) number() (*expr, error) {
	var number strings.Builder
	digits := func() {
		for !p.atEnd() && isDigit(p.peek()) {
			number.WriteRune(p.peek())
			p.index++
		}
	}
	digits()
	if p.peek() == '.' && !p.atEnd() {
		number.WriteByte('.')
		p.index++
		digits()
	}
	if c := p.peek(); !p.atEnd() && (c == 'e' || c == 'E') {
		number.WriteRune(c)
		p.index++
		if s := p.peek(); !p.atEnd() && (s == '+' || s == '-') {
			number.WriteRune(s)
			p.index++
		}
		digits()
		if text := number.String(); !isDigit(rune(text[len(text)-1])) {
			return nil, p.error("Expected exponent (" + text + ")")
		}
	}
	text := number.String()
	if !p.atEnd() {
		if c := p.peek(); isIdentifierStart(c) {
			return nil, p.error("Variable names cannot start with a number (" + text + string(c) + ")")
		} else if c == '.' {
			return nil, p.error("Unexpected period")
		}
	}
	if text == "." {
		return nil, p.error("Unexpected period")
	}
	mantissa, _, _ := strings.Cut(strings.ReplaceAll(text, "E", "e"), "e")
	value := math.NaN()
	if strings.ContainsAny(mantissa, "0123456789") {
		value = parseFloat(strings.NewReplacer(".e", "e", ".E", "E").Replace(strings.TrimSuffix(text, ".")))
	}
	return &expr{kind: exprLiteral, literal: jsNumberValue(value)}, nil
}

func (p *scriptParser) stringLiteral() (string, error) {
	quote := p.peek()
	p.index++
	var out strings.Builder
	for !p.atEnd() {
		c := p.peek()
		p.index++
		if c == quote {
			return out.String(), nil
		}
		if c != '\\' {
			out.WriteRune(c)
			continue
		}
		if p.atEnd() {
			p.index++
			continue
		}
		escaped := p.peek()
		p.index++
		switch escaped {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'v':
			out.WriteByte('\v')
		default:
			out.WriteRune(escaped)
		}
	}
	return "", p.error(`Unclosed quote after "` + out.String() + `"`)
}

func (p *scriptParser) regexLiteral() (*expr, error) {
	p.index++
	start := p.index
	inClass := false
	for !p.atEnd() {
		c := p.peek()
		if c == '/' && !inClass {
			pattern := string(p.chars[start:p.index])
			p.index++
			flags := ""
			for !p.atEnd() && (isASCIILetter(p.peek()) || isDigit(p.peek())) {
				flags += string(p.peek())
				p.index++
			}
			re, err := newJSRegex(pattern, flags)
			if err != nil {
				return nil, p.error(err.Error())
			}
			return &expr{kind: exprLiteral, literal: jsValue{kind: jsRegexKind, re: re}}, nil
		}
		if c == '[' {
			inClass = true
		} else if inClass && c == ']' {
			inClass = false
		}
		if c == '\\' {
			p.index += 2
		} else {
			p.index++
		}
	}
	return nil, p.error("Unclosed Regex")
}

func (p *scriptParser) identifier() (string, error) {
	if p.atEnd() || !isIdentifierStart(p.peek()) {
		next := ""
		if !p.atEnd() {
			next = string(p.peek())
		}
		return "", p.error("Unexpected " + next)
	}
	start := p.index
	p.index++
	for !p.atEnd() && isIdentifierPart(p.peek()) {
		p.index++
	}
	return string(p.chars[start:p.index]), nil
}

func (p *scriptParser) arguments(terminator rune) ([]*expr, error) {
	var args []*expr
	separators := 0
	for {
		p.spaces()
		if p.atEnd() {
			return nil, p.error("Expected " + string(terminator))
		}
		switch c := p.peek(); {
		case c == terminator:
			p.index++
			if terminator == ')' && separators > 0 && separators >= len(args) {
				return nil, p.error("Unexpected token )")
			}
			for _, a := range args {
				if a == nil {
					return nil, errors.New("array holes are not supported")
				}
			}
			return args, nil
		case c == ',':
			p.index++
			separators++
			if separators != len(args) {
				if terminator == ')' {
					return nil, p.error("Unexpected token ,")
				}
				for len(args) < separators {
					args = append(args, nil)
				}
			}
		case len(args) != separators && separators != 0:
			return nil, p.error("Expected comma")
		default:
			node, err := p.expression()
			if err != nil {
				return nil, err
			}
			if node == nil {
				return nil, p.error("Expected comma")
			}
			args = append(args, node)
		}
	}
}

func (p *scriptParser) group() (*expr, error) {
	p.index++
	nodes, err := p.expressions(')')
	if err != nil {
		return nil, err
	}
	if p.peek() != ')' || p.atEnd() {
		return nil, p.error("Unclosed (")
	}
	p.index++
	switch len(nodes) {
	case 0:
		return nil, nil
	case 1:
		return nodes[0], nil
	}
	return nil, errors.New("sequence expressions (a, b) are not supported")
}

// substituteScript is jsonpath-plus's textual substitution of @ forms
// before jsep sees the code.
func substituteScript(code string) string {
	code = strings.ReplaceAll(code, "@parentProperty", "_$_parentProperty")
	code = strings.ReplaceAll(code, "@parent", "_$_parent")
	code = strings.ReplaceAll(code, "@property", "_$_property")
	code = strings.ReplaceAll(code, "@root", "_$_root")
	chars := []rune(code)
	var out strings.Builder
	for i, c := range chars {
		if c == '@' && i+1 < len(chars) && (chars[i+1] == '.' || chars[i+1] == ')' || chars[i+1] == '[' || isJSWhitespace(chars[i+1])) {
			out.WriteString("_$_v")
		} else {
			out.WriteRune(c)
		}
	}
	if strings.Contains(code, "@path") {
		return strings.ReplaceAll(out.String(), "@path", "_$_path")
	}
	return out.String()
}

type scope struct {
	value, property, parent, parentProperty jsValue
	path                                    *string
}

func evalScript(x *expr, s *scope, e *engine) (jsValue, error) {
	switch x.kind {
	case exprLiteral:
		return x.literal, nil
	case exprIdent:
		switch x.name {
		case "_$_v":
			return s.value, nil
		case "_$_property":
			return s.property, nil
		case "_$_parent":
			return s.parent, nil
		case "_$_parentProperty":
			return s.parentProperty, nil
		case "_$_root":
			return fromDoc(e.root), nil
		case "_$_path":
			if s.path != nil {
				return jsStringValue(*s.path), nil
			}
		}
		return undefined, fmt.Errorf("%s is not defined", x.name)
	case exprMember:
		key := x.name
		if x.computed != nil {
			k, err := evalScript(x.computed, s, e)
			if err != nil {
				return undefined, err
			}
			key = k.toJSString()
		}
		object, err := evalScript(x.args[0], s, e)
		if err != nil {
			return undefined, err
		}
		return getProperty(object, key)
	case exprCall:
		args := make([]jsValue, len(x.args)-1)
		for i, a := range x.args[1:] {
			v, err := evalScript(a, s, e)
			if err != nil {
				return undefined, err
			}
			args[i] = v
		}
		callee, err := evalScript(x.args[0], s, e)
		if err != nil {
			return undefined, err
		}
		if callee.kind != jsFunction {
			return undefined, fmt.Errorf("%s is not a function", callee.typeOf())
		}
		return callMethod(callee.fn, args)
	case exprRFC:
		args := make([][]jsValue, len(x.rfcArgs))
		for i, a := range x.rfcArgs {
			if a.value != nil {
				v, err := evalScript(a.value, s, e)
				if err != nil {
					return undefined, err
				}
				args[i] = []jsValue{v}
			} else {
				nodes, err := e.query(*a.nodes, s.value)
				if err != nil {
					return undefined, err
				}
				args[i] = nodes
			}
		}
		return x.function.callJS(args, e), nil
	case exprArray:
		items := make([]jsValue, len(x.args))
		for i, a := range x.args {
			v, err := evalScript(a, s, e)
			if err != nil {
				return undefined, err
			}
			items[i] = v
		}
		return jsArrayValue(items), nil
	case exprUnary:
		v, err := evalScript(x.args[0], s, e)
		if err != nil {
			return undefined, err
		}
		switch x.op {
		case "-":
			return jsNumberValue(-v.toNumber()), nil
		case "+":
			return jsNumberValue(v.toNumber()), nil
		case "!":
			return jsBoolValue(!v.truthy()), nil
		case "~":
			return jsNumberValue(float64(^v.toInt32())), nil
		case "typeof":
			return jsStringValue(v.typeOf()), nil
		}
		return undefined, nil
	case exprBinary:
		left, err := evalScript(x.args[0], s, e)
		if err != nil {
			return undefined, err
		}
		if x.op == "||" || x.op == "&&" {
			if left.truthy() == (x.op == "||") {
				return left, nil
			}
			return evalScript(x.args[1], s, e)
		}
		right, err := evalScript(x.args[1], s, e)
		if err != nil {
			return undefined, err
		}
		return binary(x.op, left, right), nil
	}
	test, err := evalScript(x.args[0], s, e)
	if err != nil {
		return undefined, err
	}
	if test.truthy() {
		return evalScript(x.args[1], s, e)
	}
	return evalScript(x.args[2], s, e)
}

func binary(op string, a, b jsValue) jsValue {
	switch op {
	case "|":
		return jsNumberValue(float64(a.toInt32() | b.toInt32()))
	case "^":
		return jsNumberValue(float64(a.toInt32() ^ b.toInt32()))
	case "&":
		return jsNumberValue(float64(a.toInt32() & b.toInt32()))
	case "==":
		return jsBoolValue(a.looseEq(b))
	case "!=":
		return jsBoolValue(!a.looseEq(b))
	case "===":
		return jsBoolValue(a.strictEq(b))
	case "!==":
		return jsBoolValue(!a.strictEq(b))
	case "<":
		less, ok := a.lessThan(b)
		return jsBoolValue(ok && less)
	case ">":
		less, ok := b.lessThan(a)
		return jsBoolValue(ok && less)
	case "<=":
		less, ok := b.lessThan(a)
		return jsBoolValue(ok && !less)
	case ">=":
		less, ok := a.lessThan(b)
		return jsBoolValue(ok && !less)
	case "<<":
		return jsNumberValue(float64(a.toInt32() << (b.toUint32() & 31)))
	case ">>":
		return jsNumberValue(float64(a.toInt32() >> (b.toUint32() & 31)))
	case ">>>":
		return jsNumberValue(float64(a.toUint32() >> (b.toUint32() & 31)))
	case "+":
		return a.add(b)
	case "-":
		return jsNumberValue(a.toNumber() - b.toNumber())
	case "*":
		return jsNumberValue(a.toNumber() * b.toNumber())
	case "/":
		return jsNumberValue(a.toNumber() / b.toNumber())
	case "%":
		return jsNumberValue(math.Mod(a.toNumber(), b.toNumber()))
	}
	return undefined
}
