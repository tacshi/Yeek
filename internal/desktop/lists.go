package desktop

import "github.com/egoist/mygo/yeekui"

func localList(e ui.Element) *ui.ListState {
	return ui.Local(e, "list", func() ui.ListState { return ui.ListState{} })
}

func quietList(c *ui.Context, p colors, s *ui.ListState, n int, row func(i int)) ui.Element {
	theme := c.Theme()
	quiet := *theme
	quiet.Accent, quiet.InactiveAccent, quiet.AccentText = p.border.Alpha(.55), p.border.Alpha(.55), theme.Text
	quiet.SurfaceHover = p.border.Alpha(.3)
	c.SetTheme(&quiet)
	defer c.SetTheme(theme)
	return ui.List(c, s, n, func(i int) {
		c.SetTheme(theme)
		defer c.SetTheme(&quiet)
		row(i)
	})
}
