package desktop

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.Strikethrough, extension.Linkify, extension.TaskList)).Parser()

// markdownView renders Markdown as native text: headings, paragraphs, lists,
// quotes, code and rules, with emphasis, inline code and web links.
func markdownView(c *ui.Context, p colors, source string) {
	src := []byte(source)
	doc := markdownParser.Parse(text.NewReader(src))
	ui.Column(c).Gap(10).MinWidth(0).Children(func() { markdownBlocks(c, p, doc, src, 0) })
}

func markdownBlocks(c *ui.Context, p colors, parent ast.Node, src []byte, depth int) {
	if depth > 16 {
		return
	}
	i := 0
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		i++
		key := fmt.Sprintf("%d:%d", depth, i)
		switch n := node.(type) {
		case *ast.Heading:
			size := map[int]float32{1: 20, 2: 17, 3: 15}[n.Level]
			if size == 0 {
				size = 13
			}
			markdownInline(c, p, n, src).Key(key).FontSize(size).FontWeight(600)
		case *ast.Paragraph, *ast.TextBlock:
			markdownInline(c, p, n, src).Key(key).FontSize(13).LineHeight(1.45)
		case *ast.List:
			ui.Column(c).Key(key).Gap(4).MinWidth(0).Children(func() {
				index := 0
				for item := n.FirstChild(); item != nil; item = item.NextSibling() {
					marker := "•"
					if n.IsOrdered() {
						marker = fmt.Sprintf("%d.", n.Start+index)
					}
					index++
					ui.Row(c).Key(index).Gap(8).AlignItems(ui.Start).MinWidth(0).Children(func() {
						ui.Text(c, marker).FontSize(13).TextColor(p.muted).Width(18).Shrink(0)
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() { markdownBlocks(c, p, item, src, depth+1) })
					})
				}
			})
		case *ast.Blockquote:
			ui.Column(c).Key(key).Gap(8).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 3).BorderColor(p.border).Children(func() {
				markdownBlocks(c, p, n, src, depth+1)
			})
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			var code strings.Builder
			lines := n.Lines()
			for j := range lines.Len() {
				segment := lines.At(j)
				code.Write(segment.Value(src))
			}
			ui.Text(c, strings.TrimRight(code.String(), "\n")).Key(key).Font("monospace").FontSize(12).Padding(10, 12).Radius(5).Background(p.border.Alpha(.3)).Selectable()
		case *ast.ThematicBreak:
			ui.Box(c).Key(key).Height(1).Background(p.border)
		case *ast.HTMLBlock:
			var raw strings.Builder
			lines := n.Lines()
			for j := range lines.Len() {
				segment := lines.At(j)
				raw.Write(segment.Value(src))
			}
			ui.Text(c, strings.TrimSpace(raw.String())).Key(key).Font("monospace").FontSize(12).TextColor(p.muted).Selectable()
		default:
			markdownBlocks(c, p, node, src, depth+1)
		}
	}
}

// markdownInline renders a block's inline content as one paragraph.
func markdownInline(c *ui.Context, p colors, block ast.Node, src []byte) *ui.Element {
	type style struct {
		bold, italic, strike bool
	}
	var walk func(node ast.Node, s style)
	walk = func(node ast.Node, s style) {
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			switch n := child.(type) {
			case *ast.Text:
				value := string(n.Value(src))
				if n.SoftLineBreak() {
					value += " "
				}
				if n.HardLineBreak() {
					value += "\n"
				}
				span := ui.Text(c, value)
				if s.bold {
					span.FontWeight(600)
				}
				if s.italic {
					span.Italic()
				}
				if s.strike {
					span.Strikethrough()
				}
			case *ast.String:
				ui.Text(c, string(n.Value))
			case *ast.CodeSpan:
				var code strings.Builder
				for part := n.FirstChild(); part != nil; part = part.NextSibling() {
					if t, ok := part.(*ast.Text); ok {
						code.Write(t.Value(src))
					}
				}
				ui.Text(c, code.String()).Font("monospace").Background(p.border.Alpha(.45))
			case *ast.Emphasis:
				next := s
				if n.Level >= 2 {
					next.bold = true
				} else {
					next.italic = true
				}
				walk(n, next)
			case *extast.Strikethrough:
				next := s
				next.strike = true
				walk(n, next)
			case *ast.Link:
				markdownLink(c, nodeText(n, src), string(n.Destination))
			case *ast.AutoLink:
				markdownLink(c, string(n.Label(src)), string(n.URL(src)))
			case *extast.TaskCheckBox:
				mark := "☐ "
				if n.IsChecked {
					mark = "☑ "
				}
				ui.Text(c, mark).TextColor(p.muted)
			case *ast.Image:
				ui.Text(c, nodeText(n, src)).TextColor(p.muted)
			case *ast.RawHTML:
				// Inline HTML is not rendered.
			default:
				walk(child, s)
			}
		}
	}
	return ui.RichText(c).MinWidth(0).Selectable().Children(func() { walk(block, style{}) })
}

// markdownLink opens only web and mail links; anything else stays plain text.
func markdownLink(c *ui.Context, label, destination string) {
	if label == "" {
		label = destination
	}
	if u, err := url.Parse(destination); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
		ui.Link(c, label, destination)
		return
	}
	ui.Text(c, label)
}

func nodeText(node ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			switch t := child.(type) {
			case *ast.Text:
				b.Write(t.Value(src))
			case *ast.String:
				b.Write(t.Value)
			default:
				walk(child)
			}
		}
	}
	walk(node)
	return b.String()
}
