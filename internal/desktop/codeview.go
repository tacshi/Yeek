package desktop

import "github.com/egoist/mygo/yeekui"

func (a *App) codeView(c *ui.Context, p colors, content, language, label string) {
	a.nativeEditor(c, p, &content, label, language, true)
}
