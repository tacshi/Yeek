package query

import (
	"strings"
	"testing"
)

func fuzzy(target, search string) bool {
	return FuzzyMatch(strings.ToLower(target), strings.ToLower(strings.TrimSpace(search)))
}

func checkFuzzy(t *testing.T, want bool, cases [][2]string) {
	t.Helper()
	for _, c := range cases {
		if got := fuzzy(c[0], c[1]); got != want {
			t.Errorf("fuzzy(%q, %q) = %v, want %v", c[0], c[1], got, want)
		}
	}
}

func TestFuzzySubstringFirst(t *testing.T) {
	checkFuzzy(t, true, [][2]string{
		{"My Request", "req"},
		{"My Request", "quest"},
		{"/v1/charges", "rges"},
		{"abc", "abc"},
	})
}

func TestFuzzyWordPrefixes(t *testing.T) {
	checkFuzzy(t, true, [][2]string{
		{"My Request", "mr"},
		{"My Request", "myreq"},
		{"United States of America", "usam"},
		{"api.github.com", "apigi"},
		{"POST Create User", "postcu"},
		{"get_user_by_id", "gubi"},
	})
	checkFuzzy(t, false, [][2]string{
		{"api.github.com", "apigh"},
	})
}

func TestFuzzyNotAnySubsequence(t *testing.T) {
	checkFuzzy(t, false, [][2]string{
		{"My Request", "rq"},
		{"request", "rqst"},
		{"My Request", "mx"},
		{"abc", "x"},
	})
}

func TestFuzzySpacesDoNotBreakARun(t *testing.T) {
	checkFuzzy(t, true, [][2]string{
		{"My Request", "my r"},
		{"foo bar", "f b"},
	})
}

func TestFuzzyEmptyAndQuoted(t *testing.T) {
	checkFuzzy(t, false, [][2]string{
		{"", "a"},
		{"los angeles", `"la"`},
	})
	checkFuzzy(t, true, [][2]string{
		{"abc", ""},
		{"las vegas", `"la`},
		{"los angeles", "la"},
	})
}

func TestFuzzyUnicodeIsAlphanumeric(t *testing.T) {
	checkFuzzy(t, true, [][2]string{
		{"Über Café", "üc"},
		{"日本 語", "日語"},
	})
}
