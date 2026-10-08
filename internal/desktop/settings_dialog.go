package desktop

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
)

// Settings tabs, in Yaak's order. Yaak's License tab is left out on purpose.
const (
	settingsGeneral = iota
	settingsTheme
	settingsInterface
	settingsShortcuts
	settingsPlugins
	settingsCertificates
	settingsProxy
)

var settingsTabs = []struct{ label, glyph string }{
	{"General", "gear"}, {"Theme", "palette"}, {"Interface", "columns"}, {"Shortcuts", "keyboard"},
	{"Plugins", "puzzle"}, {"Certificates", "shield"}, {"Proxy", "wifi"},
}

// settingsDialog is Yaak's Settings: tabs with icons down the left.
func (a *App) settingsDialog(c *ui.Context, p colors) {
	selected := a.modalTab
	ui.Row(c).Height(560).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(180).Shrink(0).Padding(10).Gap(2).Radius(0, 0, 0, 10).Background(p.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
			for i, tab := range settingsTabs {
				var badge *countBadge
				switch i {
				case settingsCertificates:
					badge = badgeCount(len(oslice(a.settings, "clientCertificates")))
				case settingsPlugins:
					badge = badgeCount(len(a.Engine.Plugins()))
				case settingsProxy:
					badge = badgeDot(s(o(a.settings, "proxy"), "type") == "enabled")
				}
				item := ui.ButtonBase(c).Key(i).Label(tab.label).Height(30).Padding(0, 10).Gap(8).Radius(4).Justify(ui.Start)
				switch {
				case i == a.modalTab:
					item.Background(p.border.Alpha(.55))
				case item.Hovered():
					item.Background(p.border.Alpha(.3))
				}
				item.Children(func() {
					icon(c, tab.glyph).FontSize(14).TextColor(p.muted)
					text := ui.Text(c, tab.label).FontSize(13).Grow(1)
					if i != a.modalTab {
						text.TextColor(p.muted)
					}
					badge.view(c, p)
				})
				if item.Clicked() {
					selected = i
				}
			}
		})
		ui.Scroll(c).Grow(1).MinWidth(0).Padding(18, 24, 18, 24).Gap(24).Children(func() {
			switch a.modalTab {
			case settingsGeneral:
				a.settingsGeneral(c, p)
			case settingsTheme:
				a.settingsTheme(c, p)
			case settingsInterface:
				a.settingsInterface(c, p)
			case settingsShortcuts:
				a.settingsShortcuts(c, p)
			case settingsPlugins:
				a.pluginsSettings(c, p)
			case settingsCertificates:
				a.certificateSettings(c, p)
			case settingsProxy:
				a.proxySettingsView(c, p)
			}
		})
	})
	a.modalTab = selected
}

func settingsHeading(c *ui.Context, p colors, title, description string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(20).FontWeight(600)
		ui.Text(c, description).FontSize(13).TextColor(p.muted)
	})
}

// settingValue is Yaak's SettingValue: a monospace value with a reveal and a copy button.
func settingValue(c *ui.Context, p colors, value string, reveal func()) {
	ui.Text(c, value).Font("monospace").FontSize(12).TextColor(p.muted).SingleLine().MaxWidth(320).Selectable()
	if reveal != nil && smallIconButton(c, "folder", "Reveal in Finder").Clicked() {
		reveal()
	}
	if smallIconButton(c, "copy", "Copy value").Clicked() {
		c.WriteClipboard(value)
	}
}

func (a *App) settingsGeneral(c *ui.Context, p colors) {
	settingsHeading(c, p, "General", "Configure general settings.")
	settingsSection(c, p, "App Info", "App Info", func() {
		settingRow(c, p, "Version", "Current Yeek version.", nil, func() { settingValue(c, p, appVersion, nil) })
		data := a.Engine.DataDir
		settingRow(c, p, "Data Directory", "Where Yeek stores application data.", nil, func() {
			settingValue(c, p, data, func() { mygo.Shell.ShowItemInFolder(data) })
		})
		logs, err := mygo.App.Path(mygo.PathLogs)
		if err == nil {
			settingRow(c, p, "Logs Directory", "Where Yeek writes application logs.", nil, func() {
				settingValue(c, p, logs, func() { mygo.Shell.ShowItemInFolder(logs) })
			})
		}
	})
}

// appVersion is the version in mygo.json.
const appVersion = "0.1.0"

