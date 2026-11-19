package query

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tokLParen tokKind = iota
	tokRParen
	tokMinus
	tokOr
	tokAnd
	tokWord
	tokPhrase
	tokField
)

type tok struct {
	kind      tokKind
	span      Span
	text      string
	path      []string
	pathSpan  Span
	value     FieldValue
	valueSpan Span
}

func lex(input string) []tok {
	var toks []tok
	i := 0
	for i < len(input) {
		c, size := utf8.DecodeRuneInString(input[i:])
		if unicode.IsSpace(c) {
			i += size
			continue
		}
		var t tok
		var end int
		switch c {
		case '(':
			t, end = tok{kind: tokLParen}, i+1
		case ')':
			t, end = tok{kind: tokRParen}, i+1
		case '-':
			t, end = tok{kind: tokMinus}, i+1
		case '"':
			var text string
			text, end = lexQuoted(input, i)
			t = tok{kind: tokPhrase, text: text}
		case '@':
			var ok bool
			if t, end, ok = lexField(input, i); !ok {
				t, end = lexWord(input, i)
			}
		default:
			t, end = lexWord(input, i)
		}
		t.span = NewSpan(i, end)
		toks = append(toks, t)
		i = end
	}
	return toks
}

func endsWord(c rune) bool {
	return unicode.IsSpace(c) || c == '(' || c == ')' || c == '"'
}

func endsValue(c rune) bool {
	return unicode.IsSpace(c) || c == ')'
}

func runUntil(input string, start int, stop func(rune) bool) int {
	n := strings.IndexFunc(input[start:], stop)
	if n < 0 {
		return len(input)
	}
	return start + n
}

func lexWord(input string, start int) (tok, int) {
	_, first := utf8.DecodeRuneInString(input[start:])
	end := runUntil(input, start+first, endsWord)
	text := input[start:end]
	switch text {
	case "OR":
		return tok{kind: tokOr}, end
	case "AND":
		return tok{kind: tokAnd}, end
	}
	return tok{kind: tokWord, text: text}, end
}

func lexQuoted(input string, start int) (string, int) {
	var b strings.Builder
	j := start + 1
	for j < len(input) {
		c, size := utf8.DecodeRuneInString(input[j:])
		switch c {
		case '"':
			return b.String(), j + 1
		case '\\':
			if j+1 < len(input) && (input[j+1] == '"' || input[j+1] == '\\') {
				b.WriteByte(input[j+1])
				j += 2
				continue
			}
			b.WriteByte('\\')
		default:
			b.WriteString(input[j : j+size])
		}
		j += size
	}
	return b.String(), len(input)
}

func isIdentChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}

func lexField(input string, start int) (tok, int, bool) {
	var path []string
	i := start + 1
	for {
		end := runUntil(input, i, func(c rune) bool { return !isIdentChar(c) })
		if end == i {
			return tok{}, 0, false
		}
		path = append(path, input[i:end])
		i = end
		if i >= len(input) {
			return tok{}, 0, false
		}
		if input[i] == '.' {
			i++
			continue
		}
		if input[i] == ':' {
			break
		}
		return tok{}, 0, false
	}
	value, valueEnd := lexValue(input, i+1)
	t := tok{
		kind:      tokField,
		path:      path,
		pathSpan:  NewSpan(start, i+1),
		value:     value,
		valueSpan: NewSpan(i+1, valueEnd),
	}
	return t, valueEnd, true
}

func lexValue(input string, start int) (FieldValue, int) {
	if start >= len(input) {
		return TextValue("", false), start
	}
	c, _ := utf8.DecodeRuneInString(input[start:])
	switch {
	case endsValue(c):
		return TextValue("", false), start
	case c == '"':
		text, end := lexQuoted(input, start)
		return TextValue(text, true), end
	case c == '[':
		if low, high, end, ok := lexRange(input, start); ok {
			return FieldValue{Kind: ValueRange, Low: low, High: high}, end
		}
	}
	end := runUntil(input, start, endsValue)
	return classifyBare(input[start:end]), end
}

