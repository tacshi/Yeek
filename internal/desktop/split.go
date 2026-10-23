package desktop

import "github.com/egoist/mygo/yeekui"

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
