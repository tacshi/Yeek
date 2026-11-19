package query

import (
	"strings"
	"unicode"
)

func FormatField(field, value string, negated bool) string {
	sign := ""
	if negated {
		sign = "-"
	}
	return sign + "@" + field + ":" + FormatValue(value)
}

func FormatValue(value string) string {
	if isBareValue(value) {
		return value
	}
	return Quote(value)
}

func Quote(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := range len(value) {
		if value[i] == '"' || value[i] == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

func isBareValue(value string) bool {
	if value == "" || value[0] == '"' || value[0] == '[' {
		return false
	}
	if strings.ContainsFunc(value, func(c rune) bool { return unicode.IsSpace(c) || c == ')' }) {
		return false
	}
	return classifyBare(value).Kind == ValueText
}

func IsValidPath(name string) bool {
	if name == "" {
		return false
	}
	for part := range strings.SplitSeq(name, ".") {
		if part == "" || strings.ContainsFunc(part, func(c rune) bool { return !isIdentChar(c) }) {
			return false
		}
	}
	return true
}
