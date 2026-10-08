package desktop

import (
	"bytes"
	"encoding/csv"
	"encoding/json/jsontext"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"golang.org/x/net/html"
	"yeek/internal/engine"
)

func responseMIME(response engine.Object) string {
	kind, _ := responseContentType(response)
	return kind
}

// responseContentType is the media type of a response and its parameters, such as a boundary.
func responseContentType(response engine.Object) (string, map[string]string) {
	for _, header := range oslice(response, "headers") {
		if strings.EqualFold(s(header, "name"), "Content-Type") {
			kind, params, err := mime.ParseMediaType(s(header, "value"))
			if err == nil {
				return kind, params
			}
		}
	}
	return "", nil
}

var (
	csvMIME   = regexp.MustCompile(`(?i)csv|tab-separated`)
	audioMIME = regexp.MustCompile(`(?i)^audio`)
	videoMIME = regexp.MustCompile(`(?i)^video`)
	pdfMIME   = regexp.MustCompile(`(?i)pdf`)
)

// responseBody is Yaak's Response tab: the viewer is chosen from the
// response's state and content type, in Yaak's order. Raw mode shows text.
func (a *App) responseBody(c *ui.Context, p colors, d *Draft, response engine.Object) {
	pretty := d.ResponseMode != "Raw"
	id := s(response, "id")
	raw := a.bodies[id]
	kind, params := responseContentType(response)
	switch {
	case s(response, "state") == "initialized":
		ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Spinner(c).Size(14, 14).TextColor(p.subtle)
				ui.Text(c, "Sending Request").FontSize(13).TextColor(p.muted)
			})
			if ui.Button(c, "Cancel").FontSize(12).Clicked() {
				a.Engine.Cancel(d.ID)
			}
		})
	case s(response, "state") == "closed" && n(response, "contentLength") == 0 && raw == "":
		emptyState(c, p, "Empty")
	case strings.HasPrefix(kind, "text/event-stream") && pretty:
		a.eventStreamView(c, p, d, raw)
	case strings.HasPrefix(kind, "image/"):
		a.imageView(c, p, id, kind, []byte(raw))
	case audioMIME.MatchString(kind), videoMIME.MatchString(kind), pdfMIME.MatchString(kind):
		a.externalViewer(c, p, response, kind)
	case strings.HasPrefix(kind, "multipart/") && pretty:
		a.multipartView(c, p, d, id, []byte(raw), params["boundary"])
	case csvMIME.MatchString(kind) && pretty:
		csvView(c, p, raw, strings.Contains(strings.ToLower(kind), "tab-separated"))
	case strings.Contains(kind, "html") && pretty:
		htmlView(c, p, raw)
	case !utf8.ValidString(raw) || strings.ContainsRune(raw, 0):
		a.binaryView(c, p, response, kind)
	default:
		a.responseText(c, p, d, response, pretty)
	}
}

func emptyState(c *ui.Context, p colors, text string) {
	ui.Column(c).Grow(1).Center().Children(func() { ui.Text(c, text).FontSize(13).TextColor(p.subtle) })
}

// imageView shows an image, decoded once per key.
func (a *App) imageView(c *ui.Context, p colors, key, kind string, data []byte) {
	if a.images == nil {
		a.images = map[string]ui.ImageSource{}
	}
	source := a.images[key]
	if source == nil {
		var err error
		if strings.HasPrefix(kind, "image/svg") {
			source, err = ui.ParseSVG(data)
		} else {
			source, err = ui.DecodeBitmap(data)
		}
		if err != nil {
			ui.Text(c, err.Error()).Padding(15).TextColor(p.red)
			return
		}
		a.images[key] = source
	}
	ui.Box(c).Grow(1).MinHeight(0).Padding(0, 0, 8, 0).Children(func() {
		ui.Image(c, source).Label("Response preview").Fill().Fit(ui.Contain)
	})
}

// csvView is Yaak's CsvViewer: the first row as the header, as many columns as the widest row.
func csvView(c *ui.Context, p colors, text string, tabs bool) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	if tabs {
		reader.Comma = '\t'
	}
	rows, err := reader.ReadAll()
	if err != nil {
		ui.Text(c, err.Error()).Padding(15).TextColor(p.red)
		return
	}
	rows = slicesDeleteEmpty(rows)
	if len(rows) == 0 {
		emptyState(c, p, "Empty")
		return
	}
	width := 0
	for _, row := range rows {
		width = max(width, len(row))
	}
	columns := make([]ui.TableColumn, width)
	for i := range columns {
		title := ""
		if i < len(rows[0]) {
			title = rows[0][i]
		}
		columns[i] = ui.TableColumn{Title: title, Width: 180}
	}
	ui.Table(c, nil, columns, len(rows)-1, func(row, col int) {
		if col < len(rows[row+1]) {
			ui.Text(c, rows[row+1][col]).FontSize(12).Selectable()
		}
	}).Grow(1)
}

