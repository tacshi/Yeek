package desktop

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

func responseMIME(response engine.Object) string {
	kind, _ := responseContentType(response)
	return kind
}

func responseHeader(response engine.Object, name string) string {
	for _, header := range oslice(response, "headers") {
		if strings.EqualFold(s(header, "name"), name) {
			return s(header, "value")
		}
	}
	return ""
}

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
				ui.Text(c, locales.SendingRequest()).FontSize(13).TextColor(p.muted)
			})
			if ui.Button(c, locales.Cancel()).FontSize(12).Clicked() {
				a.Engine.Cancel(d.ID)
			}
		})
	case s(response, "state") == "closed" && n(response, "contentLength") == 0 && raw == "":
		emptyState(c, p, locales.Empty())
	case strings.HasPrefix(kind, "text/event-stream") && pretty:
		a.eventStreamView(c, p, d, raw)
	case strings.HasPrefix(kind, "image/"):
		a.imageView(c, p, id, kind, []byte(raw))
	case audioMIME.MatchString(kind), videoMIME.MatchString(kind), pdfMIME.MatchString(kind):
		a.mediaView(c, p, response, kind)
	case strings.HasPrefix(kind, "multipart/") && pretty:
		a.multipartView(c, p, d, id, []byte(raw), params["boundary"])
	case csvMIME.MatchString(kind) && pretty:
		csvView(c, p, raw, strings.Contains(strings.ToLower(kind), "tab-separated"))
	case strings.Contains(kind, "html") && pretty:
		a.webPageView(c, p, response)
	case !utf8.ValidString(raw) || strings.ContainsRune(raw, 0):
		a.binaryView(c, p, response, kind)
	default:
		a.responseText(c, p, d, response, pretty)
	}
}

func emptyState(c *ui.Context, p colors, text string) {
	ui.Column(c).Grow(1).Center().Children(func() { ui.Text(c, text).FontSize(13).TextColor(p.subtle) })
}

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
		ui.Image(c, source).Label(locales.ResponsePreview()).Fill().Fit(ui.Contain)
	})
}

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
		emptyState(c, p, locales.Empty())
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

func (a *App) binaryView(c *ui.Context, p colors, response engine.Object, kind string) {
	if kind == "" {
		kind = "unknown"
	}
	banner(c, p, locales.ContentTypeContentTypeCannotBePreviewed(kind), func() {
		if ui.Button(c, locales.SaveToFile()).FontSize(12).Clicked() {
			a.saveResponse(response)
		}
	})
}

func (a *App) webPageView(c *ui.Context, p colors, response engine.Object) {
	id := s(response, "id")
	kind := cmp.Or(responseHeader(response, "Content-Type"), "text/html")
	a.webPreview(c, p, id+":html:"+s(response, "state"), &previewItem{kind: "html", mimeType: kind, baseURL: s(response, "url"), open: a.openBody(id)}, true)
}

func (a *App) mediaView(c *ui.Context, p colors, response engine.Object, kind string) {
	if s(response, "state") != "closed" {
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Size(14, 14).TextColor(p.subtle) })
		return
	}
	id := s(response, "id")
	a.webPreview(c, p, id+":"+mediaKind(kind), &previewItem{kind: mediaKind(kind), mimeType: kind, color: cssColor(p.text), open: a.openBody(id)}, false)
}

func mediaKind(kind string) string {
	switch {
	case audioMIME.MatchString(kind):
		return "audio"
	case videoMIME.MatchString(kind):
		return "video"
	}
	return "pdf"
}

func cssColor(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func (a *App) openBody(id string) func() (io.ReadSeekCloser, error) {
	return func() (io.ReadSeekCloser, error) { return a.Engine.OpenBody(id) }
}

func banner(c *ui.Context, p colors, message string, actions func()) {
	ui.Column(c).Margin(8, 0).Padding(12, 14).Gap(12).Radius(6).Border(1, p.accent.Alpha(.35)).Background(p.accent.Alpha(.08)).Children(func() {
		ui.Text(c, message).FontSize(13).MaxLines(3)
		ui.Row(c).Gap(8).Children(actions)
	})
}

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

func (a *App) multipartView(c *ui.Context, p colors, d *Draft, id string, body []byte, boundary string) {
	parts, err := parseMultipart(body, boundary)
	if err != nil && len(parts) == 0 {
		banner(c, p, locales.FailedToParseMultipartDataError(err.Error()), func() {})
		return
	}
	if len(parts) == 0 {
		banner(c, p, locales.NoMultipartPartsFound(), func() {})
		return
	}
	d.PartIndex = max(0, min(d.PartIndex, len(parts)-1))
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Width(180).Shrink(0).Padding(4, 8, 4, 0).Gap(2).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
			for i, part := range parts {
				label := part.name
				if label == "" {
					label = locales.PartIndex(string(rune('1' + i)))
				}
				if navItem(c, p, label, i == d.PartIndex).Key(i).Clicked() {
					d.PartIndex = i
				}
			}
		})
		part := parts[d.PartIndex]
		ui.Column(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 12).Children(func() {
			text := string(part.data)
			partKey := id + ":part:" + strconv.Itoa(d.PartIndex)
			switch {
			case strings.HasPrefix(part.kind, "image/"):
				a.imageView(c, p, id+":part:"+part.name+":"+string(rune('0'+d.PartIndex)), part.kind, part.data)
			case audioMIME.MatchString(part.kind), videoMIME.MatchString(part.kind), pdfMIME.MatchString(part.kind):
				a.webPreview(c, p, partKey, &previewItem{kind: mediaKind(part.kind), mimeType: part.kind, color: cssColor(p.text), open: bytesPreview(part.data)}, false)
			case csvMIME.MatchString(part.kind):
				csvView(c, p, text, strings.Contains(part.kind, "tab-separated"))
			case strings.Contains(part.kind, "html"):
				a.webPreview(c, p, partKey, &previewItem{kind: "html", mimeType: cmp.Or(part.header.Get("Content-Type"), "text/html"), open: bytesPreview(part.data)}, true)
			case !utf8.Valid(part.data):
				banner(c, p, locales.ContentTypeContentTypeCannotBePreviewed(part.kind), func() {})
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

func (a *App) eventStreamView(c *ui.Context, p colors, d *Draft, raw string) {
	events := engine.ParseSSE(raw)
	if len(events) == 0 {
		emptyState(c, p, locales.NoEvents())
		return
	}
	d.EventIndex = max(0, min(d.EventIndex, len(events)-1))
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Scroll(c).Height(220).Shrink(0).Padding(4, 12, 4, 0).Gap(1).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			for i, event := range events {
				row := ui.ButtonBase(c).Key(i).Label(locales.EventId(event.ID)).FillWidth().Height(28).Padding(0, 8).Gap(8).Radius(4).Justify(ui.Start)
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
