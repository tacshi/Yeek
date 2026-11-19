package query

import "strings"

type Schema struct {
	Fields   []string
	FreeText FreeText
}

type FreeText int

const (
	FreeTextSubstring FreeText = iota
	FreeTextFuzzy
)

func NewSchema(fields []string, freeText FreeText) Schema {
	return Schema{Fields: fields, FreeText: freeText}
}

func (s Schema) resolve(path []string) (string, bool) {
	joined := strings.Join(path, ".")
	for _, name := range s.Fields {
		if asciiEqualFold(name, joined) {
			return strings.ToLower(name), true
		}
	}
	return "", false
}

func asciiLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if asciiLower(a[i]) != asciiLower(b[i]) {
			return false
		}
	}
	return true
}
