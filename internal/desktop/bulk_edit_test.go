package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// The cases of Yaak's BulkPairEditor.test.ts.
func TestParseBulkPairLines(t *testing.T) {
	for _, tc := range []struct {
		line, name, value string
		enabled, dropped  bool
	}{
		{line: "foo: bar", name: "foo", value: "bar", enabled: true},
		{line: "foo:bar", name: "foo:bar", enabled: true},
		{line: "not a pair", name: "not a pair", enabled: true},
		{line: `foo: bar\nbaz`, name: "foo", value: "bar\nbaz", enabled: true},
		{line: "# foo: bar", name: "foo", value: "bar"},
		{line: "#   foo: bar", name: "foo", value: "bar"},
		{line: "#\tfoo: bar", name: "foo", value: "bar"},
		{line: "#foo: bar", name: "#foo", value: "bar", enabled: true},
		{line: "# token:", name: "token"},
		{line: "# token: ", name: "token"},
		{line: "# just a comment", dropped: true},
		{line: "# see http://example.com", dropped: true},
		{line: "#", dropped: true},
		{line: "color: #fff", name: "color", value: "#fff", enabled: true},
	} {
		rows := parseBulkPairs(tc.line)
		if tc.dropped {
			if len(rows) != 0 {
				t.Errorf("%q: kept %+v", tc.line, rows)
			}
			continue
		}
		if len(rows) != 1 || rows[0].Name != tc.name || rows[0].Value != tc.value || rows[0].Enabled != tc.enabled {
			t.Errorf("%q: got %+v", tc.line, rows)
		}
	}
}

func TestBulkPairsRoundTrip(t *testing.T) {
	rows := []KV{{Name: "Accept", Value: "application/json", Enabled: true}, {Name: "X-Off", Value: "1", Enabled: false}, {Name: "Multi", Value: "a\nb", Enabled: true}, {Name: "", Value: "", Enabled: true}}
	text := formatBulkPairs(rows)
	if text != "Accept: application/json\n# X-Off: 1\nMulti: a\\nb" {
		t.Fatalf("format = %q", text)
	}
	back := parseBulkPairs(text)
	if formatBulkPairs(back) != text || len(back) != 3 || back[1].Enabled || back[2].Value != "a\nb" {
		t.Fatalf("round trip = %+v", back)
	}
}

func TestBulkEditToggleKeepsRows(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	d.Parameters = []KV{{ID: "p1", Name: "q", Value: "1", Enabled: true}}
	d.Tab = 1
	tt.Move(500, 200)
	tt.Frame()
	if err := tt.Click("Enable bulk edit"); err != nil {
		t.Fatal(err)
	}
	if !a.bulkEdit["url_parameters"] {
		t.Fatal("bulk mode not on")
	}
	tt.Frame()
	if !tt.HasText("Enable form edit") || tt.HasText("Parameter 1") {
		t.Fatalf("bulk editor not shown: %v", tt.Texts())
	}
	if err := tt.Click("Bulk edit Parameter"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("q: 1\n# page: 2\nlimit: 50")
	if len(d.Parameters) != 3 || d.Parameters[1].Enabled || d.Parameters[2].Name != "limit" || !d.Dirty {
		t.Fatalf("parsed = %+v", d.Parameters)
	}
	if err := tt.Click("Enable form edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Parameter 3") {
		t.Fatalf("rows not shown after bulk edit: %v", tt.Texts())
	}
}
