package filter

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/dlclark/regexp2/v2"
)

const (
	jsWhitespace      = `\t\n\x0B\x0C\r \u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000\uFEFF`
	wordBoundary      = `(?:(?<=[0-9A-Za-z_])(?![0-9A-Za-z_])|(?<![0-9A-Za-z_])(?=[0-9A-Za-z_]))`
	notWordBoundary   = `(?:(?<=[0-9A-Za-z_])(?=[0-9A-Za-z_])|(?<![0-9A-Za-z_])(?![0-9A-Za-z_]))`
	notLineTerminator = `[^\n\r\u2028\u2029]`
	lineStart         = `(?:^|(?<=[\n\r\u2028\u2029]))`
	lineEnd           = `(?=[\n\r\u2028\u2029]|\z)`
	regexTimeout      = 2 * time.Second
)

type jsRegex struct {
	source, flags string
	re            *regexp2.Regexp
	global        bool
	lastIndex     int
}

func newJSRegex(source, flags string) (*jsRegex, error) {
	seen := ""
	for _, flag := range flags {
		if strings.ContainsRune(seen, flag) || !strings.ContainsRune("dgimsuvy", flag) {
			return nil, fmt.Errorf("Invalid flags supplied to RegExp constructor '%s'", flags) //nolint:staticcheck
		}
		if flag == 'v' || flag == 'y' {
			return nil, fmt.Errorf("the regular expression flag %c is not supported", flag)
		}
		seen += string(flag)
	}
	translated, err := translateRegex(source, strings.ContainsRune(flags, 's'), strings.ContainsRune(flags, 'm'))
	if err != nil {
		return nil, err
	}
	options := regexp2.None
	if strings.ContainsRune(flags, 'i') {
		options |= regexp2.IgnoreCase
	}
	re, err := regexp2.Compile(translated, options)
	if err != nil {
		return nil, fmt.Errorf("Invalid regular expression: /%s/%s: %w", source, flags, err) //nolint:staticcheck
	}
	re.MatchTimeout = regexTimeout
	if source == "" {
		source = "(?:)"
	}
	return &jsRegex{source: source, flags: flags, re: re, global: strings.ContainsRune(flags, 'g')}, nil
}

func iRegexp(pattern string, whole bool) *jsRegex {
	if whole {
		pattern = "^(?:" + pattern + ")$"
	}
	re, err := newJSRegex(pattern, "u")
	if err != nil {
		return nil
	}
	return re
}

func regexRuntimeError(err error) error { return fmt.Errorf("regular expression failed: %w", err) }

func (r *jsRegex) find(text []rune, from int) (*regexp2.Match, error) {
	m, err := r.re.FindRunesMatchStartingAt(text, from)
	if err != nil {
		return nil, regexRuntimeError(err)
	}
	return m, nil
}

func (r *jsRegex) isMatch(text string) (bool, error) {
	ok, err := r.re.MatchRunes([]rune(text))
	if err != nil {
		return false, regexRuntimeError(err)
	}
	return ok, nil
}

func (r *jsRegex) test(text string) (bool, error) {
	if !r.global {
		return r.isMatch(text)
	}
	runes := []rune(text)
	start, ok := utf16ToRune(runes, r.lastIndex)
	if !ok {
		r.lastIndex = 0
		return false, nil
	}
	m, err := r.find(runes, start)
	if err != nil {
		return false, err
	}
	if m == nil {
		r.lastIndex = 0
		return false, nil
	}
	r.lastIndex = runeToUTF16(runes, m.RuneIndex+m.RuneLength)
	return true, nil
}

func (r *jsRegex) matchString(text string) (jsValue, error) {
	runes := []rune(text)
	if r.global {
		r.lastIndex = 0
		var matches []jsValue
		for pos := 0; pos <= len(runes); {
			m, err := r.find(runes, pos)
			if err != nil {
				return undefined, err
			}
			if m == nil {
				break
			}
			matches = append(matches, jsStringValue(m.String()))
			pos = m.RuneIndex + m.RuneLength
			if m.RuneLength == 0 {
				pos++
			}
		}
		if len(matches) == 0 {
			return null, nil
		}
		return jsArrayValue(matches), nil
	}
	m, err := r.find(runes, 0)
	if err != nil || m == nil {
		return null, err
	}
	return jsValue{kind: jsArrayKind, arr: &jsArray{
		items: captureValues(m, 0),
		match: &matchInfo{index: float64(runeToUTF16(runes, m.RuneIndex)), input: text},
	}}, nil
}

