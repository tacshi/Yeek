package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestHeadersTabShowsInheritedHeaders(t *testing.T) {
	a, e := cookieApp(t)
	save := func(m engine.Object) engine.Object {
		saved, err := e.Save(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		a.applyModel(saved)
		return saved
	}
	ws := a.models[a.workspace]
	ws = deepCopy(ws)
	ws["headers"] = []any{engine.Object{"name": "X-Team", "value": "core", "enabled": true}, engine.Object{"name": "Accept", "value": "application/json", "enabled": true}}
	save(ws)
	folder := save(engine.Object{"model": "folder", "workspaceId": a.workspace, "name": "API", "headers": []any{engine.Object{"name": "X-Team", "value": "search", "enabled": true}, engine.Object{"name": "X-Off", "value": "1", "enabled": false}}})
	req := save(engine.Object{"model": "http_request", "workspaceId": a.workspace, "folderId": s(folder, "id"), "name": "Owners", "headers": []any{engine.Object{"name": "x-caller-id", "value": "demo", "enabled": true}, engine.Object{"name": "user-agent", "value": "mine", "enabled": true}}})
	got := map[string]string{}
	for _, h := range a.inheritedHeaders(req) {
		got[h.Name] = h.Value
	}
	if got["X-Team"] != "search" || got["Accept"] != "application/json" || got["User-Agent"] != "Yeek" || got["X-Off"] != "" || len(got) != 3 {
		t.Fatal(got)
	}
	a.openRequest(s(req, "id"))
	a.drafts[a.active].Tab = 2
	tt := ui.NewTester(a.View, 1360, 860)
	// User-Agent is overridden by the request, so two inherited headers show.
	if !tt.HasText("Inherited") || !tt.HasText("X-Team") || !tt.HasText("search") || tt.HasText("Yeek") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Inherited headers"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("X-Team") {
		t.Fatal("inherited headers did not collapse")
	}
}
