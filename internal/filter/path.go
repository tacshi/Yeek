package filter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
)

type locKind uint8

const (
	locStr locKind = iota
	locPart
	locNum
	locOther
	locArrayKey
	locNames
	locFilter
)

type loc struct {
	kind  locKind
	s     string
	n     float64
	names []string
}

const (
	placeholderOpen  = '\uE000'
	placeholderClose = '\uE001'
)

var (
	propertyType = regexp.MustCompile(`@[A-Za-z0-9_$\-]+\(\)`)
	scriptStep   = regexp2.MustCompile(`[\['](\??\([^\n\r\u2028\u2029]*?\))[\]'](?![^\n\r\u2028\u2029]\])`, regexp2.None)
	quotedStep   = regexp.MustCompile(`\[['"]([^'\]]*)['"]\]`)
	boundary     = regexp2.MustCompile(`['"]?\.['"]?(?![^\[]*\])|\[['"]?`, regexp2.None)
	parentStep   = regexp.MustCompile(`(?:;)?(\^+)(?:;)?`)
	descendant   = regexp.MustCompile(`;;;|;;`)
	trailing     = regexp.MustCompile(`;$|'?\]|'$`)
	subxStep     = regexp.MustCompile(`#([0-9]+)`)
)

func replaceGo(re *regexp.Regexp, text string, rep func(groups []string) string) string {
	var out strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
		out.WriteString(text[last:m[0]])
		groups := make([]string, len(m)/2)
		for i := range groups {
			if m[2*i] >= 0 {
				groups[i] = text[m[2*i]:m[2*i+1]]
			}
		}
		out.WriteString(rep(groups))
		last = m[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

func replaceRegexp2(re *regexp2.Regexp, text string, rep func(m regexp2.Match) string) (string, error) {
	out, err := re.ReplaceFunc(text, rep, -1, -1)
	if err != nil {
		return "", fmt.Errorf("path is too complex: %w", err)
	}
	return out, nil
}

func parsePath(path string) ([]loc, error) {
	text, lifted := path, []loc(nil)
	if !strings.ContainsRune(path, placeholderOpen) && !strings.ContainsRune(path, placeholderClose) {
		text, lifted = liftPath(path)
	}
	var subx []string
	normalized := replaceGo(propertyType, text, func(g []string) string { return ";" + g[0] + ";" })
	normalized, err := replaceRegexp2(scriptStep, normalized, func(m regexp2.Match) string {
		subx = append(subx, m.GroupByNumber(1).String())
		return fmt.Sprintf("[#%d]", len(subx)-1)
	})
	if err != nil {
		return nil, err
	}
	normalized = replaceGo(quotedStep, normalized, func(g []string) string {
		return "['" + strings.ReplaceAll(strings.ReplaceAll(g[1], ".", "%@%"), "~", "%%@@%%") + "']"
	})
	normalized = strings.ReplaceAll(normalized, "~", ";~;")
	normalized, err = replaceRegexp2(boundary, normalized, func(regexp2.Match) string { return ";" })
	if err != nil {
		return nil, err
	}
	normalized = strings.ReplaceAll(strings.ReplaceAll(normalized, "%@%", "."), "%%@@%%", "~")
	normalized = replaceGo(parentStep, normalized, func(g []string) string {
		return ";" + strings.Join(strings.Split(g[1], ""), ";") + ";"
	})
	normalized = replaceGo(descendant, normalized, func([]string) string { return ";..;" })
	normalized = replaceGo(trailing, normalized, func([]string) string { return "" })

	parts := strings.Split(normalized, ";")
	locs := make([]loc, 0, len(parts))
	for _, part := range parts {
		if index, ok := placeholderIndex(part); ok {
			if index < len(lifted) {
				locs = append(locs, lifted[index])
			} else {
				locs = append(locs, loc{kind: locStr, s: part})
			}
			continue
		}
		m := subxStep.FindStringSubmatch(part)
		if m == nil {
			locs = append(locs, loc{kind: locStr, s: part})
			continue
		}
		if index, err := strconv.ParseUint(m[1], 10, 64); err == nil && index < uint64(len(subx)) {
			locs = append(locs, loc{kind: locStr, s: subx[index]})
		} else {
			locs = append(locs, loc{kind: locOther, s: "undefined"})
		}
	}
	return locs, nil
}

func literalKeys(path string) ([]string, bool) {
	if !strings.Contains(path, `["`) {
		return nil, false
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(path), "$")
	if !ok {
		return nil, false
	}
	keys := []string{}
	for rest != "" {
		switch {
		case rest[0] == '.':
			after := rest[1:]
			n := 0
			for n < len(after) && (isASCIIAlnum(after[n]) || after[n] == '_' || after[n] == '$') {
				n++
			}
			name := after[:n]
			if name == "" || name == "$" || name[0] >= '0' && name[0] <= '9' {
				return nil, false
			}
			keys = append(keys, name)
			rest = after[n:]
		case strings.HasPrefix(rest, `["`):
			after := rest[2:]
			end, escaped := -1, false
			for i := 0; i < len(after); i++ {
				c := after[i]
				if c == '"' && !escaped {
					end = i
					break
				}
				escaped = c == '\\' && !escaped
			}
			if end < 0 {
				return nil, false
			}
			key, err := parseJSON(`"` + after[:end+1])
			if err != nil || key.kind != jsonString {
				return nil, false
			}
			keys = append(keys, key.s)
			rest, ok = strings.CutPrefix(after[end+1:], "]")
			if !ok {
				return nil, false
			}
		case rest[0] == '[':
			after := rest[1:]
			n := 0
			for n < len(after) && after[n] >= '0' && after[n] <= '9' {
				n++
			}
			digits := after[:n]
			index, err := strconv.ParseUint(digits, 10, 64)
			if err != nil || index > 9007199254740991 || len(digits) > 1 && digits[0] == '0' {
				return nil, false
			}
			keys = append(keys, strconv.FormatUint(index, 10))
			rest, ok = strings.CutPrefix(after[n:], "]")
			if !ok {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return keys, true
}

func isASCIIAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func placeholderIndex(part string) (int, bool) {
	inner, ok := strings.CutPrefix(part, string(placeholderOpen))
	if !ok {
		return 0, false
	}
	inner, ok = strings.CutSuffix(inner, string(placeholderClose))
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(inner, 10, 63)
	return int(n), err == nil
}

func liftPath(path string) (string, []loc) {
	chars := []rune(path)
	var out strings.Builder
	var lifted []loc
	for i := 0; i < len(chars); {
		if chars[i] == '[' {
			var next, after rune
			if i+1 < len(chars) {
				next = chars[i+1]
			}
			if i+2 < len(chars) {
				after = chars[i+2]
			}
			var replacement *loc
			closeAt := 0
			switch {
			case next == '?' && after != '(':
				if end, ok := closingBracket(chars, i); ok {
					replacement, closeAt = &loc{kind: locFilter, s: string(chars[i+2 : end-1])}, end
				}
			case next == '?' || next == '(':
				if end, ok := closingBracket(chars, i); ok {
					out.WriteString(string(chars[i:end]))
					i = end
					continue
				}
			default:
				if names, end, ok := quotedNames(chars, i+1); ok {
					replacement, closeAt = &loc{kind: locNames, names: names}, end
				}
			}
			if replacement != nil {
				out.WriteString("[" + string(placeholderOpen) + strconv.Itoa(len(lifted)) + string(placeholderClose) + "]")
				lifted = append(lifted, *replacement)
				i = closeAt
				continue
			}
		}
		out.WriteRune(chars[i])
		i++
	}
	return out.String(), lifted
}

func quotedNames(chars []rune, i int) ([]string, int, bool) {
	blank := func() {
		for i < len(chars) && isBlank(chars[i]) {
			i++
		}
	}
	var names []string
	for {
		blank()
		name, end, ok := quotedString(chars, i)
		if !ok {
			return nil, 0, false
		}
		names = append(names, name)
		i = end
		blank()
		if i >= len(chars) {
			return nil, 0, false
		}
		switch chars[i] {
		case ']':
			return names, i + 1, true
		case ',':
			i++
		default:
			return nil, 0, false
		}
	}
}