func (r *jsRegex) split(text string, limit uint64) ([]jsValue, error) {
	var parts []jsValue
	if limit == 0 {
		return parts, nil
	}
	runes := []rune(text)
	if len(runes) == 0 {
		m, err := r.find(runes, 0)
		if err != nil {
			return nil, err
		}
		if m == nil {
			parts = append(parts, jsStringValue(""))
		}
		return parts, nil
	}
	full := func() bool { return uint64(len(parts)) == limit }
	p, q := 0, 0
	for q < len(runes) {
		m, err := r.find(runes, q)
		if err != nil {
			return nil, err
		}
		if m == nil {
			break
		}
		q = m.RuneIndex
		if q >= len(runes) {
			break
		}
		e := min(m.RuneIndex+m.RuneLength, len(runes))
		if e == p {
			q++
			continue
		}
		parts = append(parts, jsStringValue(string(runes[p:q])))
		if full() {
			return parts, nil
		}
		p = e
		for _, capture := range captureValues(m, 1) {
			parts = append(parts, capture)
			if full() {
				return parts, nil
			}
		}
		q = p
	}
	return append(parts, jsStringValue(string(runes[p:]))), nil
}

func captureValues(m *regexp2.Match, from int) []jsValue {
	groups := m.Groups()
	values := []jsValue{}
	for i := from; i < len(groups); i++ {
		if len(groups[i].Captures) == 0 {
			values = append(values, undefined)
		} else {
			values = append(values, jsStringValue(groups[i].String()))
		}
	}
	return values
}

func utf16ToRune(runes []rune, index int) (int, bool) {
	units := 0
	for i, c := range runes {
		if units >= index {
			return i, true
		}
		units += utf16.RuneLen(c)
	}
	return len(runes), units >= index
}

func runeToUTF16(runes []rune, index int) int {
	units := 0
	for _, c := range runes[:index] {
		units += utf16.RuneLen(c)
	}
	return units
}

