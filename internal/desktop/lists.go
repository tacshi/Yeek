package desktop

import "github.com/egoist/mygo/yeekui"

func localList(e ui.Element) *ui.ListState {
	return ui.Local(e, "list", func() ui.ListState { return ui.ListState{} })
}

func quietTheme(c *ui.Context, p colors) *ui.Theme {
	quiet := *c.Theme()
	quiet.Accent, quiet.InactiveAccent, quiet.AccentText = p.border.Alpha(.55), p.border.Alpha(.55), quiet.Text
	quiet.SurfaceHover = p.border.Alpha(.3)
	return &quiet
}

func quietSelection(c *ui.Context, p colors, build func(normal func(func())) ui.Element) ui.Element {
	theme, quiet := c.Theme(), quietTheme(c, p)
	c.SetTheme(quiet)
	defer c.SetTheme(theme)
	return build(func(content func()) {
		c.SetTheme(theme)
		defer c.SetTheme(quiet)
		content()
	})
}

func quietList(c *ui.Context, p colors, s *ui.ListState, n int, row func(i int)) ui.Element {
	return quietSelection(c, p, func(normal func(func())) ui.Element {
		return ui.List(c, s, n, func(i int) { normal(func() { row(i) }) })
	})
}

func quietTable(c *ui.Context, p colors, s *ui.ListState, columns []ui.TableColumn, n int, cell func(row, col int)) ui.Element {
	return quietSelection(c, p, func(normal func(func())) ui.Element {
		return ui.Table(c, s, columns, n, func(row, col int) { normal(func() { cell(row, col) }) })
	})
}
