package query

import (
	"fmt"
	"strings"
	"testing"
)

func showNode(node *Node) string {
	switch node.Kind {
	case NodeAnd, NodeOr:
		parts := make([]string, len(node.Children))
		for i, child := range node.Children {
			parts[i] = showNode(child)
		}
		name := "and"
		if node.Kind == NodeOr {
			name = "or"
		}
		return fmt.Sprintf("(%s %s)", name, strings.Join(parts, " "))
	case NodeNot:
		return fmt.Sprintf("(not %s)", showNode(node.Operand))
	case NodeTerm:
		return node.Text
	case NodePhrase:
		return fmt.Sprintf("%q", node.Text)
	case NodeField:
		var value string
		v := node.Value
		switch v.Kind {
		case ValueText:
			value = v.Text
			if v.Quoted {
				value = fmt.Sprintf("%q", v.Text)
			}
		case ValueWildcard:
			value = "wild(" + v.Pattern + ")"
		case ValueExists:
			value = "exists"
		case ValueCompare:
			value = v.Op.String() + v.Number.Raw
		case ValueRange:
			value = "[" + v.Low.Raw + " TO " + v.High.Raw + "]"
		}
		return "@" + strings.Join(node.Path, ".") + "=" + value
	}
	return "?"
}

func show(input string) string {
	node := Parse(input)
	if node == nil {
		return "none"
	}
	return showNode(node)
}

func checkShow(t *testing.T, cases [][2]string) {
	t.Helper()
	for _, c := range cases {
		if got := show(c[0]); got != c[1] {
			t.Errorf("show(%q) = %s, want %s", c[0], got, c[1])
		}
	}
}

func TestParseEmpty(t *testing.T) {
	checkShow(t, [][2]string{
		{"", "none"},
		{"   ", "none"},
		{"OR AND", "none"},
		{"()", "none"},
		{"-", "none"},
		{")", "none"},
	})
}

func TestParseTermsAndPhrases(t *testing.T) {
	checkShow(t, [][2]string{
		{"foo", "foo"},
		{"foo bar", "(and foo bar)"},
		{`"hi there"`, `"hi there"`},
		{`"hi there`, `"hi there"`},
		{`a"b c"d`, `(and a "b c" d)`},
		{`"say \"hi\""`, `"say \"hi\""`},
	})
}

func TestParseBooleanOperators(t *testing.T) {
	checkShow(t, [][2]string{
		{"a OR b", "(or a b)"},
		{"a AND b", "(and a b)"},
		{"a b OR c", "(or (and a b) c)"},
		{"a OR b c", "(or a (and b c))"},
		{"a OR b OR c", "(or a b c)"},
		{"a or b", "(and a or b)"},
		{"a and b", "(and a and b)"},
		{"NOT a", "(and NOT a)"},
	})
}

func TestParseDanglingOperatorsAreIgnored(t *testing.T) {
	checkShow(t, [][2]string{
		{"a OR", "a"},
		{"OR a", "a"},
		{"a OR OR b", "(or a b)"},
		{"a AND", "a"},
		{"a -", "a"},
		{"- a", "a"},
		{"(a -)", "a"},
	})
}

func TestParseGroups(t *testing.T) {
	checkShow(t, [][2]string{
		{"(a OR b) -c", "(and (or a b) (not c))"},
		{"-(a OR b)", "(not (or a b))"},
		{"(a OR b", "(or a b)"},
		{"a) b", "(and a b)"},
		{"((a))", "a"},
		{"x (a (b OR c))", "(and x (and a (or b c)))"},
	})
}

func TestParseNegation(t *testing.T) {
	checkShow(t, [][2]string{
		{"-foo", "(not foo)"},
		{"--foo", "(not (not foo))"},
		{`-"a b"`, `(not "a b")`},
		{"-@type:grpc", "(not @type=grpc)"},
		{"a-b", "a-b"},
		{"-OR a", "a"},
	})
}

func TestParseFields(t *testing.T) {
	checkShow(t, [][2]string{
		{"@method:post", "@method=post"},
		{`@url:"a b"`, `@url="a b"`},
		{"@header.x-cache:HIT", "@header.x-cache=HIT"},
		{"@req.header.content-type:json", "@req.header.content-type=json"},
		{"@folder:x OR @folder:y", "(or @folder=x @folder=y)"},
		{"@url:https://a.b/c?d=e", "@url=https://a.b/c?d=e"},
	})
}

func TestParseAtIsRequired(t *testing.T) {
	checkShow(t, [][2]string{
		{"method:post", "method:post"},
		{"localhost:3000", "localhost:3000"},
		{"@method", "@method"},
		{"@", "@"},
		{"@.a:b", "@.a:b"},
		{"@a.:b", "@a.:b"},
		{"@a b:c", "(and @a b:c)"},
	})
}

func TestParseTrailingFieldHasEmptyValue(t *testing.T) {
	checkShow(t, [][2]string{
		{"@method:", "@method="},
		{"@method: post", "(and @method= post)"},
		{"(@method:)", "@method="},
	})
}

