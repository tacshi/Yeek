package desktop

import (
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
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
	if err := os.WriteFile(filepath.Join(e.DataDir, "bodies", "rs_viewer"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	fakePreview(a)
	return a, d, ui.NewTester(a.View, 1360, 860)
}

type previewRecorder struct {
	view *mygo.WebView
	urls []string
	zoom float64
}

func fakePreview(a *App) *previewRecorder {
	r := &previewRecorder{view: new(mygo.WebView)}
	a.preview = nil
	a.openPreview = func() (ui.NativeWebView, func(string), func(float64), error) {
		return r.view, func(url string) { r.urls = append(r.urls, url) }, func(z float64) { r.zoom = z }, nil
	}
	return r
}

func fetchPreview(t *testing.T, url string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	servePreview(w, req)
	return w
}

func settledImage(tt *ui.Tester) *image.RGBA {
	tt.Advance(500 * time.Millisecond)
	return tt.Image()
}

func snapshot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("YEEK_VIEWERS"); dir != "" {
		f, _ := os.Create(dir + "/" + name + ".png") // #nosec G304 G703 -- the test runner explicitly chooses the snapshot directory.
		_ = png.Encode(f, settledImage(tt))
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

func TestResponseWebPreviews(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, element string
		framed                           bool
	}{
		{"html", "text/html; charset=utf-8", "<html><head><title>T</title></head><body><a href=\"/x\">x</a></body></html>", `sandbox="allow-scripts allow-forms"`, true},
		{"audio", "audio/mpeg", "ID3\x03fake", "<audio controls", false},
		{"video", "video/mp4", "\x00\x00\x00\x18ftypmp42", "<video controls", false},
		{"pdf", "application/pdf", "%PDF-1.4", `sandbox="allow-scripts allow-popups"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _, tt := viewerApp(t, tc.contentType, tc.body, engine.Object{"url": "https://example.com/docs/page"})
			r := fakePreview(a)
			tt.Frame()
			if len(r.urls) != 1 || !strings.HasPrefix(r.urls[0], "yeek-preview://") {
				t.Fatalf("loaded %v", r.urls)
			}
			if tt.WebViewAt(1100, 500) != r.view {
				t.Fatal("web view not shown in the response pane")
			}
			if tt.HasText("Content type") {
				t.Fatalf("binary banner shown: %v", tt.Texts())
			}
			page := fetchPreview(t, r.urls[0])
			src := `src="body"`
			if tc.name == "pdf" {
				src = `src="pdf.html?color=%23`
			}
			if page.Code != 200 || !strings.Contains(page.Body.String(), tc.element) || !strings.Contains(page.Body.String(), src) {
				t.Fatalf("page %d: %s", page.Code, page.Body)
			}
			body := fetchPreview(t, r.urls[0]+"body")
			if body.Code != 200 || body.Header().Get("Content-Type") != tc.contentType {
				t.Fatalf("body %d %q", body.Code, body.Header().Get("Content-Type"))
			}
			want := tc.body
			if tc.name == "html" {
				want = strings.Replace(tc.body, "<head>", `<head><base href="https://example.com/docs/page"/>`, 1)
			}
			if body.Body.String() != want {
				t.Fatalf("body = %q", body.Body)
			}
			tt.Frame()
			if len(r.urls) != 1 {
				t.Fatalf("reloaded: %v", r.urls)
			}
			snapshot(t, tt, "preview-"+tc.name)
		})
	}
}

func TestResponsePreviewRawAndRanges(t *testing.T) {
	_, d, tt := viewerApp(t, "text/html", "<p>hi</p>", nil)
	d.ResponseMode = "Raw"
	tt.Frame()
	if tt.WebViewAt(1100, 500) != nil || !tt.HasText("Response body") {
		t.Fatal("raw mode should show the HTML as text")
	}

	a, _, tt := viewerApp(t, "video/mp4", "0123456789", nil)
	r := fakePreview(a)
	tt.Frame()
	part := fetchPreview(t, r.urls[0]+"body", "Range", "bytes=2-5")
	if part.Code != http.StatusPartialContent || part.Body.String() != "2345" {
		t.Fatalf("range: %d %q", part.Code, part.Body)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(r.urls[0], "yeek-preview://"), "/")
	if fetchPreview(t, "yeek-preview://"+strings.Repeat("0", len(token))+"/body").Code != http.StatusNotFound {
		t.Fatal("unknown token served")
	}
	a.Close()
	if fetchPreview(t, r.urls[0]+"body").Code != http.StatusNotFound {
		t.Fatal("preview served after its window closed")
	}
}

