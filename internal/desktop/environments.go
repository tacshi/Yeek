package desktop

import (
	"regexp"
	"slices"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

var environmentColors = []string{"danger", "warning", "notice", "success", "primary", "info", "secondary"}

func envColor(stored string, p colors) (ui.Color, bool) {
	stored = strings.TrimSpace(stored)
	if name, ok := strings.CutPrefix(stored, "var(--"); ok {
		switch strings.TrimSuffix(name, ")") {
		case "danger":
			return p.red, true
		case "warning":
			return p.orange, true
		case "notice":
			return p.notice, true
		case "success":
			return p.green, true
		case "primary":
			return p.accent, true
		case "info":
			return p.blue, true
		case "secondary":
			return p.muted, true
		}
		return ui.Color{}, false
	}
	if hexColor.MatchString(stored) {
		return ui.Hex(stored), true
	}
	return ui.Color{}, false
}

var (
	hexColor        = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	variableNameRaw = regexp.MustCompile(`(?i)^[a-z_][a-z0-9_.-]*$`)
)

func colorDot(c *ui.Context, color ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Radius(size / 2).Shrink(0).Background(color)
}

func (a *App) environmentsBreakdown() (base engine.Object, subs []engine.Object) {
	for _, env := range a.list("environment") {
		switch s(env, "parentModel") {
		case "workspace":
			if base == nil {
				base = env
			}
		case "folder":
		default:
			subs = append(subs, env)
		}
	}
	slices.SortStableFunc(subs, func(x, y engine.Object) int {
		if n(x, "sortPriority") != n(y, "sortPriority") {
			return int(n(x, "sortPriority") - n(y, "sortPriority"))
		}
		return strings.Compare(s(x, "createdAt"), s(y, "createdAt"))
	})
	return base, subs
}

func (a *App) openEnvironments() {
	base, _ := a.environmentsBreakdown()
	id := a.environment
	if a.models[id] == nil {
		id = s(base, "id")
	}
	a.openEnvironmentsAt(id)
}

func (a *App) openEnvironmentsAt(id string) {
	a.prompt("environments", locales.Environments(), "", id)
	a.envDraft = kvRows(a.models[id], "variables")
}

func (a *App) environmentDialog(c *ui.Context, p colors) {
	base, subs := a.environmentsBreakdown()
	bases := a.baseEnvironments()
	multiple := len(bases) > 1
	if a.models[a.dialogID] == nil && base != nil {
		a.dialogID = s(base, "id")
		a.envDraft = kvRows(base, "variables")
	}
	ui.Row(c).Height(470).AlignItems(ui.Stretch).Children(func() {
		sidebar := ui.Scroll(c).Width(220).Shrink(0).Padding(8, 8).Gap(1).Background(p.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
			if multiple {
				for _, env := range bases {
					a.environmentRow(c, p, env, 0, true)
				}
				return
			}
			if base != nil {
				a.environmentRow(c, p, base, 0, false)
			}
			for _, env := range subs {
				a.environmentRow(c, p, env, 1, false)
			}
		})
		sidebar.ContextMenu(func(m *ui.Menu) {
			if !multiple && base != nil && m.Item(locales.CreateSubEnvironment()).Chosen() {
				a.newEnvironment()
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if multiple {
				ui.Box(c).Padding(12).Children(func() {
					ui.Row(c).Padding(8, 10).Radius(6).Border(1, p.notice.Alpha(.5)).Background(p.notice.Alpha(.1)).Children(func() {
						ui.Text(c, locales.ThereAreMultipleBaseEnvironmentsFor()).FontSize(12).Grow(1).MinWidth(0)
					})
				})
			}
			if env := a.models[a.dialogID]; env != nil {
				a.environmentEditor(c, p, env)
			}
		})
	})
}

func (a *App) baseEnvironments() []engine.Object {
	var bases []engine.Object
	for _, env := range a.list("environment") {
		if s(env, "parentModel") == "workspace" {
			bases = append(bases, env)
		}
	}
	return bases
}

func (a *App) selectEnvironment(id string) {
	a.saveEnvironment()
	a.dialogID = id
	a.envDraft = kvRows(a.models[id], "variables")
}

func (a *App) environmentRow(c *ui.Context, p colors, env engine.Object, depth int, multiple bool) {
	id := s(env, "id")
	isBase := s(env, "parentModel") == "workspace"
	selected := id == a.dialogID
	row := ui.Row(c).Key(id).Height(30).Padding(0, 4, 0, float32(8+depth*14)).Gap(6).Radius(4).Focusable().Label(s(env, "name"))
	switch {
	case selected:
		row.Background(p.border.Alpha(.55))
	case row.Hovered():
		row.Background(p.border.Alpha(.3))
	}
	row.Children(func() {
		if multiple {
			icon(c, "alert").FontSize(13).TextColor(p.notice)
		} else if color, ok := envColor(s(env, "color"), p); ok {
			colorDot(c, color, 8)
		}
		if b(env, "public") && !isBase {
			icon(c, "eye").FontSize(13).TextColor(p.subtle).Tooltip(locales.SharableEnvironmentsAreIncludedInDirectory())
		}
		ui.Text(c, s(env, "name")).FontSize(13).SingleLine().Grow(1).MinWidth(0)
		if isBase && !multiple && smallIconButton(c, "plusCircle", locales.AddSubEnvironment()).Opacity(.6).Clicked() {
			a.newEnvironment()
		}
	})
	if row.Clicked() {
		a.selectEnvironment(id)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if !isBase {
			if m.Item(locales.Rename()).Chosen() {
				a.saveEnvironment()
				a.prompt("rename", locales.RenameEnvironment(), s(env, "name"), id)
			}
			if m.Item(locales.Duplicate()).Chosen() {
				a.saveEnvironment()
				a.run(func() (func(), error) {
					created, err := a.Engine.Duplicate(a.ctx, id)
					if err != nil {
						return nil, err
					}
					m, err := a.Engine.Store.Get(a.ctx, created)
					return func() { a.applyModel(m); a.selectEnvironment(created) }, err
				})
			}
			label := locales.AssignColor()
			if s(env, "color") != "" {
				label = locales.ChangeColor()
			}
			if m.Item(label).Chosen() {
				a.saveEnvironment()
				a.environmentColor = s(env, "color")
				a.prompt("environment_color", locales.EnvironmentColor(), "", id)
			}
		}
		label := locales.MakeSharable()
		if b(env, "public") {
			label = locales.MakePrivate()
		}
		if m.Item(label).Chosen() {
			a.setEnvironmentField(id, "public", !b(env, "public"))
		}
		if (!isBase || multiple) && m.Item(locales.Delete()).Chosen() {
			a.saveEnvironment()
			a.prompt("delete", locales.DeleteName(s(env, "name")), s(env, "name"), id)
		}
		if isBase && !multiple {
			m.Separator()
			if m.Item(locales.CreateSubEnvironment()).Chosen() {
				a.newEnvironment()
			}
		}
	})
}

