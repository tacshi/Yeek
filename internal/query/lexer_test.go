package query

import (
	"slices"
	"testing"
)

type tokSummary struct {
	kind tokKind
	text string
}

func kinds(input string) []tokSummary {
	var out []tokSummary
	for _, t := range lex(input) {
		out = append(out, tokSummary{t.kind, t.text})
	}
	return out
}

func TestNumbers(t *testing.T) {
	n, ok := parseNumber("500ms")
	if !ok || n.Value != 500 || n.Unit != "ms" {
		t.Fatalf("500ms: %+v %v", n, ok)
	}
	if n, ok := parseNumber("-2.5"); !ok || n.Value != -2.5 {
		t.Fatalf("-2.5: %+v %v", n, ok)
	}
	if n, ok := parseNumber("400"); !ok || n.Unit != "" {
		t.Fatalf("400: %+v %v", n, ok)
	}
	for _, raw := range []string{"", "ms", "1.", "1.5.2", "12a3", "-"} {
		if _, ok := parseNumber(raw); ok {
			t.Errorf("%q parsed as a number", raw)
		}
	}
}

func TestWordsAndOperators(t *testing.T) {
	got := kinds("a OR b AND or")
	want := []tokSummary{{tokWord, "a"}, {tokOr, ""}, {tokWord, "b"}, {tokAnd, ""}, {tokWord, "or"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestWordsStopAtParensAndQuotes(t *testing.T) {
	got := kinds("(a)b\"c d\"")
	want := []tokSummary{{tokLParen, ""}, {tokWord, "a"}, {tokRParen, ""}, {tokWord, "b"}, {tokPhrase, "c d"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestQuotedEscapes(t *testing.T) {
	cases := []struct {
		input string
		text  string
		end   int
	}{
		{`"a \"b\" \\ \n"`, `a "b" \ \n`, 15},
		{`"open`, "open", 5},
		{`"trailing\`, `trailing\`, 10},
	}
	for _, c := range cases {
		text, end := lexQuoted(c.input, 0)
		if text != c.text || end != c.end {
			t.Errorf("lexQuoted(%q) = %q, %d; want %q, %d", c.input, text, end, c.text, c.end)
		}
	}
}

func TestSpansAreBytes(t *testing.T) {
	toks := lex("é @url:ü")
	if toks[0].span != NewSpan(0, 2) {
		t.Errorf("first span %v", toks[0].span)
	}
	if toks[1].span != NewSpan(3, 10) {
		t.Errorf("second span %v", toks[1].span)
	}
}

func TestRangeNeedsItsWholeShape(t *testing.T) {
	for _, input := range []string{"[1 TO 2]", "[ 1  TO  2 ]"} {
		if _, _, _, ok := lexRange(input, 0); !ok {
			t.Errorf("%q is not a range", input)
		}
	}
	for _, input := range []string{"[1 to 2]", "[1TO 2]", "[1 TO 2", "[1 TO 2]x", "[a TO b]", "[ base_url ]"} {
		if _, _, _, ok := lexRange(input, 0); ok {
			t.Errorf("%q is a range", input)
		}
	}
}