func TestResponsePreviewWaitsAndFollowsZoom(t *testing.T) {
	a, _, tt := viewerApp(t, "audio/mpeg", "ID3", engine.Object{"state": "connected"})
	r := fakePreview(a)
	tt.Frame()
	if len(r.urls) != 0 {
		t.Fatal("audio loaded before the response finished")
	}
	a.settings["interfaceScale"] = 1.2
	a.applyModel(engine.Object{"model": "http_response", "id": "rs_viewer", "requestId": a.active, "state": "closed", "status": float64(200), "contentLength": float64(3), "headers": []any{engine.Object{"name": "Content-Type", "value": "audio/mpeg"}}})
	tt.Frame()
	if len(r.urls) != 1 || r.zoom != 1.2 {
		t.Fatalf("urls %v zoom %v", r.urls, r.zoom)
	}
}

func TestMultipartPartPreview(t *testing.T) {
	a, d, tt := viewerApp(t, "multipart/mixed; boundary=B", "--B\r\nContent-Disposition: form-data; name=\"page\"\r\nContent-Type: text/html\r\n\r\n<b>part</b>\r\n--B\r\nContent-Disposition: form-data; name=\"clip\"\r\nContent-Type: audio/ogg\r\n\r\nOggS\r\n--B--\r\n", nil)
	r := fakePreview(a)
	tt.Frame()
	if len(r.urls) != 1 || fetchPreview(t, r.urls[0]+"body").Body.String() != "<b>part</b>" {
		t.Fatalf("html part: %v", r.urls)
	}
	d.PartIndex = 1
	tt.Frame()
	if len(r.urls) != 2 || !strings.Contains(fetchPreview(t, r.urls[1]).Body.String(), "<audio") {
		t.Fatalf("audio part: %v", r.urls)
	}
	if body := fetchPreview(t, r.urls[1]+"body"); body.Body.String() != "OggS" || body.Header().Get("Content-Type") != "audio/ogg" {
		t.Fatalf("audio part body %q %q", body.Body, body.Header().Get("Content-Type"))
	}
	if fetchPreview(t, r.urls[0]+"body").Code != http.StatusNotFound {
		t.Fatal("the previous part's preview still served")
	}
}

func TestPDFPreviewServesPDFJS(t *testing.T) {
	a, _, tt := viewerApp(t, "application/pdf", "%PDF-1.4", nil)
	r := fakePreview(a)
	tt.Frame()
	base := r.urls[0]
	for _, tc := range []struct{ path, kind, contains string }{
		{"pdf.html", "text/html", `src="pdf.js"`},
		{"pdf.js", "text/javascript", `"./pdfjs/build/pdf.min.mjs"`},
		{"pdfjs/polyfills.js", "text/javascript", "getOrInsertComputed"},
		{"pdfjs/worker.mjs", "text/javascript", `export * from "./build/pdf.worker.min.mjs"`},
		{"pdfjs/build/pdf.min.mjs", "text/javascript", "GlobalWorkerOptions"},
		{"pdfjs/build/pdf.worker.min.mjs", "text/javascript", ""},
		{"pdfjs/cmaps/Adobe-Japan1-UCS2.bcmap", "application/octet-stream", ""},
		{"pdfjs/standard_fonts/FoxitSerif.pfb", "", ""},
		{"pdfjs/css/TextLayer.css", "text/css", ".textLayer"},
	} {
		w := fetchPreview(t, base+tc.path)
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.kind) || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %q", tc.path, w.Code, w.Header().Get("Content-Type"))
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s: no CORS", tc.path)
		}
	}
	if fetchPreview(t, base+"pdfjs/../preview.go").Code != http.StatusNotFound || fetchPreview(t, base+"pdfjs/nope.js").Code != http.StatusNotFound {
		t.Fatal("served a file outside PDF.js")
	}
	if fetchPreview(t, base+"body").Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("the viewer can't read the body")
	}

	b, _, tt := viewerApp(t, "audio/mpeg", "ID3", nil)
	other := fakePreview(b)
	tt.Frame()
	if fetchPreview(t, other.urls[0]+"pdf.html").Code != http.StatusNotFound {
		t.Fatal("an audio preview serves the PDF viewer")
	}
}
