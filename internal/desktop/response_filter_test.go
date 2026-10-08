package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestResponseFilterOpensFromBodyActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"SERVING","items":[{"id":1},{"id":2}]}`))
	}))
	defer server.Close()
	a, e := cookieApp(t)
	a.settings["appearance"] = "light"
	m, _ := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Ready", "url": server.URL})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	r, _ := e.SendHTTP(t.Context(), s(m, "id"), engine.SendOptions{})
	a.applyModel(r)
	a.loadResponseBody(r)
	tt := ui.NewTester(a.View, 1360, 860)
	tt.Move(1000, 500)
	tt.Frame()
	if !tt.HasText("Save response to file") || !tt.HasText("Copy response body") {
		t.Fatal("body actions missing on hover", tt.Texts())
	}
	if err := tt.Click("Filter response"); err != nil {
		t.Fatal(err)
	}
	tt.Type("$.items[*].id")
	tt.Frame()
	d := a.drafts[a.active]
	if !d.FilterOpen || d.FilterError != "" || d.FilteredBody == "" {
		t.Fatalf("filter open=%v error=%q body=%q", d.FilterOpen, d.FilterError, d.FilteredBody)
	}
	if err := tt.Click("Close filter"); err != nil {
		t.Fatal(err)
	}
	if d.FilterOpen {
		t.Fatal("filter did not close")
	}
}