func translateRegex(source string, dotAll, multiline bool) (string, error) {
	chars := []rune(source)
	groups := regexGroupNames(chars)
	var out strings.Builder
	inClass := false
	i := 0
	for i < len(chars) {
		c := chars[i]
		i++
		switch {
		case c == '\\':
			if i >= len(chars) {
				return "", fmt.Errorf("Invalid regular expression: \\ at end of pattern") //nolint:staticcheck
			}
			e := chars[i]
			i++
			switch {
			case e == 'd':
				out.WriteString(classOr(inClass, "0-9"))
			case e == 'D':
				out.WriteString("[^0-9]")
			case e == 'w':
				out.WriteString(classOr(inClass, "0-9A-Za-z_"))
			case e == 'W':
				out.WriteString("[^0-9A-Za-z_]")
			case e == 's':
				out.WriteString(classOr(inClass, jsWhitespace))
			case e == 'S':
				out.WriteString("[^" + jsWhitespace + "]")
			case e == 'b' && inClass:
				out.WriteString(`\x08`)
			case e == 'b':
				out.WriteString(wordBoundary)
			case e == 'B':
				out.WriteString(notWordBoundary)
			case e == 'n':
				out.WriteString(`\n`)
			case e == 'r':
				out.WriteString(`\r`)
			case e == 't':
				out.WriteString(`\t`)
			case e == 'f':
				out.WriteString(`\x0C`)
			case e == 'v':
				out.WriteString(`\x0B`)
			case e == '0' && !isDigit(runeAt(chars, i)):
				out.WriteString(`\x00`)
			case e >= '1' && e <= '9' && !inClass:
				digits := string(e)
				for i < len(chars) && isDigit(chars[i]) {
					digits += string(chars[i])
					i++
				}
				out.WriteString(`(?:\` + digits + `)`)
			case e == 'x' && hexRun(chars, i, 2):
				writeRegexRune(&out, rune(hexValue(chars[i:i+2])))
				i += 2
			case e == 'u' && i < len(chars) && chars[i] == '{':
				end := indexRune(chars, i, '}')
				if end < 0 {
					return "", fmt.Errorf("Invalid regular expression: invalid Unicode escape") //nolint:staticcheck
				}
				if !hexRun(chars, i+1, end-i-1) || end == i+1 {
					return "", fmt.Errorf("Invalid regular expression: invalid Unicode escape") //nolint:staticcheck
				}
				writeRegexRune(&out, rune(hexValue(chars[i+1:end])))
				i = end + 1
			case e == 'u' && hexRun(chars, i, 4):
				high := hexValue(chars[i : i+4])
				i += 4
				code := high
				if high >= 0xD800 && high < 0xDC00 && i+1 < len(chars) && chars[i] == '\\' && chars[i+1] == 'u' && hexRun(chars, i+2, 4) {
					if low := hexValue(chars[i+2 : i+6]); low >= 0xDC00 && low < 0xE000 {
						code = 0x10000 + (high-0xD800)<<10 + (low - 0xDC00)
						i += 6
					}
				}
				writeRegexRune(&out, rune(code))
			case e == 'c' && i < len(chars) && isASCIILetter(chars[i]):
				writeRegexRune(&out, chars[i]%32)
				i++
			case e == 'k' && i < len(chars) && chars[i] == '<':
				end := indexRune(chars, i, '>')
				if n, ok := groups[string(chars[i+1:max(end, i+1)])]; end > 0 && ok {
					fmt.Fprintf(&out, `(?:\%d)`, n)
					i = end + 1
				} else {
					out.WriteString(`\k`)
				}
			case (e == 'p' || e == 'P') && i < len(chars) && chars[i] == '{':
				end := indexRune(chars, i, '}')
				if end < 0 {
					end = len(chars) - 1
				}
				out.WriteString(`\` + string(e) + string(chars[i:end+1]))
				i = end + 1
			default:
				writeRegexLiteral(&out, e)
			}
		case c == '[' && inClass:
			out.WriteString(`\[`)
		case c == '[':
			negated := i < len(chars) && chars[i] == '^'
			body := i
			if negated {
				body++
			}
			if body < len(chars) && chars[body] == ']' {
				if negated {
					out.WriteString(`[\s\S]`)
				} else {
					out.WriteString(`[^\s\S]`)
				}
				i = body + 1
			} else {
				inClass = true
				out.WriteByte('[')
				if negated {
					out.WriteByte('^')
					i++
				}
			}
		case c == ']' && inClass:
			inClass = false
			out.WriteByte(']')
		case (c == '&' || c == '~' || c == '-' && i < len(chars) && chars[i] == '[') && inClass:
			out.WriteString(`\` + string(c))
		case c == ']' || c == '}':
			writeRegexLiteral(&out, c)
		case c == '{' && !inClass:
			if n := quantifierLen(chars[i:]); n > 0 && out.Len() > 0 {
				out.WriteString("{" + string(chars[i:i+n]))
				i += n
			} else {
				writeRegexLiteral(&out, '{')
			}
		case c == '.' && !inClass && !dotAll:
			out.WriteString(notLineTerminator)
		case c == '.' && !inClass:
			out.WriteString(`[\s\S]`)
		case c == '^' && !inClass && multiline:
			out.WriteString(lineStart)
		case c == '$' && !inClass && multiline:
			out.WriteString(lineEnd)
		case c == '$' && !inClass:
			out.WriteString(`\z`)
		case c == '(' && !inClass && runeAt(chars, i) == '?' && runeAt(chars, i+1) == '<' && !isLookbehind(runeAt(chars, i+2)):
			end := indexRune(chars, i, '>')
			if end < 0 {
				return "", fmt.Errorf("Invalid regular expression: /%s/: Invalid capture group name", source) //nolint:staticcheck
			}
			out.WriteByte('(')
			i = end + 1
		default:
			out.WriteRune(c)
		}
	}
	return out.String(), nil
}

func regexGroupNames(chars []rune) map[string]int {
	names := map[string]int{}
	count, inClass := 0, false
	for i := 0; i < len(chars); i++ {
		switch c := chars[i]; {
		case c == '\\':
			i++
		case c == '[' && !inClass:
			inClass = true
		case c == ']' && inClass:
			inClass = false
		case c == '(' && !inClass:
			switch {
			case i+1 >= len(chars) || chars[i+1] != '?':
				count++
			case runeAt(chars, i+2) == '<' && !isLookbehind(runeAt(chars, i+3)):
				count++
				if end := indexRune(chars, i+3, '>'); end > 0 {
					names[string(chars[i+3:end])] = count
				}
			}
		}
	}
	return names
}

func runeAt(chars []rune, i int) rune {
	if i < len(chars) {
		return chars[i]
	}
	return 0
}

func isLookbehind(c rune) bool { return c == '=' || c == '!' }

func classOr(inClass bool, body string) string {
	if inClass {
		return body
	}
	return "[" + body + "]"
}

func writeRegexRune(out *strings.Builder, r rune) {
	if r > 0xFFFF {
		out.WriteRune(r)
		return
	}
	fmt.Fprintf(out, `\u%04X`, r)
}

func writeRegexLiteral(out *strings.Builder, c rune) {
	if c < 0x80 && (isASCIIPunct(c) || c == ' ') {
		out.WriteByte('\\')
	}
	out.WriteRune(c)
}

func isDigit(c rune) bool       { return c >= '0' && c <= '9' }
func isASCIILetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isHexDigit(c rune) bool    { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func isASCIIPunct(c rune) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

func hexRun(chars []rune, from, length int) bool {
	if from+length > len(chars) || length <= 0 {
		return false
	}
	for _, c := range chars[from : from+length] {
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func hexValue(chars []rune) int {
	n := 0
	for _, c := range chars {
		n = n*16 + digitValue(c)
		if n > 0x10FFFF {
			return 0xFFFD
		}
	}
	return n
}

func indexRune(chars []rune, from int, c rune) int {
	for i := from; i < len(chars); i++ {
		if chars[i] == c {
			return i
		}
	}
	return -1
}

func quantifierLen(rest []rune) int {
	digits := func(from int) int {
		n := 0
		for from+n < len(rest) && isDigit(rest[from+n]) {
			n++
		}
		return n
	}
	i := digits(0)
	if i == 0 {
		return 0
	}
	if i < len(rest) && rest[i] == ',' {
		i++
		i += digits(i)
	}
	if i < len(rest) && rest[i] == '}' {
		return i + 1
	}
	return 0
}
