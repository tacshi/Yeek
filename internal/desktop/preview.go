package desktop

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/third_party/pdfjs"
)

// pdfViewer is Yeek's port of Yaak's PdfViewer to a page of its own.
//
//go:embed preview
var pdfViewer embed.FS

// previewScheme serves the pages of response previews: Yaak's web page,
// audio, video and PDF viewers. yeek-preview://<token>/ is a page holding
// the body, which yeek-preview://<token>/body serves. A token is random and
// names one preview while it shows, so a page cannot reach other bodies.
const previewScheme = "yeek-preview"

// previewItem is a body a preview shows.
type previewItem struct {
	// kind is "html", "audio", "video" or "pdf".
	kind string
	// mimeType is the Content-Type the body is served with.
	mimeType string
	// baseURL is where an HTML page's relative URLs resolve, as Yaak's
	// <base href> does: the response's URL.
	baseURL string
	// color is the theme's text color, for the PDF viewer's messages.
	color string
	open  func() (io.ReadSeekCloser, error)
}

var previews = struct {
	sync.Mutex
	items map[string]*previewItem
}{items: map[string]*previewItem{}}

// RegisterPreviewScheme serves response previews to web views. Call it
// before opening windows.
func RegisterPreviewScheme() error {
	return mygo.Protocol.Handle(previewScheme, http.HandlerFunc(servePreview))
}

func registerPreview(item *previewItem) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	previews.Lock()
	previews.items[token] = item
	previews.Unlock()
	return token
}

func unregisterPreview(token string) {
	previews.Lock()
	delete(previews.items, token)
	previews.Unlock()
}

// previewShell is the page of each kind of preview. An HTML response shows
// in a frame sandboxed as Yaak's is, so its scripts run in an opaque origin
// with no way to the page around it or to Go; the others play in the
// elements Yaak uses. The page itself runs no script.
var previewShell = map[string]string{
	"html":  `<iframe title="Yeek response preview" src="body" sandbox="allow-scripts allow-forms" referrerpolicy="no-referrer"></iframe>`,
	"audio": `<audio controls src="body"></audio>`,
	"video": `<video controls src="body"></video>`,
	// Yaak's PdfViewer is PDF.js, which runs in a sandboxed frame too:
	// its links open in a new window, which Yeek opens in the browser.
	"pdf": `<iframe title="PDF preview" src="pdf.html%s" sandbox="allow-scripts allow-popups"></iframe>`,
}

const previewPage = `<!doctype html><html><head><meta charset="utf-8">` +
	`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; frame-src ` + previewScheme + `:; media-src ` + previewScheme + `:; style-src 'unsafe-inline'">` +
	`<style>html,body{margin:0;height:100%;background:transparent}iframe{display:block;border:0;width:100%;height:100%}audio,video{display:block;width:100%}</style>` +
	`</head><body>%s</body></html>`

var headTag = regexp.MustCompile(`(?i)<head>`)

