package desktop

import (
	"encoding/csv"
	"encoding/json/v2"
	"fmt"
	"maps"
	"mime"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"golang.org/x/net/html"
	"yeek/internal/engine"
)

func responseMIME(response engine.Object) string {
	for _, header := range oslice(response, "headers") {
		if strings.EqualFold(s(header, "name"), "Content-Type") {
			kind, _, err := mime.ParseMediaType(s(header, "value"))
			if err == nil {
				return kind
			}
		}
	}
	return ""
}
func (a *App) previewResponse(c *ui.Context, p colors, d *Draft, response engine.Object, body string) {
	kind := responseMIME(response)
	id := s(response, "id")
	if strings.HasPrefix(kind, "image/") {
		if a.images == nil {
			a.images = map[string]ui.ImageSource{}
		}
		source := a.images[id]
		if source == nil {
			var err error
			if kind == "image/svg+xml" {
				source, err = ui.ParseSVG([]byte(body))
			} else {
				source, err = ui.DecodeBitmap([]byte(body))
			}
			if err != nil {
				ui.Text(c, err.Error()).Padding(15).TextColor(p.red)
				return
			}
			a.images[id] = source
		}
		ui.Image(c, source).Label("Response image").Grow(1).FillWidth().Fit(ui.Contain)
		return
	}
	if kind == "text/csv" {
		rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil {
			ui.Text(c, err.Error()).Padding(15).TextColor(p.red)
			return
		}
		if len(rows) == 0 {
			return
		}
		columns := []ui.TableColumn{}
		for _, name := range rows[0] {
			columns = append(columns, ui.TableColumn{Title: name, Width: 180})
		}
		ui.Table(c, nil, columns, len(rows)-1, func(row, col int) {
			if col < len(rows[row+1]) {
				ui.Text(c, rows[row+1][col]).FontSize(12).Selectable()
			}
		}).Grow(1)
		return
	}
	if kind == "text/html" {
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
		return
	}
	var data any
	if json.Unmarshal([]byte(body), &data) == nil {
		if d.TreeOpen == nil {
			d.TreeOpen = map[string]bool{"$": true}
		}
		ui.Scroll(c).Grow(1).Padding(10).Children(func() { ui.Tree(c, func() { a.jsonNode(c, p, d, "$", "", data, 0) }) })
		return
	}
	a.codeView(c, p, body, "", "Response preview")
}
func (a *App) jsonNode(c *ui.Context, p colors, d *Draft, path, name string, value any, depth int) {
	if depth > 80 {
		return
	}
	label := name
	children := func() {}
	branch := false
	switch node := value.(type) {
	case map[string]any:
		branch = true
		label += fmt.Sprintf(" {%d}", len(node))
		children = func() {
			for _, key := range slices.Sorted(maps.Keys(node)) {
				a.jsonNode(c, p, d, path+"["+strconv.Quote(key)+"]", key, node[key], depth+1)
			}
		}
	case []any:
		branch = true
		label += fmt.Sprintf(" [%d]", len(node))
		children = func() {
			for i, value := range node {
				a.jsonNode(c, p, d, fmt.Sprintf("%s[%d]", path, i), fmt.Sprint(i), value, depth+1)
			}
		}
	default:
		data, _ := json.Marshal(value)
		if name != "" {
			label += ": "
		}
		label += string(data)
	}
	ui.Box(c).Key(path).Children(func() {
		var node *ui.Element
		if branch {
			open := d.TreeOpen[path]
			node = ui.TreeItem(c, label, &open, children)
			d.TreeOpen[path] = open
		} else {
			node = ui.TreeItem(c, label, nil, nil)
		}
		node.Font("monospace").FontSize(12).ContextMenu(func(m *ui.Menu) {
			if m.Item("Copy JSONPath").Chosen() {
				c.WriteClipboard(path)
			}
			if m.Item("Copy Value").Chosen() {
				data, _ := json.Marshal(value)
				c.WriteClipboard(string(data))
			}
		})
	})
}