func classifyBare(raw string) FieldValue {
	if raw == "*" {
		return FieldValue{Kind: ValueExists}
	}
	if op, number, ok := parseCompare(raw); ok {
		return FieldValue{Kind: ValueCompare, Op: op, Number: number}
	}
	if strings.Contains(raw, "*") {
		return FieldValue{Kind: ValueWildcard, Pattern: raw}
	}
	return TextValue(raw, false)
}

func parseCompare(raw string) (CompareOp, NumberLiteral, bool) {
	var op CompareOp
	var rest string
	switch {
	case strings.HasPrefix(raw, ">="):
		op, rest = Gte, raw[2:]
	case strings.HasPrefix(raw, "<="):
		op, rest = Lte, raw[2:]
	case strings.HasPrefix(raw, ">"):
		op, rest = Gt, raw[1:]
	case strings.HasPrefix(raw, "<"):
		op, rest = Lt, raw[1:]
	default:
		return 0, NumberLiteral{}, false
	}
	number, ok := parseNumber(rest)
	return op, number, ok
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isASCIIAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func digitRun(s string) int {
	for i := range len(s) {
		if !isASCIIDigit(s[i]) {
			return i
		}
	}
	return len(s)
}

func parseNumber(raw string) (NumberLiteral, bool) {
	body := strings.TrimPrefix(raw, "-")
	digitsEnd := digitRun(body)
	if digitsEnd == 0 {
		return NumberLiteral{}, false
	}
	end := digitsEnd
	if fraction, ok := strings.CutPrefix(body[end:], "."); ok {
		fractionLen := digitRun(fraction)
		if fractionLen == 0 {
			return NumberLiteral{}, false
		}
		end += 1 + fractionLen
	}
	unit := body[end:]
	for i := range len(unit) {
		if !isASCIIAlpha(unit[i]) {
			return NumberLiteral{}, false
		}
	}
	numberLen := len(raw) - len(body) + end
	value, ok := parseFloat(raw[:numberLen])
	if !ok {
		return NumberLiteral{}, false
	}
	return NumberLiteral{Value: value, Unit: unit, Raw: raw}, true
}

func parseFloat(s string) (float64, bool) {
	digits := strings.TrimLeft(s, "+-")
	if len(s)-len(digits) > 1 || strings.ContainsRune(s, '_') {
		return 0, false
	}
	if len(digits) > 1 && digits[0] == '0' && (digits[1]|0x20 == 'x' || digits[1]|0x20 == 'b' || digits[1]|0x20 == 'o') {
		return 0, false
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if numErr, ok := errors.AsType[*strconv.NumError](err); ok && numErr.Err == strconv.ErrRange {
			return value, true
		}
		return 0, false
	}
	return value, true
}

func lexRange(input string, start int) (NumberLiteral, NumberLiteral, int, bool) {
	notSpace := func(c rune) bool { return !unicode.IsSpace(c) }
	endsNumber := func(c rune) bool { return unicode.IsSpace(c) || c == ']' }
	skipWS := func(i int) int { return runUntil(input, i, notSpace) }
	fail := func() (NumberLiteral, NumberLiteral, int, bool) {
		return NumberLiteral{}, NumberLiteral{}, 0, false
	}
	i := skipWS(start + 1)
	lowEnd := runUntil(input, i, endsNumber)
	low, ok := parseNumber(input[i:lowEnd])
	if !ok {
		return fail()
	}
	i = skipWS(lowEnd)
	if i == lowEnd || !strings.HasPrefix(input[i:], "TO") {
		return fail()
	}
	i += 2
	afterTo := skipWS(i)
	if afterTo == i {
		return fail()
	}
	i = afterTo
	highEnd := runUntil(input, i, endsNumber)
	high, ok := parseNumber(input[i:highEnd])
	if !ok {
		return fail()
	}
	i = skipWS(highEnd)
	if !strings.HasPrefix(input[i:], "]") {
		return fail()
	}
	end := i + 1
	if end < len(input) {
		if c, _ := utf8.DecodeRuneInString(input[end:]); !endsValue(c) {
			return fail()
		}
	}
	return low, high, end, true
}