func servePreview(w http.ResponseWriter, r *http.Request) {
	previews.Lock()
	item := previews.items[r.URL.Host]
	previews.Unlock()
	if item == nil || r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "", "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		shell := previewShell[item.kind]
		if item.kind == "pdf" {
			query := ""
			if item.color != "" {
				query = "?color=" + url.QueryEscape(item.color)
			}
			shell = fmt.Sprintf(shell, html.EscapeString(query))
		}
		_, _ = io.WriteString(w, strings.Replace(previewPage, "%s", shell, 1))
	case "/body":
		// The PDF viewer's frame has an opaque origin, so it reads the body
		// across origins.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		body, err := item.open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		defer func() { _ = body.Close() }()
		w.Header().Set("Content-Type", item.mimeType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		var content io.ReadSeeker = body
		if item.kind == "html" && item.baseURL != "" {
			// Yaak's WebPageViewer: relative URLs resolve against the
			// response's URL.
			data, err := io.ReadAll(body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if bytes.Contains(data, []byte("<head>")) {
				data = headTag.ReplaceAll(data, []byte(`<head><base href="`+html.EscapeString(item.baseURL)+`"/>`))
			}
			content = bytes.NewReader(data)
		}
		// Ranges let a media element seek.
		http.ServeContent(w, r, "", time.Time{}, content)
	default:
		if item.kind != "pdf" || !servePDFViewer(w, r) {
			http.NotFound(w, r)
		}
	}
}

// pdfWorker is PDF.js's worker after the polyfills, which it needs in its
// own globals (Yaak's pdfWorker.ts). It exports the worker's handler too,
// for PDF.js to run it on the page where a frame of an opaque origin
// can't start a worker.
const pdfWorker = `import "./polyfills.js";
export * from "./build/pdf.worker.min.mjs";
`

// servePDFViewer serves the PDF viewer's page and PDF.js, reporting whether
// the path is one of theirs. The viewer's frame loads them across origins.
func servePDFViewer(w http.ResponseWriter, r *http.Request) bool {
	var data []byte
	var err error
	name := r.URL.Path
	switch {
	case name == "/pdf.html" || name == "/pdf.js":
		data, err = pdfViewer.ReadFile("preview" + name)
	case name == "/pdfjs/polyfills.js":
		data, err = pdfViewer.ReadFile("preview/pdf_polyfills.js")
	case name == "/pdfjs/worker.mjs":
		data = []byte(pdfWorker)
	case strings.HasPrefix(name, "/pdfjs/"):
		data, err = fs.ReadFile(pdfjs.FS, path.Clean(strings.TrimPrefix(name, "/pdfjs/")))
	default:
		return false
	}
	if err != nil {
		return false
	}
	kind := mime.TypeByExtension(path.Ext(name))
	switch path.Ext(name) {
	case ".js", ".mjs":
		kind = "text/javascript; charset=utf-8"
	case "":
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", cmp.Or(kind, "application/octet-stream"))
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(data)
	return true
}

type readSeekNopCloser struct{ io.ReadSeeker }

func (readSeekNopCloser) Close() error { return nil }

func bytesPreview(data []byte) func() (io.ReadSeekCloser, error) {
	return func() (io.ReadSeekCloser, error) { return readSeekNopCloser{bytes.NewReader(data)}, nil }
}

// responsePreview is a window's web view for previews, made the first time
// one shows. It shows one preview at a time: the Response tab's body.
type responsePreview struct {
	view ui.NativeWebView
	load func(url string)
	zoom func(float64)
	err  error
	key  string
	// token names the item it shows in previews.
	token string
	scale float64
}

// newPreviewView makes the web view previews show in.
func (a *App) newPreviewView() (ui.NativeWebView, func(string), func(float64), error) {
	if a.Window == nil {
		return nil, nil, nil, errNoWindow
	}
	view, err := a.Window.NewWebView(mygo.WebViewOptions{Transparent: true})
	if err != nil {
		return nil, nil, nil, err
	}
	page := view.Page()
	// A response opens no windows of Yeek's and gets no camera, microphone or
	// other permission, as in Yaak's sandboxed frame.
	page.SetWindowOpenHandler(func(request mygo.WindowOpenRequest) *mygo.WindowOptions {
		// A PDF's links open in the browser, as Yaak's open outside it.
		if u, err := url.Parse(request.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
			_ = mygo.Shell.OpenExternal(request.URL)
		}
		return nil
	})
	page.SetPermissionHandler(func(mygo.PermissionRequest) bool { return false })
	load := func(url string) {
		if err := page.LoadURL(url); err != nil {
			a.Window.Update(func() { a.errorMessage = err.Error() })
		}
	}
	return view, load, page.SetZoomFactor, nil
}

var errNoWindow = errors.New("previews need a window")

// webPreview shows item in the window's web view, loading it when key
// changes. A framed preview has Yaak's rounded border, as a web page does.
func (a *App) webPreview(c *ui.Context, p colors, key string, item *previewItem, framed bool) {
	if a.preview == nil {
		pv := &responsePreview{}
		open := a.openPreview
		if open == nil {
			open = a.newPreviewView
		}
		pv.view, pv.load, pv.zoom, pv.err = open()
		a.preview = pv
	}
	pv := a.preview
	if pv.err != nil {
		banner(c, p, "The preview can't be shown: "+pv.err.Error(), func() {})
		return
	}
	if scale := n(a.settings, "interfaceScale"); scale > 0 && scale != pv.scale && pv.zoom != nil {
		// The page follows the interface's zoom.
		pv.scale = scale
		pv.zoom(scale)
	}
	if pv.key != key {
		if pv.token != "" {
			unregisterPreview(pv.token)
		}
		pv.key, pv.token = key, registerPreview(item)
		pv.load(previewScheme + "://" + pv.token + "/")
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(0, 0, 12, 0).Children(func() {
		view := ui.WebView(c, pv.view).Grow(1).MinHeight(0)
		if framed {
			view.Radius(8).Border(1, p.border)
		}
	})
}

// closePreview forgets the window's preview as it closes.
func (a *App) closePreview() {
	if a.preview != nil && a.preview.token != "" {
		unregisterPreview(a.preview.token)
		a.preview.token = ""
	}
}