func (a *App) settingsTheme(c *ui.Context, p colors) {
	settingsHeading(c, p, "Theme", "Make Yeek your own by selecting a theme, or create your own with a Go plugin.")
	appearance := cmpOrString(s(a.settings, "appearance"), "system")
	settingsSection(c, p, "Theme", "Theme", func() {
		settingRow(c, p, "Appearance", "Choose whether Yeek follows your system appearance or uses a fixed mode.", nil, func() {
			labels, values := []string{"Automatic", "Light", "Dark"}, []string{"system", "light", "dark"}
			label := labels[max(0, slices.Index(values, appearance))]
			if ui.Select(c, &label, labels).Label("Appearance setting").Width(220).Changed() {
				a.saveSetting("appearance", values[slices.Index(labels, label)])
			}
		})
		for _, dark := range []bool{false, true} {
			if appearance == map[bool]string{false: "dark", true: "light"}[dark] {
				continue
			}
			key, title, mode := "themeLight", "Light theme", "light"
			if dark {
				key, title, mode = "themeDark", "Dark theme", "dark"
			}
			names, ids := a.themeChoices(dark)
			chosen := names[max(0, slices.Index(ids, s(a.settings, key)))]
			settingRow(c, p, title, "Theme used when Yeek is in "+mode+" mode.", nil, func() {
				if ui.Select(c, &chosen, names).Label(title + " setting").Width(220).Changed() {
					a.saveSetting(key, ids[slices.Index(names, chosen)])
				}
			})
		}
	})
	settingsSection(c, p, "Preview", "Preview", func() {
		ui.Column(c).Margin(14, 0, 0, 0).Padding(12).Gap(12).Radius(4).Border(1, p.border).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				glyph := "sun"
				if c.Theme().Dark {
					glyph = "moon"
				}
				icon(c, glyph).FontSize(14)
				ui.Text(c, a.activeThemeName(c.Theme().Dark)).FontWeight(600).FontSize(13)
				ui.Text(c, "(preview)").Italic().FontSize(13).TextColor(p.muted)
			})
			ui.Row(c).Gap(6).Children(func() {
				glyphs := []string{"info", "gear", "download", "alert", "redirect", "copy", "send", "search"}
				for i, color := range []ui.Color{p.accent, p.blue, p.green, p.notice, p.orange, p.red, p.muted, p.text} {
					swatch := ui.Box(c).Key(i).Size(24, 24).Radius(4).Background(color.Alpha(.15)).Justify(ui.Center).AlignItems(ui.Center)
					swatch.Children(func() { icon(c, glyphs[i]).FontSize(13).TextColor(color) })
				}
				for i, color := range []ui.Color{p.accent, p.blue, p.green, p.notice, p.orange, p.red, p.muted, p.text} {
					swatch := ui.Box(c).Key(10+i).Size(24, 24).Radius(4).Border(1, color.Alpha(.6)).Justify(ui.Center).AlignItems(ui.Center)
					swatch.Children(func() { icon(c, glyphs[i]).FontSize(13).TextColor(color) })
				}
			})
			ui.Box(c).Height(96).Children(func() {
				a.codeView(c, p, "let foo = { // Demo code editor\n  foo: (\"bar\" || \"baz\" ?? 'qux'),\n  baz: [1, 10.2, null, false, true],\n};", "javascript", "Theme preview")
			})
		})
	})
}

func cmpOrString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// themeChoices lists Yaak's own theme first, then the built-in and plugin themes.
func (a *App) themeChoices(dark bool) (names, ids []string) {
	names, ids = []string{"Yaak"}, []string{"yaak-light"}
	if dark {
		ids[0] = "yaak-dark"
	}
	for _, theme := range builtinThemes {
		if theme.Dark == dark {
			names, ids = append(names, theme.Name), append(ids, theme.ID)
		}
	}
	for _, theme := range a.Engine.PluginThemes() {
		if theme.Dark == dark {
			names, ids = append(names, theme.Name), append(ids, "plugin:"+theme.Name)
		}
	}
	return names, ids
}

func (a *App) activeThemeName(dark bool) string {
	key := "themeLight"
	if dark {
		key = "themeDark"
	}
	names, ids := a.themeChoices(dark)
	return names[max(0, slices.Index(ids, s(a.settings, key)))]
}

var fontSizes = func() []string {
	sizes := []string{}
	for size := 8; size <= 30; size++ {
		sizes = append(sizes, strconv.Itoa(size))
	}
	return sizes
}()