// slicesDeleteEmpty drops blank lines, as Papa Parse's skipEmptyLines does.
func slicesDeleteEmpty(rows [][]string) [][]string {
	result := rows[:0]
	for _, row := range rows {
		if len(row) == 1 && strings.TrimSpace(row[0]) == "" {
			continue
		}
		result = append(result, row)
	}
	return result
}

// htmlView renders an HTML page's text natively. Yaak shows the page in a web
// view; a MyGo window cannot hold one beside native UI, so this keeps the
// page's headings, emphasis and code without its layout or scripts.
func htmlView(c *ui.Context, p colors, body string) {
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		ui.Text(c, err.Error()).TextColor(p.red)
		return
	}
	ui.Scroll(c).Grow(1).Padding(18).Gap(10).Children(func() {
		count := 0
		var visit func(*html.Node)
		visit = func(node *html.Node) {
			count++
			if count > 10000 {
				return
			}
			if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "head") {
				return
			}
			if node.Type == html.TextNode {
				value := strings.TrimSpace(node.Data)
				if value != "" {
					text := ui.Text(c, value).Selectable()
					if node.Parent != nil {
						switch node.Parent.Data {
						case "h1":
							text.FontSize(28).Bold()
						case "h2":
							text.FontSize(22).Bold()
						case "h3":
							text.FontSize(18).Bold()
						case "b", "strong":
							text.Bold()
						case "i", "em":
							text.Italic()
						case "code", "pre":
							text.Font("monospace").FontSize(12)
						}
					}
				}
				return
			}
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				visit(child)
			}
		}
		visit(root)
	})
}

// binaryView is Yaak's BinaryViewer for a body that is not text.
func (a *App) binaryView(c *ui.Context, p colors, response engine.Object, kind string) {
	if kind == "" {
		kind = "unknown"
	}
	banner(c, p, "Content type "+kind+" cannot be previewed", func() {
		if ui.Button(c, "Save to File").FontSize(12).Clicked() {
			a.saveResponse(response)
		}
	})
}

// externalViewer stands in for Yaak's audio, video and PDF viewers, which
// play the body in a web view: Yeek offers to open it in the system's viewer.
func (a *App) externalViewer(c *ui.Context, p colors, response engine.Object, kind string) {
	what := "PDF documents"
	switch {
	case audioMIME.MatchString(kind):
		what = "Audio"
	case videoMIME.MatchString(kind):
		what = "Video"
	}
	if s(response, "state") != "closed" {
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Size(14, 14).TextColor(p.subtle) })
		return
	}
	banner(c, p, what+" can't be shown inside Yeek. Open it in the default app or save it.", func() {
		if ui.PrimaryButton(c, "Open").FontSize(12).Clicked() {
			a.openResponseExternally(response, kind)
		}
		if ui.Button(c, "Save to File").FontSize(12).Clicked() {
			a.saveResponse(response)
		}
	})
}

func banner(c *ui.Context, p colors, message string, actions func()) {
	ui.Column(c).Margin(8, 0).Padding(12, 14).Gap(12).Radius(6).Border(1, p.accent.Alpha(.35)).Background(p.accent.Alpha(.08)).Children(func() {
		ui.Text(c, message).FontSize(13).MaxLines(3)
		ui.Row(c).Gap(8).Children(actions)
	})
}

// openResponseExternally writes the whole body to a temporary file named for
// its type and opens it with the system's default app.
func (a *App) openResponseExternally(response engine.Object, kind string) {
	id := s(response, "id")
	ext := ".bin"
	if exts, _ := mime.ExtensionsByType(kind); len(exts) > 0 {
		ext = exts[len(exts)-1]
	}
	a.background(func() (func(), error) {
		dir, err := os.MkdirTemp("", "yeek-preview-")
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "response"+ext)
		if err = a.Engine.SaveBody(id, path); err != nil {
			return nil, err
		}
		return nil, mygo.Shell.OpenPath(path)
	})
}

// multipartPart is one part of a multipart response.
type multipartPart struct {
	name, filename, kind string
	header               textproto.MIMEHeader
	data                 []byte
}