func (a *App) setEnvironmentField(id, key string, value any) {
	m := a.models[id]
	if m == nil {
		return
	}
	copy := deepCopy(m)
	copy[key] = value
	a.applyModel(copy)
	a.saveModel(copy)
}

func (a *App) environmentEditor(c *ui.Context, p colors, env engine.Object) {
	a.environmentEditorFor(c, p, env, &a.envDraft, a.saveEnvironment, false)
}

func (a *App) environmentEditorFor(c *ui.Context, p colors, env engine.Object, rows *[]KV, save func(), hideName bool) {
	id := s(env, "id")
	isBase := s(env, "parentModel") == "workspace"
	encrypted := a.models[s(env, "workspaceId")]["encryptionKeyChallenge"] != nil
	ui.Column(c).Grow(1).MinWidth(0).Padding(12, 16, 12, 16).Gap(10).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if color, ok := envColor(s(env, "color"), p); ok {
				dot := ui.ButtonBase(c).Label(locales.ChangeColor()).Size(14, 14).Children(func() { colorDot(c, color, 12) })
				if dot.Clicked() {
					a.saveEnvironment()
					a.environmentColor = s(env, "color")
					a.prompt("environment_color", locales.EnvironmentColor(), "", id)
				}
			}
			if !hideName {
				ui.Text(c, s(env, "name")).FontSize(17).FontWeight(600).SingleLine().MinWidth(0)
			}
			pill := func(label string) ui.Element {
				button := ui.ButtonBase(c).Label(label).Height(24).Padding(0, 10).Radius(12).Border(1, p.border)
				if button.Hovered() {
					button.Background(p.border.Alpha(.4))
				}
				button.Children(func() { ui.Text(c, label).FontSize(12).TextColor(p.muted) })
				return button
			}
			if encrypted {
				if pill(locales.EncryptionSettings()).Clicked() {
					a.prompt("workspace_settings", locales.WorkspaceSettings(), "", s(env, "workspaceId"))
				}
			} else {
				label := locales.ShowValues()
				if a.showEnvValues {
					label = locales.HideValues()
				}
				if pill(label).Clicked() {
					a.showEnvValues = !a.showEnvValues
				}
			}
			if !isBase {
				label := locales.Private()
				if b(env, "public") {
					label = locales.Sharable()
				}
				if pill(label).Tooltip(locales.SharableEnvironmentsAreIncludedInDirectory()).Clicked() {
					a.setEnvironmentField(id, "public", !b(env, "public"))
				}
			}
		})
		if b(env, "public") && !isBase && !a.dismissed["warn-unencrypted-"+id] && !allSecure(*rows) {
			ui.Row(c).Padding(8, 10).Gap(10).Radius(6).Border(1, p.notice.Alpha(.5)).Background(p.notice.Alpha(.1)).Children(func() {
				ui.Text(c, locales.ThisSharableEnvironmentContainsPlainText()).FontSize(12).Grow(1)
				if ui.Button(c, locales.EncryptVariables()).FontSize(12).Clicked() {
					a.encryptEnvironment(id)
				}
				if smallIconButton(c, "close", locales.Dismiss()).Clicked() {
					if a.dismissed == nil {
						a.dismissed = map[string]bool{}
					}
					a.dismissed["warn-unencrypted-"+id] = true
				}
			})
		}
		dirty := false
		a.kvMask = !encrypted && !a.showEnvValues
		a.kvEditor(c, p, rows, "Variable", "Value", &dirty)
		a.kvMask = false
		if dirty {
			save()
		}
	})
}

