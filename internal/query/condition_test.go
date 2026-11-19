package query

import "testing"

func conditionFor(t *testing.T, input string) Condition {
	t.Helper()
	node := Parse(input)
	if node == nil || node.Kind != NodeField {
		t.Fatalf("%q is not a field", input)
	}
	return NewCondition(node.Value)
}

func TestResolvesTextValues(t *testing.T) {
	for input, want := range map[string]Condition{
		"@a:Foo":       {Kind: CondContains, Text: "foo"},
		"@a:":          {Kind: CondNever},
		"@a:F*o":       {Kind: CondWildcard, Text: "f*o"},
		"@a:*":         {Kind: CondPresent},
		"@a:>5":        {Kind: CondCompare, Op: Gt, Value: 5},
		"@a:>5s":       {Kind: CondNever},
		"@a:[1 TO 3]":  {Kind: CondRange, Low: 1, High: 3},
		"@a:[1 TO 3s]": {Kind: CondNever},
	} {
		if got := conditionFor(t, input); got != want {
			t.Errorf("%q: got %+v, want %+v", input, got, want)
		}
	}
}

func TestConditionTestsValues(t *testing.T) {
	if (Condition{Kind: CondWildcard, Text: "40*"}).MatchesText("504") {
		t.Error("40* should not match 504")
	}
	if !(Condition{Kind: CondCompare, Op: Gte, Value: 200}).MatchesText(" 200 ") {
		t.Error("\" 200 \" should be at least 200")
	}
	if !(Condition{Kind: CondRange, Low: 400, High: 499}).MatchesText("404") {
		t.Error("404 should be in [400 TO 499]")
	}
	if !(Condition{Kind: CondContains, Text: "é"}).MatchesText("CAFÉ") {
		t.Error("é should be in CAFÉ")
	}
	if (Condition{Kind: CondPresent}).MatchesText("") {
		t.Error("empty text should not be present")
	}
}

func TestReadNumbers(t *testing.T) {
	for text, want := range map[string]float64{"200": 200, " 1.5 ": 1.5, "1e3": 1000, "-0": 0, ".5": 0.5} {
		if got, ok := ReadNumber(text); !ok || got != want {
			t.Errorf("ReadNumber(%q) = %v, %v", text, got, ok)
		}
	}
	for _, text := range []string{"", "abc", "0x10", "1_000", "--1"} {
		if _, ok := ReadNumber(text); ok {
			t.Errorf("ReadNumber(%q) should fail", text)
		}
	}
}
