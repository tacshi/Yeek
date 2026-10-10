package engine

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// FormatJSON is Yaak's format_json (crates/yaak-templates), behind its
// editor's Format and its pretty JSON responses: it reindents JSON by its
// characters, so invalid JSON formats too, template tags and strings are
// kept whole (#710: quoted strings in tags may hold ]}), and comments stay.
func FormatJSON(text, tab string) string {
	r := []rune(text)
	var out []rune
	var quoted tagStrings
	const (
		none = iota
		inString
		inTag
	)
	state, depth := none, 0
	sawNewline := false
	indent := func() { out = append(out, []rune("\n"+strings.Repeat(tab, depth))...) }
	trimmed := func() string { return strings.TrimRight(string(out), " \t") }
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch state {
		case inString:
			out = append(out, c)
			if c == '"' {
				state = none
			} else if c == '\\' && i+1 < len(r) {
				i++
				out = append(out, r[i])
			}
			continue
		case inTag:
			// A quoted argument is allowed to contain ]}, so skip over strings whole.
			if c == '\'' {
				out = append(out, c)
				if end, ok := quoted.take(r, i+1); ok {
					out = append(out, r[i+1:end]...)
					i = end - 1
				}
				continue
			}
			if c == ']' && i+1 < len(r) && r[i+1] == '}' {
				state = none
				out = append(out, ']', '}')
				i++
				continue
			}
			out = append(out, c)
			continue
		}
		if c == '$' && i+2 < len(r) && r[i+1] == '{' && r[i+2] == '[' {
			state = inTag
			out = append(out, '$', '{', '[')
			i += 2
			continue
		}
		if c == '/' && i+1 < len(r) && r[i+1] == '/' {
			i += 2
			comment := []rune("//")
			for i < len(r) && r[i] != '\n' {
				comment = append(comment, r[i])
				i++
			}
			i--
			// Whether the comma before already broke the line.
			if t := trimmed(); strings.HasSuffix(t, ",\n") && !sawNewline {
				// A trailing comment on the line of its comma, as "foo",// comment.
				out = append([]rune(t[:len(t)-1]), ' ')
			} else if !strings.HasSuffix(t, "\n") && len(out) > 0 {
				// A trailing comment after a value.
				out = append(out, ' ')
			}
			out = append(out, comment...)
			indent()
			sawNewline = false
			continue
		}
		if c == '/' && i+1 < len(r) && r[i+1] == '*' {
			i += 2
			comment := []rune("/*")
			for ; i < len(r); i++ {
				if r[i] == '*' && i+1 < len(r) && r[i+1] == '/' {
					comment = append(comment, '*', '/')
					i++
					break
				}
				comment = append(comment, r[i])
			}
			// On a fresh line, then a fresh line for what follows.
			if t := trimmed(); t != "" && !strings.HasSuffix(t, "\n") {
				indent()
			}
			out = append(out, comment...)
			indent()
			continue
		}
		switch c {
		case ',':
			out = append(out, c)
			indent()
		case '{', '[':
			closer := map[rune]rune{'{': '}', '[': ']'}[c]
			if i+1 < len(r) && r[i+1] == closer {
				out = append(out, c, closer)
				i++
				continue
			}
			depth++
			out = append(out, c)
			indent()
		case '}', ']':
			// Invalid JSON may close more than it opens.
			if depth > 0 {
				depth--
			}
			indent()
			out = append(out, c)
		case ':':
			out = append(out, ':', ' ')
		case '"':
			state = inString
			out = append(out, c)
		case ' ', '\n', '\t', '\r':
			if c == '\n' {
				sawNewline = true
			}
		default:
			sawNewline = false
			out = append(out, c)
		}
	}
	// Drop whitespace-only lines, keeping empty lines inside block comments.
	var lines []string
	inComment := false
	for _, line := range rustLines(string(out)) {
		if inComment {
			lines = append(lines, line)
			if strings.Contains(line, "*/") {
				inComment = false
			}
			continue
		}
		if strings.Contains(line, "/*") && !strings.Contains(line, "*/") {
			inComment = true
		}
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	for i, line := range lines {
		lines[i] = strings.TrimRightFunc(line, isRustWhitespace)
	}
	return strings.Join(lines, "\n")
}

func isRustWhitespace(r rune) bool {
	return strings.ContainsRune(" \t\n\v\f\r\u0085                 　", r)
}

var unicodeLiteral = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)

// PrettyJSONResponse is how Yaak's TextViewer shows a JSON response pretty:
// formatted, its \uXXXX escapes decoded and \/ written /.
func PrettyJSONResponse(text string) string {
	if text == "" {
		return text
	}
	body := FormatJSON(text, "  ")
	// JavaScript's String.fromCharCode of each escape, joined as UTF-16.
	var units []uint16
	var b strings.Builder
	flush := func() {
		if len(units) > 0 {
			b.WriteString(string(utf16.Decode(units)))
			units = units[:0]
		}
	}
	last := 0
	for _, m := range unicodeLiteral.FindAllStringSubmatchIndex(body, -1) {
		if m[0] != last {
			flush()
			b.WriteString(body[last:m[0]])
		}
		n, _ := strconv.ParseUint(body[m[2]:m[3]], 16, 16)
		units = append(units, uint16(n))
		last = m[1]
	}
	flush()
	b.WriteString(body[last:])
	return strings.ReplaceAll(b.String(), `\/`, "/")
}