func (a *App) settingsInterface(c *ui.Context, p colors) {
	settingsHeading(c, p, "Interface", "Tweak settings related to the user interface.")
	settingsSection(c, p, "Workspaces", "Workspaces", func() {
		settingRow(c, p, "Open workspace behavior", "Choose what happens when opening another workspace.", nil, func() {
			labels := []string{"Always ask", "Open in current window", "Open in new window"}
			label := labels[0]
			if inNewWindow, ok := a.settings["openWorkspaceNewWindow"].(bool); ok {
				label = labels[map[bool]int{false: 1, true: 2}[inNewWindow]]
			}
			if ui.Select(c, &label, labels).Label("Open workspace behavior setting").Width(220).Changed() {
				a.saveSetting("openWorkspaceNewWindow", map[string]any{labels[0]: nil, labels[1]: false, labels[2]: true}[label])
			}
		})
	})
	settingsSection(c, p, "Fonts", "Fonts", func() {
		fontRow := func(title, description, fontKey, sizeKey, fallback string) {
			font := s(a.settings, fontKey)
			size := strconv.Itoa(int(n(a.settings, sizeKey)))
			if n(a.settings, sizeKey) == 0 {
				size = fallback
			}
			settingRow(c, p, title, description, nil, func() {
				if ui.TextInput(c, &font).Label(title + " setting").Placeholder("System default").Width(220).Changed() {
					a.saveSetting(fontKey, nilIfEmpty(strings.TrimSpace(font)))
				}
				if ui.Select(c, &size, fontSizes).Label(title + " size").Width(72).Changed() {
					value, _ := strconv.Atoi(size)
					a.saveSetting(sizeKey, value)
				}
			})
		}
		fontRow("Interface font", "Font used for Yeek interface controls.", "interfaceFont", "interfaceFontSize", "14")
		fontRow("Editor font", "Font used in request and response editors.", "editorFont", "editorFontSize", "12")
	})
	settingsSection(c, p, "Editor", "Editor", func() {
		a.booleanSetting(c, p, "editorSoftWrap", "Wrap editor lines", "Wrap long lines in request and response editors.")
		a.booleanSetting(c, p, "coloredMethods", "Colorize request methods", "Use method-specific colors for HTTP request methods.")
	})
	settingsSection(c, p, "Window", "Window", func() {
		a.booleanSetting(c, p, "useNativeTitlebar", "Native title bar", "Use the operating system's standard title bar and window controls. Takes effect after restarting.")
	})
}

// booleanSetting is Yaak's ModelSettingRowBoolean for an app setting.
func (a *App) booleanSetting(c *ui.Context, p colors, key, title, description string) {
	settingRow(c, p, title, description, nil, func() {
		value := b(a.settings, key)
		if ui.Checkbox(c, &value, "").Label("Enable " + title).Changed() {
			a.saveSetting(key, value)
		}
	})
}

// settingsShortcuts is Yaak's SettingsHotkeys: each action's keys, which
// can be recorded anew or reset to the default.
func (a *App) settingsShortcuts(c *ui.Context, p colors) {
	settingsHeading(c, p, "Keyboard Shortcuts", "Click a shortcut to record new keys for it.")
	ui.TextInput(c, &a.hotkeyFilter).Label("Filter shortcuts").Placeholder("Filter shortcuts").FillWidth()
	custom := o(a.settings, "hotkeys")
	query := strings.ToLower(strings.TrimSpace(a.hotkeyFilter))
	ui.Column(c).FillWidth().Children(func() {
		for _, h := range hotkeys {
			if query != "" && !strings.Contains(strings.ToLower(h.label+" "+h.action), query) {
				continue
			}
			ui.Row(c).Key(h.action).Height(36).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, h.label).FontSize(13).Grow(1)
				recording := a.recordingHotkey == h.action
				label := a.hotkeyText(h.action)
				switch {
				case recording:
					label = "Press keys…"
				case label == "":
					label = "None"
				}
				button := ui.ButtonBase(c).Label("Shortcut for "+h.label).Height(26).Padding(0, 10).Radius(4).Border(1, p.border).Focusable()
				if recording {
					button.Border(1, p.accent)
					button.Focus()
					button.HandleInput(func(ev ui.InputEvent) bool {
						if ev.Kind != ui.InputKeyDown {
							return false
						}
						if ev.Key == ui.KeyEscape && ev.Mods == 0 {
							a.recordingHotkey = ""
							return true
						}
						if text, ok := formatHotkey(ev.Mods, ev.Key); ok {
							a.setHotkey(h.action, []any{text})
							a.recordingHotkey = ""
						}
						return true
					})
				}
				button.Children(func() { ui.Text(c, label).Font("monospace").FontSize(12).TextColor(p.muted) })
				if button.Clicked() {
					a.recordingHotkey = h.action
				}
				if _, changed := custom[h.action]; changed && smallIconButton(c, "undo", "Reset "+h.label).Clicked() {
					a.setHotkey(h.action, nil)
				}
			})
		}
	})
}

// setHotkey stores an action's keys, or removes them to restore the default.
func (a *App) setHotkey(action string, keys []any) {
	hotkeys := deepCopy(o(a.settings, "hotkeys"))
	if keys == nil {
		delete(hotkeys, action)
	} else {
		hotkeys[action] = keys
	}
	a.saveSetting("hotkeys", hotkeys)
}
