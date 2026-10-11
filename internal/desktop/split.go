package desktop

import "github.com/egoist/mygo/yeekui"

// splitQuiet is ui.Split, or ui.SplitVertical when vertical is set, whose
// divider shows only while the pointer is over it or drags it, as in
// Yaak's pane layout. The split is built in a copy of the theme with a
// transparent border, which its divider paints in, and its panes in the
// theme itself.
func splitQuiet(c *ui.Context, size *float32, vertical bool, first, second func()) ui.Element {
	theme := c.Theme()
	quiet := *theme
	quiet.Border = ui.Transparent
	c.SetTheme(&quiet)
	defer c.SetTheme(theme)
	pane := func(build func()) func() {
		return func() {
			c.SetTheme(theme)
			build()
		}
	}
	if vertical {
		return ui.SplitVertical(c, size, pane(first), pane(second))
	}
	return ui.Split(c, size, pane(first), pane(second))
}
