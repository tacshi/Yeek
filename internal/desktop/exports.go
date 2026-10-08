package desktop

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type exportDialog struct {
	selected             map[string]bool
	includePrivate, busy bool
	error, savedPath     string
}

func (a *App) exportFile() {
	if a.deferUntilInputs(a.exportFile) {
		return
	}
	a.saveActive()
	a.exports = &exportDialog{selected: map[string]bool{a.workspace: true}}
	a.prompt("exports", "Export Data", "", "")
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
		state.error = "Select a workspace to export."
		return
	}
	state.busy, state.error = true, ""
	filename := exportFilename(workspaces, options)
	a.background(func() (func(), error) {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "Export Data", DefaultPath: filename, Filters: []mygo.FileFilter{{Name: "JSON Collection", Extensions: []string{"json", "yeek", "yaak"}}}})
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
				state.savedPath = path
			}
		}, nil
	})
}

func (a *App) exportDialog(c *ui.Context, p colors) {
	state := a.exports
	if state == nil {
		return
	}
	if state.savedPath != "" {
		ui.Column(c).Padding(20).Gap(15).Children(func() {
			ui.Text(c, "Saved "+filepath.Base(state.savedPath)).FontWeight(600)
			ui.Text(c, state.savedPath).FontSize(12).TextColor(p.muted).Selectable().MaxLines(3)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Show File").Clicked() {
					mygo.Shell.ShowItemInFolder(state.savedPath)
				}
				if ui.PrimaryButton(c, "Done").Clicked() {
					a.dialogOpen = false
				}
			})
		})
		return
	}
	workspaces := a.list("workspace")
	slices.SortStableFunc(workspaces, func(a1, a2 engine.Object) int {
		if s(a1, "id") == a.workspace {
			return -1
		}
		if s(a2, "id") == a.workspace {
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
	ui.Column(c).Padding(20).Gap(14).Disabled(state.busy).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			all := selected == len(workspaces) && selected > 0
			if ui.Checkbox(c, &all, "All Workspaces").Changed() {
				for _, w := range workspaces {
					state.selected[s(w, "id")] = all
				}
			}
		})
		ui.Scroll(c).Height(min(float32(len(workspaces)*38+12), 300)).Gap(4).Children(func() {
			for _, workspace := range workspaces {
				id := s(workspace, "id")
				ui.Row(c).Key(id).Padding(6, 4).Gap(10).Children(func() {
					checked := state.selected[id]
					name := s(workspace, "name")
					if ui.Checkbox(c, &checked, name).Label("Export " + name).Grow(1).Changed() {
						state.selected[id] = checked
					}
					if id == a.workspace {
						ui.Text(c, "Current").TextColor(p.muted).FontSize(11)
					}
				})
			}
		})
		ui.Checkbox(c, &state.includePrivate, "Include private environments")
		if state.error != "" {
			ui.Text(c, state.error).TextColor(p.red).MaxLines(3).Selectable()
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen = false
			}
			label := fmt.Sprintf("Export %d Workspaces…", selected)
			if selected == 1 {
				label = "Export Workspace…"
			}
			if state.busy {
				label = "Saving…"
			}
			if ui.PrimaryButton(c, label).Disabled(selected == 0 || state.busy).Clicked() {
				a.saveExport()
			}
		})
	})
}