func allSecure(rows []KV) bool {
	for _, row := range rows {
		value := strings.TrimSpace(row.Value)
		if value != "" && (!strings.HasPrefix(value, "${[") || !strings.Contains(value, "secure(")) {
			return false
		}
	}
	return true
}

func (a *App) encryptEnvironment(id string) {
	workspace := s(a.models[id], "workspaceId")
	if !a.workspaceKeySet(workspace) {
		a.withEncryptionEnabled(workspace, func() { a.encryptEnvironment(id) })
		return
	}
	rows := slices.Clone(a.envDraft)
	a.run(func() (func(), error) {
		for i, row := range rows {
			value := strings.TrimSpace(row.Value)
			if value == "" || strings.Contains(value, "secure(") {
				continue
			}
			secured, err := a.Engine.SecureValue(a.ctx, workspace, row.Value)
			if err != nil {
				return nil, err
			}
			rows[i].Value = secured
		}
		return func() {
			if a.dialogID == id {
				a.envDraft = rows
				a.saveEnvironment()
			}
		}, nil
	})
}

func (a *App) saveEnvironment() {
	if m := a.models[a.dialogID]; m != nil && s(m, "model") == "environment" {
		a.saveEnvironmentRows(a.dialogID, a.envDraft)
	}
}

func (a *App) saveEnvironmentRows(id string, rows []KV) {
	if m := a.models[id]; m != nil {
		copy := deepCopy(m)
		copy["variables"] = rowObjects(rows)
		a.applyModel(copy)
		a.saveModel(copy)
	}
}

func (a *App) newEnvironment() {
	a.saveEnvironment()
	a.newEnv = newEnvironmentForm{}
	a.prompt("new_environment", locales.NewEnvironment(), "", "")
}

type newEnvironmentForm struct {
	name     string
	sharable bool
	color    string
}

