package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/locales"
)

func (a *App) pluginsSettings(c *ui.Context, p colors) {
	ui.Text(c, locales.Plugins()).FontSize(18).FontWeight(600)
	for _, plugin := range a.Engine.Plugins() {
		ui.Column(c).Key(plugin.ID).Gap(7).Padding(12).Border(1, p.border).Radius(6).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				enabled := plugin.Enabled
				if ui.Checkbox(c, &enabled, plugin.Name).Grow(1).Changed() {
					id := plugin.ID
					a.background(func() (func(), error) { return nil, a.Engine.PluginEnabled(a.ctx, id, enabled) })
				}
				ui.Text(c, plugin.Version).FontSize(11).TextColor(p.muted)
				if smallIconButton(c, "close", locales.RemoveName(plugin.Name)).Clicked() {
					id := plugin.ID
					a.background(func() (func(), error) { return nil, a.Engine.RemovePlugin(a.ctx, id) })
				}
			})
			if plugin.Description != "" {
				ui.Text(c, plugin.Description).FontSize(12).TextColor(p.muted)
			}
			ui.Text(c, plugin.Path).FontSize(10).Font("monospace").TextColor(p.subtle).SingleLine()
			if plugin.Error != "" {
				ui.Text(c, plugin.Error).FontSize(11).TextColor(p.red).Selectable()
			}
		})
	}
	ui.Row(c).Gap(8).Children(func() {
		if ui.PrimaryButton(c, locales.LoadGoPluginEllipsis()).Clicked() {
			a.background(func() (func(), error) {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: locales.LoadGoPlugin(), Filters: []mygo.FileFilter{{Name: locales.GoPlugin(), Extensions: []string{"go"}}}})
				if err != nil || len(paths) == 0 {
					return nil, err
				}
				_, err = a.Engine.InstallPlugin(a.ctx, paths[0])
				return nil, err
			})
		}
		if ui.Button(c, locales.Reload()).Clicked() {
			a.background(func() (func(), error) { return nil, a.Engine.LoadPlugins(a.ctx) })
		}
	})
	ui.Text(c, locales.GoPluginsCanAccessYourFiles()).FontSize(11).TextColor(p.muted)
}
