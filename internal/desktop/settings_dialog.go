package desktop

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/locales"
)

const (
	settingsGeneral = iota
	settingsTheme
	settingsInterface
	settingsShortcuts
	settingsPlugins
	settingsAgents
	settingsCertificates
	settingsProxy
)

var settingsTabs = []struct {
	label func() string
	glyph string
}{
	{locales.General, "gear"}, {locales.Theme, "palette"}, {locales.Interface, "columns"}, {locales.Shortcuts, "keyboard"},
	{locales.Plugins, "puzzle"}, {locales.Agents, "sparkles"}, {locales.Certificates, "shield"}, {locales.Proxy, "wifi"},
}

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
				case settingsAgents:
					badge = badgeDot(b(a.settings, "mcpServerEnabled"))
				case settingsProxy:
					badge = badgeDot(s(o(a.settings, "proxy"), "type") == "enabled")
				}
				item := ui.ButtonBase(c).Key(i).Label(tab.label()).Height(30).Padding(0, 10).Gap(8).Radius(4).Justify(ui.Start)
				switch {
				case i == a.modalTab:
					item.Background(p.border.Alpha(.55))
				case item.Hovered():
					item.Background(p.border.Alpha(.3))
				}
				item.Children(func() {
					icon(c, tab.glyph).FontSize(14).TextColor(p.muted)
					text := ui.Text(c, tab.label()).FontSize(13).Grow(1)
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
			case settingsAgents:
				a.settingsAgents(c, p)
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

func settingValue(c *ui.Context, p colors, value string, reveal func()) {
	ui.Text(c, value).Font("monospace").FontSize(12).TextColor(p.muted).SingleLine().MaxWidth(320).Selectable()
	if reveal != nil && smallIconButton(c, "folder", locales.RevealInFinder()).Clicked() {
		reveal()
	}
	if smallIconButton(c, "copy", locales.CopyValue()).Clicked() {
		c.WriteClipboard(value)
	}
}

func (a *App) settingsGeneral(c *ui.Context, p colors) {
	settingsHeading(c, p, locales.General(), locales.ConfigureGeneralSettingsForUpdateBehavior())
	settingsSection(c, p, locales.Updates(), locales.Updates(), func() {
		auto := b(a.settings, "autoupdate")
		settingRow(c, p, locales.UpdateBehavior(), locales.ChooseWhetherUpdatesAreInstalledAutomatically(), nil, func() {
			labels, values := []string{locales.Automatic(), locales.Manual()}, []bool{true, false}
			label := labels[slices.Index(values, auto)]
			if ui.Select(c, &label, labels).Label(locales.UpdateBehavior()).Width(192).Changed() {
				a.saveSetting("autoupdate", label == labels[0])
			}
		})
		settingRow(c, p, locales.AutomaticallyDownloadUpdates(), locales.DownloadYeekUpdatesInTheBackground(), nil, func() {
			on := b(a.settings, "autoDownloadUpdates")
			if ui.Checkbox(c, &on, "").Label(locales.EnableAutomaticallyDownloadUpdates()).Disabled(!auto).Changed() {
				a.saveSetting("autoDownloadUpdates", on)
			}
		})
	})
	settingsSection(c, p, locales.AppInfo(), locales.AppInfo(), func() {
		settingRow(c, p, locales.Version(), locales.CurrentYeekVersion(), nil, func() { settingValue(c, p, appVersion(), nil) })
		data := a.Engine.DataDir
		settingRow(c, p, locales.DataDirectory(), locales.WhereYeekStoresApplicationData(), nil, func() {
			settingValue(c, p, data, func() { mygo.Shell.ShowItemInFolder(data) })
		})
		logs, err := mygo.App.Path(mygo.PathLogs)
		if err == nil {
			settingRow(c, p, locales.LogsDirectory(), locales.WhereYeekWritesApplicationLogs(), nil, func() {
				settingValue(c, p, logs, func() { mygo.Shell.ShowItemInFolder(logs) })
			})
		}
	})
}

func (a *App) settingsTheme(c *ui.Context, p colors) {
	settingsHeading(c, p, locales.Theme(), locales.MakeYeekYourOwnBySelecting())
	appearance := cmpOrString(s(a.settings, "appearance"), "system")
	settingsSection(c, p, locales.Theme(), locales.Theme(), func() {
		settingRow(c, p, locales.Appearance(), locales.ChooseWhetherYeekFollowsYourSystem(), nil, func() {
			labels, values := []string{locales.Automatic(), locales.Light(), locales.Dark()}, []string{"system", "light", "dark"}
			label := labels[max(0, slices.Index(values, appearance))]
			if ui.Select(c, &label, labels).Label(locales.AppearanceSetting()).Width(220).Changed() {
				a.saveSetting("appearance", values[slices.Index(labels, label)])
			}
		})
		for _, dark := range []bool{false, true} {
			if appearance == map[bool]string{false: "dark", true: "light"}[dark] {
				continue
			}
			key, title, description := "themeLight", locales.LightTheme(), locales.ThemeUsedWhenYeekIsInLight()
			if dark {
				key, title, description = "themeDark", locales.DarkTheme(), locales.ThemeUsedWhenYeekIsIn()
			}
			names, ids := a.themeChoices(dark)
			chosen := names[max(0, slices.Index(ids, s(a.settings, key)))]
			settingRow(c, p, title, description, nil, func() {
				if ui.Select(c, &chosen, names).Label(locales.SettingSetting(title)).Width(220).Changed() {
					a.saveSetting(key, ids[slices.Index(names, chosen)])
				}
			})
		}
	})
	settingsSection(c, p, locales.Preview(), locales.Preview(), func() {
		ui.Column(c).Margin(14, 0, 0, 0).Padding(12).Gap(12).Radius(4).Border(1, p.border).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				glyph := "sun"
				if c.Theme().Dark {
					glyph = "moon"
				}
				icon(c, glyph).FontSize(14)
				ui.Text(c, a.activeThemeName(c.Theme().Dark)).FontWeight(600).FontSize(13)
				ui.Text(c, locales.PreviewParenthesized()).Italic().FontSize(13).TextColor(p.muted)
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
				a.codeView(c, p, "let foo = { // Demo code editor\n  foo: (\"bar\" || \"baz\" ?? 'qux'),\n  baz: [1, 10.2, null, false, true],\n};", "javascript", locales.ThemePreview())
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

func (a *App) themeChoices(dark bool) (names, ids []string) {
	names, ids = []string{"Yeek"}, []string{"yeek-light"}
	if dark {
		ids[0] = "yeek-dark"
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

var systemDefaultFont = locales.SystemDefault

type systemFonts struct {
	ui, editor []string
	loaded     bool
}

func listFonts() (uiFonts, editorFonts []string) {
	for _, f := range ui.FontFamilies() {
		switch {
		case f.Registered:
		case f.Monospaced:
			editorFonts = append(editorFonts, f.Name)
		default:
			uiFonts = append(uiFonts, f.Name)
		}
	}
	return uiFonts, editorFonts
}

var fontSizes = func() []string {
	sizes := []string{}
	for size := 8; size <= 30; size++ {
		sizes = append(sizes, strconv.Itoa(size))
	}
	return sizes
}()

func (a *App) settingsInterface(c *ui.Context, p colors) {
	settingsHeading(c, p, locales.Interface(), locales.TweakSettingsRelatedToTheUser())
	settingsSection(c, p, "Language", locales.Language(), func() { a.languageSetting(c, p) })
	settingsSection(c, p, locales.Workspaces(), locales.Workspaces(), func() {
		settingRow(c, p, locales.OpenWorkspaceBehavior(), locales.ChooseWhatHappensWhenOpeningAnother(), nil, func() {
			labels := []string{locales.AlwaysAsk(), locales.OpenInCurrentWindow(), locales.OpenInNewWindow()}
			label := labels[0]
			if inNewWindow, ok := a.settings["openWorkspaceNewWindow"].(bool); ok {
				label = labels[map[bool]int{false: 1, true: 2}[inNewWindow]]
			}
			if ui.Select(c, &label, labels).Label(locales.OpenWorkspaceBehaviorSetting()).Width(220).Changed() {
				a.saveSetting("openWorkspaceNewWindow", map[string]any{labels[0]: nil, labels[1]: false, labels[2]: true}[label])
			}
		})
	})
	settingsSection(c, p, locales.Fonts(), locales.Fonts(), func() {
		if a.fonts == nil {
			a.fonts = &systemFonts{}
			fonts := a.fonts
			a.background(func() (func(), error) {
				uiFonts, editorFonts := listFonts()
				return func() { fonts.ui, fonts.editor, fonts.loaded = uiFonts, editorFonts, true }, nil
			})
		}
		fontRow := func(title, description, fontKey, sizeKey, fallback string, fonts []string) {
			font := cmp.Or(s(a.settings, fontKey), systemDefaultFont())
			size := strconv.Itoa(int(n(a.settings, sizeKey)))
			if n(a.settings, sizeKey) == 0 {
				size = fallback
			}
			settingRow(c, p, title, description, nil, func() {
				if a.fonts.loaded {
					options := append([]string{systemDefaultFont()}, fonts...)
					if !slices.Contains(options, font) {
						options = append(options, font)
					}
					if ui.Select(c, &font, options).Label(locales.SettingSetting(title)).Width(288).Changed() {
						if font == systemDefaultFont() {
							a.saveSetting(fontKey, nil)
						} else {
							a.saveSetting(fontKey, font)
						}
					}
				}
				if ui.Select(c, &size, fontSizes).Label(locales.SettingSize(title)).Width(72).Changed() {
					value, _ := strconv.Atoi(size)
					a.saveSetting(sizeKey, value)
				}
			})
		}
		fontRow(locales.InterfaceFont(), locales.FontUsedForYeekInterfaceControls(), "interfaceFont", "interfaceFontSize", "14", append(slices.Clone(a.fonts.ui), a.fonts.editor...))
		fontRow(locales.EditorFont(), locales.FontUsedInRequestAndResponse(), "editorFont", "editorFontSize", "12", a.fonts.editor)
	})
	settingsSection(c, p, locales.Editor(), locales.Editor(), func() {
		a.booleanSetting(c, p, "editorSoftWrap", locales.WrapEditorLines(), locales.WrapLongLinesInRequestAnd())
		a.booleanSetting(c, p, "coloredMethods", locales.ColorizeRequestMethods(), locales.UseMethodSpecificColorsForHTTP())
	})
	settingsSection(c, p, locales.Window(), locales.Window(), func() {
		a.booleanSetting(c, p, "useNativeTitlebar", locales.NativeTitleBar(), locales.UseTheOperatingSystemsStandardTitle())
	})
}

func (a *App) booleanSetting(c *ui.Context, p colors, key, title, description string) {
	settingRow(c, p, title, description, nil, func() {
		value := b(a.settings, key)
		if ui.Checkbox(c, &value, "").Label(locales.EnableSetting(title)).Changed() {
			a.saveSetting(key, value)
		}
	})
}

func (a *App) settingsShortcuts(c *ui.Context, p colors) {
	settingsHeading(c, p, locales.KeyboardShortcuts(), locales.ClickAShortcutToRecordNew())
	ui.TextInput(c, &a.hotkeyFilter).Label(locales.FilterShortcuts()).Placeholder(locales.FilterShortcuts()).FillWidth()
	custom := o(a.settings, "hotkeys")
	query := strings.ToLower(strings.TrimSpace(a.hotkeyFilter))
	ui.Column(c).FillWidth().Children(func() {
		for _, h := range hotkeys {
			if query != "" && !strings.Contains(strings.ToLower(h.label()+" "+h.action), query) {
				continue
			}
			ui.Row(c).Key(h.action).Height(36).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, h.label()).FontSize(13).Grow(1)
				recording := a.recordingHotkey == h.action
				label := a.hotkeyText(h.action)
				switch {
				case recording:
					label = locales.PressKeysEllipsis()
				case label == "":
					label = locales.None()
				}
				button := ui.ButtonBase(c).Label(locales.ShortcutForAction(h.label())).Height(26).Padding(0, 10).Radius(4).Border(1, p.border).Focusable()
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
						key := ev.Key
						if key == ui.KeyUnknown {
							key = ev.PhysicalKey
						}
						if text, ok := formatHotkey(ev.Mods, key); ok {
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
				if _, changed := custom[h.action]; changed && smallIconButton(c, "undo", locales.ResetAction(h.label())).Clicked() {
					a.setHotkey(h.action, nil)
				}
			})
		}
	})
}

func (a *App) setHotkey(action string, keys []any) {
	hotkeys := deepCopy(o(a.settings, "hotkeys"))
	if keys == nil {
		delete(hotkeys, action)
	} else {
		hotkeys[action] = keys
	}
	a.saveSetting("hotkeys", hotkeys)
}
