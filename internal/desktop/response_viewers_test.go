package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func viewerApp(t *testing.T, contentType, body string, extra engine.Object) (*App, *Draft, *ui.Tester) {
	t.Helper()
	a, e := cookieApp(t)
	m, _ := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Viewer"})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	d := a.drafts[a.active]
	response := engine.Object{"model": "http_response", "id": "rs_viewer", "requestId": d.ID, "state": "closed", "status": float64(200), "contentLength": float64(len(body)), "headers": []any{engine.Object{"name": "Content-Type", "value": contentType}}}
	for k, v := range extra {
		response[k] = v
	}
	a.applyModel(response)
	a.bodies["rs_viewer"] = body
	return a, d, ui.NewTester(a.View, 1360, 860)
}

func snapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("YEEK_VIEWERS"); dir != "" {
		f, _ := os.Create(dir + "/" + name + ".png") // #nosec G304 G703 -- the test runner explicitly chooses the snapshot directory.
		_ = png.Encode(f, tt.Image())
		_ = f.Close()
	}
}

func TestResponseViewerChoice(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		extra                   engine.Object
		want, absent            []string
	}{
		{"sending", "application/json", "", engine.Object{"state": "initialized"}, []string{"Sending Request", "Cancel"}, nil},
		{"empty", "application/json", "", nil, []string{"Empty"}, nil},
		{"events", "text/event-stream", "id: 1\nevent: update\ndata: {\"n\":1}\n\nid: 2\ndata: second\n\n", nil, []string{"update", "second", "Event data"}, nil},
		{"csv", "text/csv", "name,age\nAda,36\n\nLinus,54\n", nil, []string{"name", "age", "Ada", "Linus"}, nil},
		{"tsv", "text/tab-separated-values", "name\tage\nAda\t36\n", nil, []string{"name", "Ada", "36"}, nil},
		{"multipart", "multipart/form-data; boundary=XX", "--XX\r\nContent-Disposition: form-data; name=\"meta\"\r\nContent-Type: application/json\r\n\r\n{\"a\":1}\r\n--XX\r\nContent-Disposition: form-data; name=\"notes\"\r\n\r\nhello\r\n--XX--\r\n", nil, []string{"meta", "notes", "Multipart part"}, nil},
		{"audio", "audio/mpeg", "ID3\x03fake", nil, []string{"Audio can't be shown inside Yeek. Open it in the default app or save it.", "Open", "Save to File"}, nil},
		{"pdf", "application/pdf", "%PDF-1.4", nil, []string{"PDF documents can't be shown inside Yeek. Open it in the default app or save it."}, nil},
		{"binary", "application/octet-stream", "\x00\x01\x02\xff", nil, []string{"Content type application/octet-stream cannot be previewed", "Save to File"}, nil},
		{"json", "application/json", `{"b":1,"a":2}`, nil, []string{"Response body"}, []string{"Content type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, tt := viewerApp(t, tc.contentType, tc.body, tc.extra)
			tt.Frame()
			for _, text := range tc.want {
				if !tt.HasText(text) {
					t.Fatalf("missing %q: %v", text, tt.Texts())
				}
			}
			for _, text := range tc.absent {
				if tt.HasText(text) {
					t.Fatalf("unexpected %q", text)
				}
			}
			snapshot(t, tt, tc.name)
		})
	}
}

func TestResponseRawModeAndMenu(t *testing.T) {
	_, d, tt := viewerApp(t, "text/csv", "name,age\nAda,36\n", nil)
	if err := tt.Click("Response"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !contains(menu, "Response") || !contains(menu, "Response (Raw)") || !contains(menu, "Save to File") || !contains(menu, "Copy Body") || contains(menu, "Hex") {
		t.Fatalf("menu = %v", menu)
	}
	if err := tt.ChooseMenuItem("Response (Raw)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.ResponseMode != "Raw" || !tt.HasText("Response body") {
		t.Fatalf("raw mode did not show text: %v", tt.Texts())
	}
	_, _, tt = viewerApp(t, "image/png", "not really a png", nil)
	if err := tt.Click("Response"); err != nil {
		t.Fatal(err)
	}
	if contains(tt.Menu(), "Response (Raw)") {
		t.Fatal("raw offered for an image")
	}
}

func TestMultipartPartSelection(t *testing.T) {
	_, d, tt := viewerApp(t, "multipart/mixed; boundary=B", "--B\r\nContent-Disposition: form-data; name=\"first\"\r\n\r\none\r\n--B\r\nContent-Disposition: form-data; name=\"second\"\r\nContent-Type: text/csv\r\n\r\ncol\nvalue\r\n--B--\r\n", nil)
	if err := tt.Click("second"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if d.PartIndex != 1 || !tt.HasText("col") || !tt.HasText("value") {
		t.Fatalf("second part not shown: %v", tt.Texts())
	}
	snapshot(t, tt, "multipart-second")
}

func TestResponseHeadersTab(t *testing.T) {
	a, d, tt := viewerApp(t, "application/json", `{}`, engine.Object{
		"url": "https://api.example.com/users", "remoteAddr": "93.184.216.34:443", "version": "HTTP/2", "createdAt": "2026-10-08T19:54:14.386321000",
		"requestHeaders": []any{engine.Object{"name": "User-Agent", "value": "Yeek"}, engine.Object{"name": "accept", "value": "*/*"}},
		"headers":        []any{engine.Object{"name": "Server", "value": "fixture"}, engine.Object{"name": "content-type", "value": "application/json"}},
	})
	d.ResponseTab = 2
	tt.Frame()
	// Response headers start open, request headers and info closed, as in Yaak.
	if !tt.HasText("Info") || !tt.HasText("Request Headers") || !tt.HasText("Response Headers") || !tt.HasText("fixture") || tt.HasText("Yeek") || tt.HasText("93.184.216.34:443") {
		t.Fatalf("initial sections: %v", tt.Texts())
	}
	texts := tt.Texts()
	if indexOf(texts, "content-type") > indexOf(texts, "Server") {
		t.Fatal("response headers are not sorted by name")
	}
	if err := tt.Click(d.ID + ".general"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(d.ID + ".request_headers"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Sent", "Request URL", "https://api.example.com/users", "Remote Address", "93.184.216.34:443", "HTTP/2", "User-Agent", "Yeek"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	if err := tt.Click("Open in browser"); err != nil {
		t.Fatal(err)
	}
	if opened := tt.OpenedURLs(); len(opened) != 1 || opened[0] != "https://api.example.com/users" {
		t.Fatalf("opened %v", opened)
	}
	snapshot(t, tt, "headers")
	d.ResponseTab = 1
	tt.Frame()
	if !tt.HasText("No request body") || tt.HasText("Request Headers") {
		t.Fatalf("request tab: %v", tt.Texts())
	}
	_ = a
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}
