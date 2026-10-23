package desktop

import (
	"cmp"

	"github.com/egoist/mygo/yeekui"
	"github.com/egoist/mygo/yeekui/editor"
	"github.com/egoist/mygo/yeekui/markdown"
)

func (a *App) markdownView(c *ui.Context, p colors, source string) ui.Element {
	if a.markdown == nil {
		a.markdown = &markdown.Options{}
	}
	a.markdown.Theme = a.proseTheme(c, p)
	a.markdown.CodeBlock = func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).AlignSelf(ui.Stretch).Padding(9, 0, 0, 0).Radius(6).Background(p.sidebar).Clip().Children(func() { b.Body(c) })
	}
	return markdown.View(c, source, a.markdown).MaxWidth(512).MinWidth(0)
}

func (a *App) proseTheme(c *ui.Context, p colors) *markdown.Theme {
	size := c.Theme().FontSize
	codeSize := float32(n(a.settings, "editorFontSize"))
	if codeSize <= 0 {
		codeSize = 12
	}
	heading := func(size float32, weight int) markdown.HeadingStyle {
		return markdown.HeadingStyle{Size: size, Weight: weight, Top: 2.5, Bottom: 1.25}
	}
	return &markdown.Theme{
		CodeFont:            cmp.Or(s(a.settings, "editorFont"), "monospace"),
		Text:                p.text,
		Muted:               p.green,
		Link:                p.notice,
		Strong:              p.text,
		StrongWeight:        700,
		Code:                p.blue,
		CodeBackground:      p.border.Alpha(.55),
		CodeSize:            codeSize / size,
		BlockSpacing:        1.25,
		ItemSpacing:         0.25,
		ListIndent:          2,
		Headings:            [6]markdown.HeadingStyle{heading(2.25, 700), heading(1.5, 700), heading(1.25, 700), heading(1, 400), heading(1, 400), heading(1, 400)},
		QuoteBar:            p.border,
		QuoteBarWidth:       8,
		QuoteText:           p.text,
		Rule:                p.subtle,
		CodeBlockBackground: p.sidebar,
		CodeBlockBorder:     ui.Transparent,
		CodeBlockRadius:     6,
		CodeBlockPadding:    0.875,
		CodeColors:          prismTheme(p),
		TableBorder:         p.border,
	}
}

func prismTheme(p colors) *editor.Theme {
	t := &editor.Theme{Background: p.sidebar, Text: p.text}
	tok := func(color ui.Color, kinds ...editor.TokenKind) {
		for _, k := range kinds {
			t.Tokens[k].Color = color
		}
	}
	tok(p.muted, editor.TokenComment, editor.TokenPunctuation)
	tok(p.accent, editor.TokenProperty, editor.TokenAttribute, editor.TokenType)
	tok(p.notice, editor.TokenString)
	tok(p.blue, editor.TokenNumber, editor.TokenConstant, editor.TokenTag)
	tok(p.orange, editor.TokenRegexp)
	tok(p.green, editor.TokenVariable, editor.TokenFunction, editor.TokenInserted)
	tok(p.red, editor.TokenOperator, editor.TokenKeyword, editor.TokenDeleted, editor.TokenMeta)
	return t
}
