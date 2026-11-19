package desktop

import (
	"cmp"
	"encoding/csv"
	"encoding/hex"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"mime"
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
			return mediaTypeOf(s(header, "value")), headerParams(s(header, "value"))
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
		a.eventStreamView(c, p, d, decodeBodyText([]byte(raw), ""))
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

type gifPreview struct {
	key    string
	player *ui.VideoPlayer
}

func (a *App) gifPlayer(key string, data []byte) *ui.VideoPlayer {
	if a.gif.key == key {
		return a.gif.player
	}
	if a.gif.player != nil {
		a.gif.player.Close()
	}
	a.gif = gifPreview{key: key}
	if decoder, err := ui.NewGIFDecoder(data); err == nil {
		if player, err := ui.NewVideoPlayer(decoder); err == nil {
			a.gif.player = player.SetLoop(true)
		}
	}
	return a.gif.player
}

func (a *App) imageView(c *ui.Context, p colors, key, kind string, data []byte) {
	if strings.HasPrefix(kind, "image/gif") {
		if player := a.gifPlayer(key, data); player != nil {
			ui.Box(c).Grow(1).MinHeight(0).Padding(0, 0, 8, 0).Children(func() {
				ui.Video(c, player).Label(locales.ResponsePreview()).Fill().Fit(ui.Contain)
			})
			return
		}
	}
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
	a.webPreview(c, p, id+":html:"+s(response, "state"), &previewItem{kind: "html", mimeType: kind, baseURL: s(response, "url"), open: a.openBody(id)})
}

func (a *App) mediaView(c *ui.Context, p colors, response engine.Object, kind string) {
	if s(response, "state") != "closed" {
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Size(14, 14).TextColor(p.subtle) })
		return
	}
	id := s(response, "id")
	a.webPreview(c, p, id+":"+mediaKind(kind), &previewItem{kind: mediaKind(kind), mimeType: kind, color: cssColor(p.text), open: a.openBody(id)})
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

func (a *App) multipartView(c *ui.Context, p colors, d *Draft, id string, body []byte, boundary string) {
	a.multipartParts(c, p, d, id, "", body, boundary, 1)
}

const maxMultipartDepth = 4

func (d *Draft) partIndex(key string) int {
	if key == "" {
		return d.PartIndex
	}
	return d.PartIndexes[key]
}

func (d *Draft) setPartIndex(key string, i int) {
	if key == "" {
		d.PartIndex = i
		return
	}
	if d.PartIndexes == nil {
		d.PartIndexes = map[string]int{}
	}
	d.PartIndexes[key] = i
}

func partLabel(part multipartPart, i int) string {
	return cmp.Or(part.name, part.filename, part.contentID, locales.PartIndex(strconv.Itoa(i+1)))
}

func toneBanner(c *ui.Context, p colors, message string, tone ui.Color) {
	ui.Column(c).Margin(0, 12, 8, 0).Padding(8, 12).Radius(6).Border(1, tone.Alpha(.35)).Background(tone.Alpha(.08)).Children(func() {
		ui.Text(c, message).FontSize(12).MaxLines(3)
	})
}

func (a *App) multipartParts(c *ui.Context, p colors, d *Draft, id, key string, body []byte, boundary string, depth int) {
	result, err := parseMultipart(body, boundary)
	if err != nil {
		banner(c, p, locales.FailedToParseMultipartDataError(err.Error()), func() {})
		return
	}
	parts := result.parts
	if len(parts) == 0 {
		banner(c, p, locales.NoMultipartPartsFound(), func() {})
		return
	}
	index := max(0, min(d.partIndex(key), len(parts)-1))
	d.setPartIndex(key, index)
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		if !result.complete {
			toneBanner(c, p, locales.TheBodyEndsWithoutItsClosing(), p.orange)
		}
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			ui.Scroll(c).Width(180).Shrink(0).Padding(4, 8, 4, 0).Gap(2).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
				for i, part := range parts {
					if navItem(c, p, partLabel(part, i), i == index).Key(i).Clicked() {
						d.setPartIndex(key, i)
					}
				}
			})
			part := parts[index]
			partKey := key + "." + strconv.Itoa(index)
			ui.Column(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 12).Children(func() {
				if part.mediaType == "application/http" {
					if message := parseHTTPMessage(part.data); message != nil {
						a.httpMessagePart(c, p, d, id, partKey, message, depth)
						return
					}
				}
				a.partBody(c, p, d, id, partKey, part.data, part.contentType, depth)
			})
		})
	})
}

func (a *App) httpMessagePart(c *ui.Context, p colors, d *Draft, id, key string, message *httpMessage, depth int) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(8).Children(func() {
		ui.Row(c).Gap(6).Padding(4, 0, 0, 0).Children(func() {
			if message.status != 0 {
				status := engine.Object{"status": float64(message.status)}
				ui.Text(c, strings.TrimSpace(strconv.Itoa(message.status)+" "+message.statusReason)).Font("monospace").FontSize(13).TextColor(statusColor(status, p)).SingleLine()
				return
			}
			ui.Text(c, message.method).Font("monospace").FontSize(13).TextColor(p.blue).SingleLine()
			ui.Text(c, message.url).Font("monospace").FontSize(13).SingleLine().Shrink(1)
		})
		a.detailsBanner(c, p, id+":part"+key+".headers", true, func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, locales.Headers()).FontSize(13)
				badgePair(len(message.headers), 0).viewSingle(c, p)
			})
		}, func() {
			if len(message.headers) == 0 {
				ui.Text(c, locales.NoHeaders()).FontSize(12).Italic().TextColor(p.subtle)
				return
			}
			rows := make([]keyValue, len(message.headers))
			for i, h := range message.headers {
				rows[i] = keyValue{label: h.name, value: h.value, color: p.accent}
			}
			keyValueRows(c, p, rows)
		})
		contentType, _ := headerValue(message.headers, "content-type")
		a.partBody(c, p, d, id, key, message.body, contentType, depth)
	})
}

