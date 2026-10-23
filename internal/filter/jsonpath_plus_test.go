package filter

import (
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

var deviations = []string{
	"literal-key",
	"rfc-negative-index",
	"rfc-negative-step",
	"rfc-filter",
	"rfc-function",
	"unsupported",
	"eager-parse",
	"falsy-root",
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
