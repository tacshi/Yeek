package desktop

import "github.com/egoist/mygo/yeekui"

func splitQuiet(c *ui.Context, size *float32, vertical bool, first, second func()) ui.Element {
	build := ui.Split
	if vertical {
		build = ui.SplitVertical
	}
	return quietSplit(c, build, size, first, second)
}

func splitQuietPercent(c *ui.Context, percent *float32, vertical bool, first, second func()) ui.Element {
	build := ui.SplitPercent
	if vertical {
		build = ui.SplitVerticalPercent
	}
	return quietSplit(c, build, percent, first, second)
}

func quietSplit(c *ui.Context, build func(*ui.Context, *float32, func(), func()) ui.Element, size *float32, first, second func()) ui.Element {
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
	return build(c, size, pane(first), pane(second))
}