func (a *App) partBody(c *ui.Context, p colors, d *Draft, id, key string, body []byte, contentType string, depth int) {
	if len(body) == 0 {
		ui.Text(c, locales.Empty()).FontSize(13).Italic().TextColor(p.subtle).Padding(4, 0)
		return
	}
	kind := mediaTypeOf(contentType)
	if strings.HasPrefix(kind, "multipart/") {
		if boundary := boundaryFromContentType(contentType); boundary != "" {
			if depth < maxMultipartDepth {
				a.multipartParts(c, p, d, id, key, body, boundary, depth+1)
				return
			}
			ui.Column(c).Grow(1).MinHeight(0).Children(func() {
				toneBanner(c, p, locales.MultipartNestedMoreThanDepthLevels(maxMultipartDepth), p.blue)
				a.leafBody(c, p, id, key, body, "", "", true)
			})
			return
		}
	}
	a.leafBody(c, p, id, key, body, contentType, kind, false)
}

func isBinaryPart(kind string, body []byte) bool {
	if textMIME.MatchString(kind) {
		return false
	}
	return isProbablyBinary(body)
}

var textMIME = regexp.MustCompile(`^text/|json|xml|javascript|yaml|csv|x-www-form-urlencoded`)

func (a *App) leafBody(c *ui.Context, p colors, id, key string, body []byte, contentType, kind string, raw bool) {
	binary := isBinaryPart(kind, body)
	text := string(body)
	partKey := id + ":part" + key
	if raw {
		if binary {
			text = hex.Dump(body)
		}
		a.codeView(c, p, text, "", "Multipart part")
		return
	}
	switch {
	case strings.HasPrefix(kind, "image/"):
		a.imageView(c, p, partKey, kind, body)
	case audioMIME.MatchString(kind), videoMIME.MatchString(kind):
		a.webPreview(c, p, partKey, &previewItem{kind: mediaKind(kind), mimeType: kind, color: cssColor(p.text), open: bytesPreview(body)})
	case csvMIME.MatchString(kind):
		csvView(c, p, text, strings.Contains(kind, "tab-separated"))
	case strings.HasPrefix(kind, "text/html"), !binary && strings.Contains(kind, "html"):
		a.webPreview(c, p, partKey, &previewItem{kind: "html", mimeType: cmp.Or(contentType, "text/html"), open: bytesPreview(body)})
	case pdfMIME.MatchString(kind):
		a.webPreview(c, p, partKey, &previewItem{kind: mediaKind(kind), mimeType: kind, color: cssColor(p.text), open: bytesPreview(body)})
	case binary:
		a.codeView(c, p, hex.Dump(body), "", "Multipart part")
	default:
		language := ""
		if strings.Contains(kind, "json") || jsontext.Value(body).IsValid() {
			language = "json"
			if pretty := jsontext.Value(slicesClone(body)); pretty.Indent(jsontext.WithIndent("  ")) == nil {
				text = string(pretty)
			}
		}
		a.codeView(c, p, text, language, "Multipart part")
	}
}

func slicesClone(b []byte) []byte { return append([]byte(nil), b...) }

func (a *App) eventStreamView(c *ui.Context, p colors, d *Draft, raw string) {
	events := engine.ParseSSE(raw)
	if len(events) == 0 {
		emptyState(c, p, locales.NoEvents())
		return
	}
	d.EventIndex = max(0, min(d.EventIndex, len(events)-1))
	box := ui.Column(c).Grow(1).MinHeight(0)
	state := localList(box)
	state.Selected = &d.EventIndex
	state.Label = func(i int) string { return locales.EventId(events[i].ID) }
	box.Children(func() {
		quietList(c, p, state, len(events), func(i int) {
			event := events[i]
			ui.Row(c).Height(28).Padding(0, 8).Gap(8).Children(func() {
				icon(c, "download").FontSize(13).TextColor(p.blue)
				if event.Event != "" && event.Event != "message" {
					ui.Text(c, event.Event).Font("monospace").FontSize(11).TextColor(p.accent).Shrink(0)
				}
				ui.Text(c, strings.ReplaceAll(event.Data, "\n", " ")).FontSize(12).SingleLine().Grow(1).MinWidth(0)
				if event.ID != "" {
					ui.Text(c, event.ID).Font("monospace").FontSize(11).TextColor(p.subtle).Shrink(0)
				}
			})
		}).Height(220).Shrink(0).Padding(4, 12, 4, 0).Gap(1).BorderWidth(0, 0, 1, 0).BorderColor(p.border)
		event := events[d.EventIndex]
		data, language := event.Data, ""
		if pretty := jsontext.Value(event.Data); pretty.Indent(jsontext.WithIndent("  ")) == nil {
			data, language = string(pretty), "json"
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(8, 0, 0, 0).Children(func() { a.codeView(c, p, data, language, "Event data") })
	})
}
