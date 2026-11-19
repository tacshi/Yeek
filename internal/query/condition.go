package query

import "strings"

type ConditionKind int

const (
	CondContains ConditionKind = iota
	CondWildcard
	CondPresent
	CondCompare
	CondRange
	CondNever
)

type Condition struct {
	Kind  ConditionKind
	Text  string
	Op    CompareOp
	Value float64
	Low   float64
	High  float64
}

func NewCondition(value FieldValue) Condition {
	switch value.Kind {
	case ValueText:
		if value.Text == "" {
			return Condition{Kind: CondNever}
		}
		return Condition{Kind: CondContains, Text: strings.ToLower(value.Text)}
	case ValueWildcard:
		return Condition{Kind: CondWildcard, Text: strings.ToLower(value.Pattern)}
	case ValueExists:
		return Condition{Kind: CondPresent}
	case ValueCompare:
		if value.Number.Unit == "" {
			return Condition{Kind: CondCompare, Op: value.Op, Value: value.Number.Value}
		}
	case ValueRange:
		if value.Low.Unit == "" && value.High.Unit == "" {
			return Condition{Kind: CondRange, Low: value.Low.Value, High: value.High.Value}
		}
	}
	return Condition{Kind: CondNever}
}

func (c Condition) MatchesText(text string) bool {
	switch c.Kind {
	case CondContains:
		return ContainsIgnoreCase(text, c.Text)
	case CondWildcard:
		return WildcardMatch(c.Text, strings.ToLower(text))
	case CondPresent:
		return text != ""
	}
	n, ok := ReadNumber(text)
	if !ok {
		return false
	}
	switch c.Kind {
	case CondCompare:
		return c.Op.Test(n, c.Value)
	case CondRange:
		return n >= c.Low && n <= c.High
	}
	return false
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func ContainsIgnoreCase(hay, needle string) bool {
	if !isASCII(hay) || !isASCII(needle) {
		return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
	}
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		if asciiEqualFold(hay[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func WildcardMatch(pattern, text string) bool {
	parts := strings.Split(pattern, "*")
	rest, ok := strings.CutPrefix(text, parts[0])
	if !ok {
		return false
	}
	parts = parts[1:]
	if len(parts) == 0 {
		return rest == ""
	}
	last := parts[len(parts)-1]
	for _, part := range parts[:len(parts)-1] {
		i := strings.Index(rest, part)
		if i < 0 {
			return false
		}
		rest = rest[i+len(part):]
	}
	return strings.HasSuffix(rest, last)
}

func ReadNumber(text string) (float64, bool) {
	return parseFloat(strings.TrimSpace(text))
}
