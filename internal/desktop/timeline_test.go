package desktop

import (
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestResponseTimeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new?x=1", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	a, e := cookieApp(t)
	m, _ := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Moved", "url": server.URL + "/old", "settingRequestTimeout": engine.Object{"enabled": true, "value": 1500}})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	r, err := e.SendHTTP(t.Context(), s(m, "id"), engine.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := e.Store.List(t.Context(), "", a.workspace)
	for _, x := range all {
		a.applyModel(x)
	}
	a.loadResponseBody(r)
	d := a.drafts[a.active]
	d.ResponseTab = 4
	tt := ui.NewTester(a.View, 1360, 860)
	for _, text := range []string{"redirects = true", "timeout = 1.5s (http_request)", "GET /old", "Redirecting 302", "GET /new?x=1", "HTTP/1.1 200 OK"} {
		found := false
		for _, have := range tt.Texts() {
			if strings.Contains(have, text) {
				found = true
			}
		}
		if !found {
			t.Fatalf("timeline missing %q: %v", text, tt.Texts())
		}
	}
	if err := tt.Click("Request GET /new?x=1"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Request", "URL", "Method", "Path", "/new", "Query", "x=1"} {
		if !tt.HasText(text) {
			t.Fatalf("details missing %q: %v", text, tt.Texts())
		}
	}
	if path := os.Getenv("YEEK_TIMELINE"); path != "" {
		f, _ := os.Create(path) // #nosec G304 G703 -- the test runner explicitly chooses the snapshot destination.
		_ = png.Encode(f, tt.Image())
		_ = f.Close()
	}
	if err := tt.Click("Timeline"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Timeline (Text)"); err != nil {
		t.Fatal(err)
	}
	if !d.TimelineText || !tt.HasText("Timeline text") {
		t.Fatal("text mode not shown")
	}
}

func TestTimelineTextFormat(t *testing.T) {
	for _, tc := range []struct {
		event engine.Object
		want  string
	}{
		{engine.Object{"type": "send_url", "method": "GET", "path": "/a", "query": "b=1"}, "> GET /a?b=1"},
		{engine.Object{"type": "receive_url", "version": "HTTP/2", "status": "200 OK"}, "< HTTP/2 200 OK"},
		{engine.Object{"type": "header_up", "name": "Accept", "value": "*/*"}, "> Accept: */*"},
		{engine.Object{"type": "setting", "name": "redirects", "value": "true"}, "* Setting redirects=true"},
		{engine.Object{"type": "chunk_received", "bytes": float64(2048)}, "* [2.0 KB received]"},
		{engine.Object{"type": "redirect", "status": float64(301), "url": "/x", "behavior": "drop_body", "dropped_body": true, "dropped_headers": []any{"Authorization"}}, "* Redirect 301 -> /x (drop body, body dropped, headers dropped: Authorization)"},
	} {
		if got := timelineText(tc.event, true); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}
