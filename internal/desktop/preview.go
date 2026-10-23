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
	"yeek/locales"
	"yeek/third_party/pdfjs"
)

//go:embed preview
var pdfViewer embed.FS

const previewScheme = "yeek-preview"

type previewItem struct {
	kind     string
	mimeType string
	baseURL  string
	color    string
	open     func() (io.ReadSeekCloser, error)
}

var previews = struct {
	sync.Mutex
	items map[string]*previewItem
}{items: map[string]*previewItem{}}

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

var previewShell = map[string]string{
	"html":  `<iframe title="Yeek response preview" src="body" sandbox="allow-scripts allow-forms" referrerpolicy="no-referrer"></iframe>`,
	"audio": `<audio controls src="body"></audio>`,
	"video": `<video controls src="body"></video>`,
	"pdf":   `<iframe title="PDF preview" src="pdf.html%s" sandbox="allow-scripts allow-popups"></iframe>`,
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
		http.ServeContent(w, r, "", time.Time{}, content)
	default:
		if item.kind != "pdf" || !servePDFViewer(w, r) {
			http.NotFound(w, r)
		}
	}
}

const pdfWorker = `import "./polyfills.js";
export * from "./build/pdf.worker.min.mjs";
`

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

type responsePreview struct {
	view  ui.NativeWebView
	load  func(url string)
	zoom  func(float64)
	err   error
	key   string
	token string
	scale float64
}

func (a *App) newPreviewView() (ui.NativeWebView, func(string), func(float64), error) {
	if a.Window == nil {
		return nil, nil, nil, errNoWindow
	}
	view, err := a.Window.NewWebView(mygo.WebViewOptions{Transparent: true})
	if err != nil {
		return nil, nil, nil, err
	}
	page := view.Page()
	page.SetWindowOpenHandler(func(request mygo.WindowOpenRequest) *mygo.WindowOptions {
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

var errNoWindow = errors.New(locales.PreviewsNeedAWindow())

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
		banner(c, p, locales.ThePreviewCantBeShownError(pv.err.Error()), func() {})
		return
	}
	if scale := n(a.settings, "interfaceScale"); scale > 0 && scale != pv.scale && pv.zoom != nil {
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

func (a *App) closePreview() {
	if a.preview != nil && a.preview.token != "" {
		unregisterPreview(a.preview.token)
		a.preview.token = ""
	}
}