func TestParseFieldValues(t *testing.T) {
	checkShow(t, [][2]string{
		{"@url:-foo", "@url=-foo"},
		{"@url:AND", "@url=AND"},
		{"@url:a(b", "@url=a(b"},
		{"(@url:a OR @url:b)", "(or @url=a @url=b)"},
		{`@url:a"b`, `@url=a"b`},
		{`@url:"a`, `@url="a"`},
	})
}

func TestParseSpecialValues(t *testing.T) {
	checkShow(t, [][2]string{
		{"@status:*", "@status=exists"},
		{"-@status:*", "(not @status=exists)"},
		{"@url:*.github.com", "@url=wild(*.github.com)"},
		{`@url:"*.github.com"`, `@url="*.github.com"`},
		{"@status:>=400", "@status=>=400"},
		{"@duration:>500ms", "@duration=>500ms"},
		{"@status:<1", "@status=<1"},
		{"@status:[400 TO 499]", "@status=[400 TO 499]"},
		{"(@status:[400 TO 499])", "@status=[400 TO 499]"},
		{"@status:>=", "@status=>="},
		{"@status:>abc", "@status=>abc"},
		{"@status:**", "@status=wild(**)"},
		{"@status:[400", "@status=[400"},
	})
}

func TestParseTemplatesSanely(t *testing.T) {
	checkShow(t, [][2]string{
		{"@url:${[ base_url ]}", "(and @url=${[ base_url ]})"},
		{"@url:[ base_url ]", "(and @url=[ base_url ])"},
		{`@url:"${[ base_url ]}/x"`, `@url="${[ base_url ]}/x"`},
	})
}

func TestParseCompareValueDetails(t *testing.T) {
	node := Parse("@duration:<=1.5s")
	if node == nil || node.Kind != NodeField || node.Value.Kind != ValueCompare {
		t.Fatal("not a comparison")
	}
	if node.Value.Op != Lte || node.Value.Number.Value != 1.5 || node.Value.Number.Unit != "s" {
		t.Fatalf("got %+v", node.Value)
	}
}

func TestParseSpans(t *testing.T) {
	input := `-@url:"a b" OR (c`
	node := Parse(input)
	if node.Kind != NodeOr {
		t.Fatalf("not an or: %v", node.Kind)
	}
	if got := node.Span.Slice(input); got != input {
		t.Errorf("or span %q", got)
	}
	not := node.Children[0]
	if not.Kind != NodeNot {
		t.Fatalf("not a not: %v", not.Kind)
	}
	if got := not.Span.Slice(input); got != `-@url:"a b"` {
		t.Errorf("not span %q", got)
	}
	field := not.Operand
	if field.Kind != NodeField {
		t.Fatalf("not a field: %v", field.Kind)
	}
	if got := field.Span.Slice(input); got != `@url:"a b"` {
		t.Errorf("field span %q", got)
	}
	if got := field.PathSpan.Slice(input); got != "@url:" {
		t.Errorf("path span %q", got)
	}
	if got := field.ValueSpan.Slice(input); got != `"a b"` {
		t.Errorf("value span %q", got)
	}
	if got := node.Children[1].Span.Slice(input); got != "c" {
		t.Errorf("term span %q", got)
	}

	field = Parse("@method:")
	if field == nil || field.Kind != NodeField {
		t.Fatal("not a field")
	}
	if field.ValueSpan != NewSpan(8, 8) {
		t.Errorf("empty value span %v", field.ValueSpan)
	}
}

func nodeDepth(node *Node) int {
	switch node.Kind {
	case NodeAnd, NodeOr:
		deepest := 0
		for _, child := range node.Children {
			deepest = max(deepest, nodeDepth(child))
		}
		return deepest + 1
	case NodeNot:
		return nodeDepth(node.Operand) + 1
	}
	return 1
}

func TestParseNestingIsBounded(t *testing.T) {
	inputs := []string{
		strings.Repeat("(", 5000) + "a OR b" + strings.Repeat(")", 5000),
		strings.Repeat("-", 5000) + "a",
		strings.Repeat("-(", 5000) + "a" + strings.Repeat(")", 5000),
	}
	for _, input := range inputs {
		node := Parse(input)
		if node == nil {
			t.Fatalf("no node for %q", input[:10])
		}
		if d := nodeDepth(node); d > MaxDepth+2 {
			t.Errorf("too deep (%d) for %q", d, input[:10])
		}
	}
	input := strings.Repeat("(", MaxDepth) + "a" + strings.Repeat(")", MaxDepth)
	if got := show(input); got != "a" {
		t.Errorf("nested group: %s", got)
	}
	if got := show(strings.Repeat("-", 2) + "a"); got != "(not (not a))" {
		t.Errorf("double negation: %s", got)
	}
}

func TestParseNeverPanicsOnOddInput(t *testing.T) {
	for _, input := range []string{
		`"`,
		`\`,
		"@",
		"@:",
		"@a:",
		"-",
		"--",
		"(",
		")",
		"((",
		"))",
		"[",
		"@a:[",
		"@a:[1 TO",
		`@a:"`,
		`@a:"\`,
		"é@ü:ö",
		"@a.b.c:",
		"@a:>",
		"@a:<=",
		"-(",
		"OR",
		"AND OR",
		"@a:)",
		"🦀 @x:🦀 \"🦀",
		"\xff@a:\xfe \"\xff",
	} {
		_ = Parse(input)
	}
}
