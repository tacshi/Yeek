package desktop

import (
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

type exportDialog struct {
	selected             map[string]bool
	includePrivate, busy bool
	error                string
}

func (a *App) exportFile() {
	if a.deferUntilInputs(a.exportFile) {
		return
	}
	a.saveActive()
	a.exports = &exportDialog{selected: map[string]bool{a.workspace: true}}
	a.prompt("exports", locales.ExportData(), "", "")
}

func exportOptions(state *exportDialog, workspaces []engine.Object) engine.ExportOptions {
	options := engine.ExportOptions{IncludePrivateEnvironments: state.includePrivate}
	for _, workspace := range workspaces {
		if state.selected[s(workspace, "id")] {
			options.WorkspaceIDs = append(options.WorkspaceIDs, s(workspace, "id"))
		}
	}
	return options
}
func exportFilename(workspaces []engine.Object, options engine.ExportOptions) string {
	name := "workspaces"
	if len(options.WorkspaceIDs) == 1 {
		for _, w := range workspaces {
			if s(w, "id") == options.WorkspaceIDs[0] {
				name = s(w, "name")
			}
		}
	}
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "workspace"
	}
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return "yeek." + name + ".json"
}

func (a *App) saveExport() {
	if a.deferUntilInputs(a.saveExport) {
		return
	}
	state := a.exports
	if state == nil || state.busy {
		return
	}
	a.saveActive()
	workspaces := a.list("workspace")
	options := exportOptions(state, workspaces)
	if len(options.WorkspaceIDs) == 0 {
		state.error = locales.SelectAWorkspaceToExport()
		return
	}
	state.busy, state.error = true, ""
	filename := exportFilename(workspaces, options)
	a.background(func() (func(), error) {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: locales.ExportData(), DefaultPath: filename, Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		if err == nil && path != "" {
			err = a.Engine.ExportToFile(a.ctx, path, options)
		}
		return func() {
			state.busy = false
			if err != nil {
				state.error = err.Error()
				return
			}
			if path != "" {
				a.dialogOpen = false
				a.showToast("", locales.DataExportSuccessful(), "success", 5*time.Second)
			}
		}, nil
	})
}

func (a *App) exportDialog(c *ui.Context, p colors) {
	state := a.exports
	if state == nil {
		return
	}
	workspaces := a.list("workspace")
	slices.SortStableFunc(workspaces, func(a1, a2 engine.Object) int {
		switch a.workspace {
		case s(a1, "id"):
			return -1
		case s(a2, "id"):
			return 1
		}
		return 0
	})
	selected := 0
	for _, w := range workspaces {
		if state.selected[s(w, "id")] {
			selected++
		}
	}
	all := selected == len(workspaces) && selected > 0
	toggleAll := func() {
		state.selected = map[string]bool{}
		if !all {
			for _, w := range workspaces {
				state.selected[s(w, "id")] = true
			}
		}
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 24, 16, 24).Gap(12).Disabled(state.busy).Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			ui.Row(c).FillWidth().Padding(8, 0, 8, 4).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border.Alpha(.6)).Children(func() {
				checked := all
				if checkbox(c, p, &checked, selected > 0 && !all, locales.AllWorkspaces()).Changed() {
					toggleAll()
				}
				header := ui.ButtonBase(c).Label(locales.Workspace()).Grow(1).Justify(ui.Start)
				header.Children(func() { ui.Text(c, locales.Workspace()).FontSize(13).FontWeight(600) })
				if header.Clicked() {
					toggleAll()
				}
			})
			for i, w := range workspaces {
				id := s(w, "id")
				row := ui.Row(c).Key(id).FillWidth().Padding(4, 0, 4, 4).Gap(16)
				if i < len(workspaces)-1 {
					row.BorderWidth(0, 0, 1, 0).BorderColor(p.border.Alpha(.6))
				}
				row.Children(func() {
					checked := state.selected[id]
					if checkbox(c, p, &checked, false, s(w, "name")).Changed() {
						state.selected[id] = checked
					}
					name := s(w, "name")
					if id == a.workspace {
						name = locales.NameCurrentWorkspace(name)
					}
					cell := ui.ButtonBase(c).Label(locales.ExportName(s(w, "name"))).Grow(1).MinWidth(0).Justify(ui.Start)
					cell.Children(func() { ui.Text(c, name).FontSize(13).SingleLine() })
					if cell.Clicked() {
						state.selected[id] = !state.selected[id]
					}
				})
			}
		})
		a.detailsBanner(c, p, locales.ExtraSettings(), true, func() {
			ui.Text(c, locales.ExtraSettings()).FontSize(13)
		}, func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Checkbox(c, &state.includePrivate, locales.IncludePrivateEnvironments()).FontSize(13)
				icon(c, "info").FontSize(12).TextColor(p.subtle).Tooltip(locales.EnvironmentsMarkedAsSharableWillBe())
			})
		})
		if state.error != "" {
			ui.Text(c, state.error).TextColor(p.red).MaxLines(3).Selectable()
		}
	})
	dialogFooter(c, p, nil, func() {
		if ui.Button(c, locales.Cancel()).Clicked() {
			a.dialogOpen = false
		}
		label := locales.ExportCountWorkspaces(selected)
		if ui.PrimaryButton(c, label).Disabled(selected == 0 || state.busy).Clicked() {
			a.saveExport()
		}
	})
}