func (a *App) newEnvironmentDialog(c *ui.Context, p colors) {
	f := &a.newEnv
	ui.Column(c).Padding(16, 20).Gap(14).Children(func() {
		ui.Text(c, locales.CreateMultipleEnvironmentsWithDifferentSets()).FontSize(12).TextColor(p.muted)
		labeled(c, p, locales.NameTitle(), func() {
			ui.TextInput(c, &f.name).Label(locales.NameTitle()).Placeholder(locales.Production()).AutoFocus().FillWidth()
		})
		ui.Column(c).Gap(4).Children(func() {
			ui.Checkbox(c, &f.sharable, locales.ShareThisEnvironment())
			ui.Text(c, locales.SharableEnvironmentsAreIncludedInData()).FontSize(12).TextColor(p.muted).Padding(0, 0, 0, 24)
		})
		labeled(c, p, locales.Color(), func() {
			ui.Text(c, locales.SelectAColorToBeDisplayed()).FontSize(12).TextColor(p.muted)
			environmentColorPicker(c, p, &f.color)
		})
		ui.Row(c).Justify(ui.End).Children(func() {
			create := ui.ButtonBase(c).Label(locales.CreateEnvironment()).Height(30).Padding(0, 14).Gap(6).Radius(5).Border(1, p.border).Disabled(strings.TrimSpace(f.name) == "")
			create.Children(func() {
				if color, ok := envColor(f.color, p); ok {
					colorDot(c, color, 10)
				}
				ui.Text(c, locales.CreateEnvironment()).FontSize(13)
			})
			if create.Clicked() {
				workspace, form := a.workspace, *f
				a.run(func() (func(), error) {
					m := engine.Object{"model": "environment", "workspaceId": workspace, "parentModel": "environment", "name": strings.TrimSpace(form.name), "public": form.sharable, "variables": []any{}, "color": nilIfEmpty(form.color)}
					saved, err := a.Engine.Save(a.ctx, m)
					return func() {
						a.applyModel(saved)
						a.openEnvironmentsAt(s(saved, "id"))
					}, err
				})
			}
		})
	})
}

func environmentColorPicker(c *ui.Context, p colors, color *string) {
	selected := ""
	switch {
	case *color == "":
	case strings.HasPrefix(*color, "var(--"):
		selected = strings.TrimSuffix(strings.TrimPrefix(*color, "var(--"), ")")
	default:
		selected = "custom"
	}
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			for _, option := range append(append([]string{""}, environmentColors...), "custom") {
				label := environmentColorLabel(option)
				swatch := ui.ButtonBase(c).Key(cmpOr(option, "none")).Label(label).Tooltip(label).Size(28, 28).Radius(14).Opacity(.6)
				if option == selected {
					swatch.Opacity(1).Border(2, p.text)
				}
				switch option {
				case "":
					swatch.Border(1, p.muted)
				case "custom":
					swatch.Gradient(p.red, p.blue, 135)
				default:
					value, _ := envColor("var(--"+option+")", p)
					swatch.Background(value)
				}
				if swatch.Clicked() {
					switch option {
					case "":
						*color = ""
					case "custom":
						*color = "#ffffff"
					default:
						*color = "var(--" + option + ")"
					}
				}
			}
		})
		if selected == "custom" {
			value, ok := envColor(*color, p)
			if !ok {
				value = ui.Hex("#ffffff")
			}
			if ui.ColorPicker(c, &value).Label(locales.CustomColor()).Changed() {
				*color = hexString(value)
			}
		}
	})
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func hexString(color ui.Color) string {
	const digits = "0123456789abcdef"
	b := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, v := range []uint8{color.R, color.G, color.B} {
		b[1+i*2], b[2+i*2] = digits[v>>4], digits[v&15]
	}
	return string(b)
}

func (a *App) environmentColorDialog(c *ui.Context, p colors) {
	id := a.dialogID
	ui.Column(c).Padding(16, 20).Gap(16).Children(func() {
		ui.Text(c, locales.ThisColorWillBeUsedTo()).FontSize(12).Padding(8, 10).Radius(6).Background(p.border.Alpha(.3))
		environmentColorPicker(c, p, &a.environmentColor)
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Save()).Clicked() {
				a.setEnvironmentField(id, "color", nilIfEmpty(a.environmentColor))
				a.openEnvironmentsAt(id)
			}
		})
	})
}

func environmentColorLabel(option string) string {
	switch option {
	case "":
		return locales.NoColor()
	case "danger":
		return locales.Danger()
	case "warning":
		return locales.Warning()
	case "notice":
		return locales.Notice()
	case "success":
		return locales.Success()
	case "primary":
		return locales.Primary()
	case "info":
		return locales.InfoLower()
	case "secondary":
		return locales.Secondary()
	case "custom":
		return locales.CustomLower()
	}
	return option
}
