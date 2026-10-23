package filter

import (
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

// Every case in testdata/jsonpath_plus.json, which Yaak's
// crates/yaak-filter/tests/fixtures/generate-jsonpath-plus.mjs records from
// jsonpath-plus 11.0.0 as the removed plugin ran it.

// deviations are where this engine means to differ from jsonpath-plus; the
// fixture holds the result each such case should give instead.
var deviations = []string{
	// ["a.b"] and ['a.b'] are literal keys, never operators or expressions.
	"literal-key",
	// RFC 9535 [-1], which jsonpath-plus looks up as the key "-1".
	"rfc-negative-index",
	// RFC 9535 [::-1], which jsonpath-plus does not read as a slice.
	"rfc-negative-step",
	// RFC 9535 [?@.a > 1], which jsonpath-plus looks up as a key.
	"rfc-filter",
	// RFC 9535 length(), count(), match(), search() and value() in ?(...).
	"rfc-function",
	// A call outside the allowlist, or several statements: an error, not JavaScript.
	"unsupported",
	// Filters are compiled up front, so a bad one fails even when no node reaches it.
	"eager-parse",
	// jsonpath-plus returns undefined for a root of 0, "", false or null.
	"falsy-root",
	// Indexing into a string can split a surrogate pair, which becomes U+FFFD here.
	"lone-surrogate",
}

func TestMatchesJSONPathPlusExceptNamedDeviations(t *testing.T) {
	data, err := os.ReadFile("testdata/jsonpath_plus.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Docs  map[string]string `json:"docs"`
		Cases []struct {
			Doc       string `json:"doc"`
			Path      string `json:"path"`
			Deviation string `json:"deviation"`
			Expected  struct {
				Content *string `json:"content"`
				Error   bool    `json:"error"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 300 {
		t.Fatalf("%d cases", len(fixture.Cases))
	}
	var failures []string
	perDeviation := map[string]int{}
	for _, c := range fixture.Cases {
		label := c.Doc + " " + c.Path
		if c.Deviation != "" {
			if !slices.Contains(deviations, c.Deviation) {
				t.Fatalf("unknown deviation %s for %s", c.Deviation, c.Path)
			}
			perDeviation[c.Deviation]++
			label += " [" + c.Deviation + "]"
		}
		content, err := FilterJSONPath(fixture.Docs[c.Doc], c.Path)
		var ok bool
		if c.Expected.Error {
			ok = content == "" && err != nil && strings.HasPrefix(err.Error(), "Invalid filter: ")
		} else {
			ok = err == nil && c.Expected.Content != nil && content == *c.Expected.Content
		}
		if !ok {
			want := "an error"
			if c.Expected.Content != nil {
				want = *c.Expected.Content
			}
			failures = append(failures, label+"\n  expected: "+want+"\n  got: "+content+" "+errorText(err))
		}
	}
	for _, d := range deviations {
		if perDeviation[d] == 0 {
			t.Errorf("no case covers %s", d)
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d cases differ:\n%s", len(failures), len(fixture.Cases), strings.Join(failures, "\n"))
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
