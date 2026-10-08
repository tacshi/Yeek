package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
)

func TestHeaderNameAndValueCompletions(t *testing.T) {
	a, _, ids := treeApp(t)
	a.openRequest(ids["Alpha"])
	a.drafts[ids["Alpha"]].Tab = 2
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Header 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Co")
	if tt.HasText("Complete Content-Type") {
		t.Fatal("completed before three characters")
	}
	tt.Type("n")
	tt.Frame()
	if !tt.HasText("Complete Content-Type") || !tt.HasText("constant") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Complete Content-Type"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	d := a.drafts[ids["Alpha"]]
	if d.Headers[0].Name != "Content-Type" {
		t.Fatal(d.Headers)
	}
	if err := tt.Click("Header value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("json")
	tt.Frame()
	if err := tt.Click("Complete application/json"); err != nil {
		t.Fatal(tt.Texts())
	}
	tt.Frame()
	if d.Headers[0].Value != "application/json" {
		t.Fatal(d.Headers)
	}
}

func TestSecretHeaderValuesAreMaskedAndNamesValidated(t *testing.T) {
	if !secretHeader("Authorization") || !secretHeader("X-API-Key") || !secretHeader("cookie") || secretHeader("Accept") {
		t.Fatal("secret header detection")
	}
	if !validHeaderName("X-${[ name ]}-Id") || validHeaderName("Bad Header") || !validHeaderName("") {
		t.Fatal("header name validation")
	}
	a, _, ids := treeApp(t)
	a.openRequest(ids["Alpha"])
	d := a.drafts[ids["Alpha"]]
	d.Tab = 2
	d.Headers = []KV{{ID: "h1", Name: "Authorization", Value: "Bearer secret-token", Enabled: true}}
	tt := ui.NewTester(a.View, 1360, 860)
	if tt.HasText("Bearer secret-token") || !tt.HasText("Show Header value") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Show Header value"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Obscure Header value") || !a.revealed["h1"] {
		t.Fatal(tt.Texts())
	}
}