func parseMultipart(body []byte, boundary string) ([]multipartPart, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := []multipartPart{}
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			return parts, nil
		}
		if err != nil {
			return parts, err
		}
		data, err := io.ReadAll(io.LimitReader(part, 10<<20))
		if err != nil {
			return parts, err
		}
		kind, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		parts = append(parts, multipartPart{name: part.FormName(), filename: part.FileName(), kind: kind, header: part.Header, data: data})
	}
}

// multipartView is Yaak's MultipartViewer: the parts listed on the left, the
// chosen one shown by its own content type on the right.
func (a *App) multipartView(c *ui.Context, p colors, d *Draft, id string, body []byte, boundary string) {
	parts, err := parseMultipart(body, boundary)
	if err != nil && len(parts) == 0 {
		banner(c, p, "Failed to parse multipart data: "+err.Error(), func() {})
		return
	}
	if len(parts) == 0 {
		banner(c, p, "No multipart parts found", func() {})
		return
	}
	d.PartIndex = max(0, min(d.PartIndex, len(parts)-1))
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Width(180).Shrink(0).Padding(4, 8, 4, 0).Gap(2).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
			for i, part := range parts {
				label := part.name
				if label == "" {
					label = "Part " + string(rune('1'+i))
				}
				if navItem(c, p, label, i == d.PartIndex).Key(i).Clicked() {
					d.PartIndex = i
				}
			}
		})
		part := parts[d.PartIndex]
		ui.Column(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 12).Children(func() {
			text := string(part.data)
			switch {
			case strings.HasPrefix(part.kind, "image/"):
				a.imageView(c, p, id+":part:"+part.name+":"+string(rune('0'+d.PartIndex)), part.kind, part.data)
			case audioMIME.MatchString(part.kind), videoMIME.MatchString(part.kind), pdfMIME.MatchString(part.kind):
				banner(c, p, part.kind+" parts can't be shown inside Yeek.", func() {})
			case csvMIME.MatchString(part.kind):
				csvView(c, p, text, strings.Contains(part.kind, "tab-separated"))
			case strings.Contains(part.kind, "html"):
				htmlView(c, p, text)
			case !utf8.Valid(part.data):
				banner(c, p, "Content type "+part.kind+" cannot be previewed", func() {})
			default:
				language := ""
				if strings.Contains(part.kind, "json") || jsontext.Value(part.data).IsValid() {
					language = "json"
					if pretty := jsontext.Value(slicesClone(part.data)); pretty.Indent(jsontext.WithIndent("  ")) == nil {
						text = string(pretty)
					}
				}
				a.codeView(c, p, text, language, "Multipart part")
			}
		})
	})
}

func slicesClone(b []byte) []byte { return append([]byte(nil), b...) }

// eventStreamView is Yaak's EventStreamViewer: the events listed as they
// arrived, with the chosen one's data below.
func (a *App) eventStreamView(c *ui.Context, p colors, d *Draft, raw string) {
	events := engine.ParseSSE(raw)
	if len(events) == 0 {
		emptyState(c, p, "No events")
		return
	}
	d.EventIndex = max(0, min(d.EventIndex, len(events)-1))
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Height(220).Shrink(0).Padding(4, 12, 4, 0).Gap(1).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			for i, event := range events {
				row := ui.ButtonBase(c).Key(i).Label("Event "+event.ID).FillWidth().Height(28).Padding(0, 8).Gap(8).Radius(4).Justify(ui.Start)
				if i == d.EventIndex {
					row.Background(p.border.Alpha(.55))
				} else if row.Hovered() {
					row.Background(p.border.Alpha(.3))
				}
				row.Children(func() {
					icon(c, "download").FontSize(13).TextColor(p.blue)
					if event.Event != "" && event.Event != "message" {
						ui.Text(c, event.Event).Font("monospace").FontSize(11).TextColor(p.accent).Shrink(0)
					}
					ui.Text(c, strings.ReplaceAll(event.Data, "\n", " ")).FontSize(12).SingleLine().Grow(1).MinWidth(0)
					if event.ID != "" {
						ui.Text(c, event.ID).Font("monospace").FontSize(11).TextColor(p.subtle).Shrink(0)
					}
				})
				if row.Clicked() {
					d.EventIndex = i
				}
			}
		})
		event := events[d.EventIndex]
		data, language := event.Data, ""
		if pretty := jsontext.Value(event.Data); pretty.Indent(jsontext.WithIndent("  ")) == nil {
			data, language = string(pretty), "json"
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(8, 0, 0, 0).Children(func() { a.codeView(c, p, data, language, "Event data") })
	})
}
