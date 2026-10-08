package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestInfoTabPreviewsMarkdownAndEditsName(t *testing.T) {
	a, e := cookieApp(t)
	m, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Exact historical reference", "url": "http://127.0.0.1:1",
		"description": "Expect **NA489/154** directly in `data`.\n\n- Inspect the title fields\n- Check [meta](https://example.com/meta)\n\n[local](file:///etc/passwd)"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	d := a.drafts[a.active]
	d.Tab = 5
	tt := ui.NewTester(a.View, 1360, 860)
	for _, text := range []string{"NA489/154", "data", "Inspect the title fields", "meta"} {
		if !tt.HasText(text) {
			t.Fatalf("preview missing %q: %v", text, tt.Texts())
		}
	}
	if tt.HasText("**NA489/154**") || d.DescriptionMode != "preview" {
		t.Fatal("description not rendered as Markdown")
	}
	tt.Move(500, 300)
	if err := tt.Click("Edit mode"); err != nil {
		t.Fatal(err)
	}
	if d.DescriptionMode != "edit" {
		t.Fatal("did not switch to editing")
	}
	if err := tt.Click("Name"); err != nil {
		t.Fatal(err)
	}
	tt.Type(" v2")
	a.saveActive()
	saved, _ := e.Store.Get(t.Context(), d.ID)
	if s(saved, "name") != "Exact historical reference v2" {
		t.Fatal(saved["name"])
	}
}

func TestMarkdownLinksOnlyOpenWebAddresses(t *testing.T) {
	view := func(c *ui.Context) {
		markdownView(c, colors{}, "[web](https://example.com) [file](file:///etc/passwd) [js](javascript:alert(1))")
	}
	tt := ui.NewTester(view, 600, 200)
	if !tt.HasText("web") || !tt.HasText("file") || !tt.HasText("js") {
		t.Fatal(tt.Texts())
	}
}
