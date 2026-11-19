package query

type Span struct {
	Start int
	End   int
}

func NewSpan(start, end int) Span {
	return Span{Start: start, End: end}
}

func (s Span) Slice(source string) string {
	return source[s.Start:s.End]
}

func (s Span) to(other Span) Span {
	return Span{Start: min(s.Start, other.Start), End: max(s.End, other.End)}
}

type NodeKind int

const (
	NodeAnd NodeKind = iota
	NodeOr
	NodeNot
	NodeTerm
	NodePhrase
	NodeField
)

func (k NodeKind) String() string {
	switch k {
	case NodeAnd:
		return "And"
	case NodeOr:
		return "Or"
	case NodeNot:
		return "Not"
	case NodeTerm:
		return "Term"
	case NodePhrase:
		return "Phrase"
	case NodeField:
		return "Field"
	}
	return "Unknown"
}

type Node struct {
	Kind      NodeKind
	Span      Span
	Children  []*Node
	Operand   *Node
	Text      string
	Path      []string
	Value     FieldValue
	PathSpan  Span
	ValueSpan Span
}

type ValueKind int

const (
	ValueText ValueKind = iota
	ValueWildcard
	ValueExists
	ValueCompare
	ValueRange
)

type FieldValue struct {
	Kind    ValueKind
	Text    string
	Quoted  bool
	Pattern string
	Op      CompareOp
	Number  NumberLiteral
	Low     NumberLiteral
	High    NumberLiteral
}

func TextValue(text string, quoted bool) FieldValue {
	return FieldValue{Kind: ValueText, Text: text, Quoted: quoted}
}

type CompareOp int

const (
	Gt CompareOp = iota
	Gte
	Lt
	Lte
)

func (op CompareOp) String() string {
	switch op {
	case Gt:
		return ">"
	case Gte:
		return ">="
	case Lt:
		return "<"
	case Lte:
		return "<="
	}
	return ""
}

func (op CompareOp) Test(left, right float64) bool {
	switch op {
	case Gt:
		return left > right
	case Gte:
		return left >= right
	case Lt:
		return left < right
	case Lte:
		return left <= right
	}
	return false
}

type NumberLiteral struct {
	Value float64
	Unit  string
	Raw   string
}
