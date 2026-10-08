package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestPasteCurlIntoURLOverwritesRequest(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	id, name := d.ID, s(a.models[d.ID], "name")
	if err := tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.SetClipboard(`curl -X PUT https://api.test/items/7 -H 'X-Token: abc' --data '{"a":1}'`)
	tt.Command("paste")
	tt.Frame()
	d = a.drafts[id]
	saved := a.models[id]
	if d.Method != "PUT" || d.URL != "https://api.test/items/7" || s(saved, "name") != name || s(saved, "id") != id {
		t.Fatalf("request = %s %s (%s)", d.Method, d.URL, s(saved, "name"))
	}
	if len(d.Headers) == 0 || d.Headers[0].Name != "X-Token" {
		t.Fatalf("headers = %+v", d.Headers)
	}
	if !tt.HasText("Updated request from Curl") {
		t.Fatal("no toast")
	}
	tt.Key(0, ui.KeyEscape)
	if len(a.toasts) != 0 {
		t.Fatal("Escape did not dismiss the toast")
	}
}

func TestPasteURLWithQueryMovesParams(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	if err := tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.SetClipboard("https://api.test/search?q=yeek&page=2&q=again")
	tt.Command("paste")
	tt.Frame()
	if d.URL != "https://api.test/search" || d.Tab != 1 {
		t.Fatalf("url = %s tab %d", d.URL, d.Tab)
	}
	got := []string{}
	for _, row := range d.Parameters {
		if row.Name != "" {
			got = append(got, row.Name+"="+row.Value)
		}
	}
	if len(got) != 3 || got[0] != "q=yeek" || got[1] != "page=2" || got[2] != "q=again" {
		t.Fatalf("params = %v", got)
	}
	// A paste into part of the URL is just text.
	tt.SetClipboard("?x=1")
	tt.Command("paste")
	tt.Frame()
	if d.URL != "https://api.test/search?x=1" {
		t.Fatalf("partial paste changed to %s", d.URL)
	}
	_ = a
}

func TestToastsHideAfterTimeout(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	a.changeBodyType(d, "application/json")
	tt.Frame()
	if !tt.HasText("Request method switched to POST") {
		t.Fatal("method toast missing")
	}
	a.toasts[0].shown = a.toasts[0].shown.Add(-6e9)
	tt.Frame()
	if len(a.toasts) != 0 {
		t.Fatal("toast did not hide after five seconds")
	}
	a.toast("first")
	a.showToast("same", "one", "info", 0)
	a.showToast("same", "two", "info", 0)
	tt.Frame()
	if len(a.toasts) != 2 || tt.HasText("one") || !tt.HasText("two") {
		t.Fatalf("toasts = %+v", a.toasts)
	}
	if err := tt.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	if len(a.toasts) != 1 {
		t.Fatal("dismiss did not remove a toast")
	}
}
