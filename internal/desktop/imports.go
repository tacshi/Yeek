package desktop

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type importDialog struct {
	inputs                          []engine.ImportInput
	destination                     engine.ImportDestination
	folders                         []engine.Object
	entryKind, entry, error, detail string
	plan                            *engine.ImportPlan
	cancel                          context.CancelFunc
	generation                      uint64
	busy, applying                  bool
	collapsed                       map[string]bool
}

func (a *App) openImport(inputs []engine.ImportInput, destination engine.ImportDestination) {
	if a.deferUntilInputs(func() { a.openImport(inputs, destination) }) {
		return
	}
	if a.imports != nil && a.imports.cancel != nil {
		a.imports.cancel()
	}
	a.imports = &importDialog{inputs: inputs, destination: destination, entryKind: "URL", collapsed: map[string]bool{}}
	a.prompt("imports", "Import Data", "", "")
	a.loadImportFolders(a.imports)
}
func (a *App) loadImportFolders(state *importDialog) {
	id := state.destination.WorkspaceID
	state.folders = nil
	if id == "" {
		return
	}
	a.run(func() (func(), error) {
		folders, err := a.Engine.Store.List(a.ctx, "folder", id)
		return func() {
			if a.imports == state && state.destination.WorkspaceID == id {
				state.folders = folders
			}
		}, err
	})
}
func (a *App) addImportFiles(directory bool) {
	state := a.imports
	if state == nil || state.busy {
		return
	}
	a.background(func() (func(), error) {
		options := mygo.OpenDialogOptions{Parent: a.Window, Title: "Import Collection", Multiple: true, Directory: directory}
		if !directory {
			options.Filters = []mygo.FileFilter{{Name: "Collections", Extensions: []string{"json", "yaml", "yml", "yeek", "yaak", "bru"}}}
		}
		paths, err := mygo.Dialog.Open(options)
		return func() {
			if a.imports != state || !a.dialogOpen {
				return
			}
			if err != nil {
				state.error = err.Error()
				return
			}
			for _, path := range paths {
				state.inputs = append(state.inputs, engine.ImportInput{Origin: path})
			}
			state.plan = nil
		}, nil
	})
}
func (a *App) previewImport() {
	if a.deferUntilInputs(a.previewImport) {
		return
	}
	state := a.imports
	if state == nil || state.busy {
		return
	}
	if strings.TrimSpace(state.entry) != "" {
		a.addImportEntry(state)
	}
	if len(state.inputs) == 0 {
		state.error = "Add a collection source."
		return
	}
	a.saveActive()
	state.error, state.detail = "", ""
	state.busy = true
	state.generation++
	generation := state.generation
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	inputs, destination := slices.Clone(state.inputs), state.destination
	a.background(func() (func(), error) {
		defer cancel()
		plan, err := a.Engine.PlanImport(ctx, inputs, destination)
		return func() {
			if a.imports != state || state.generation != generation {
				return
			}
			state.busy = false
			state.cancel = nil
			if err != nil {
				state.error = err.Error()
				return
			}
			state.plan = plan
		}, nil
	})
}
func (a *App) addImportEntry(state *importDialog) {
	if a.deferUntilInputs(func() { a.addImportEntry(state) }) {
		return
	}
	value := strings.TrimSpace(state.entry)
	if value == "" {
		return
	}
	input := engine.ImportInput{Kind: "url", Origin: value}
	if state.entryKind == "Text / cURL" {
		input = engine.ImportInput{Kind: "text", Content: state.entry}
	}
	state.inputs = append(state.inputs, input)
	state.entry, state.error = "", ""
	state.plan = nil
}
func (a *App) applyImport() {
	if a.deferUntilInputs(a.applyImport) {
		return
	}
	state := a.imports
	if state == nil || state.plan == nil || state.busy || !canApplyImport(state.plan) {
		return
	}
	a.saveActive()
	state.busy, state.applying, state.error = true, true, ""
	plan := state.plan
	workspace := plan.Destination.WorkspaceID
	if workspaces := plan.NewWorkspaces(); len(workspaces) > 0 {
		workspace = s(workspaces[0], "id")
	}
	a.background(func() (func(), error) {
		_, err := a.Engine.CommitImport(a.ctx, plan)
		var models []engine.Object
		if err == nil {
			models, err = a.Engine.Snapshot(a.ctx, workspace)
		}
		return func() {
			state.busy, state.applying = false, false
			if err != nil {
				state.error = err.Error()
				return
			}
			for _, item := range plan.Items {
				if item.Selected && item.Action != "unchanged" && (item.Action != "conflict" || item.Resolution == "take_source") {
					delete(a.drafts, item.ID)
				}
			}
			if a.workspace != workspace {
				a.active = ""
				a.tabs = nil
				a.environment = ""
				a.cookieJar = ""
			}
			a.workspace = workspace
			a.replace(models)
			a.tabs = slices.DeleteFunc(a.tabs, func(id string) bool { return a.models[id] == nil })
			if a.models[a.active] == nil {
				a.active = ""
			} else if a.drafts[a.active] == nil {
				a.drafts[a.active] = newDraft(a.models[a.active])
			}
			a.activeCookieJar()
			if a.imports == state {
				a.dialogOpen = false
			}
		}, nil
	})
}
func (a *App) reimportSource(source engine.Object) {
	input := engine.ImportInput{Origin: s(source, "origin"), SourceWorkspaceID: s(source, "sourceWorkspaceId")}
	a.openImport([]engine.ImportInput{input}, engine.ImportDestination{WorkspaceID: s(source, "workspaceId"), FolderID: s(source, "destinationFolderId")})
	a.previewImport()
}
func (a *App) unlinkImportSource(id string) {
	state := a.imports
	a.run(func() (func(), error) {
		err := a.Engine.Delete(a.ctx, id)
		return func() {
			if err != nil {
				if state != nil {
					state.error = err.Error()
				}
				return
			}
			delete(a.models, id)
		}, nil
	})
}

func (a *App) importDialog(c *ui.Context, p colors) {
	state := a.imports
	if state == nil {
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Gap(0).Children(func() {
		if state.plan == nil {
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(20).Gap(16).Disabled(state.busy).Children(func() {
				a.importDestination(c, p, state)
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "Sources").FontWeight(600).Grow(1)
					if ui.Button(c, "Add Files…").Clicked() {
						a.addImportFiles(false)
					}
					if ui.Button(c, "Add Bruno Folder…").Clicked() {
						a.addImportFiles(true)
					}
				})
				remove := -1
				for i, input := range state.inputs {
					ui.Row(c).Key(fmt.Sprint(i)).Gap(8).Children(func() {
						label := input.Origin
						if input.Kind == "text" {
							label = "Pasted text: " + strings.ReplaceAll(input.Content, "\n", " ")
						}
						ui.Text(c, label).Grow(1).MaxLines(2).FontSize(12)
						if smallIconButton(c, "close", fmt.Sprintf("Remove source %d", i+1)).Clicked() {
							remove = i
						}
					})
				}
				if remove >= 0 {
					state.inputs = slices.Delete(state.inputs, remove, remove+1)
				}
				ui.Row(c).Gap(8).Children(func() {
					kind := state.entryKind
					if ui.Select(c, &kind, []string{"URL", "Text / cURL"}).Label("Source type").Width(150).Changed() {
						a.deferUntilInputs(func() { state.entryKind = kind })
					}
					if state.entryKind == "URL" {
						ui.TextInput(c, &state.entry).Label("Collection URL").Placeholder("https://example.com/openapi.json").Grow(1)
					}
					if ui.Button(c, "Add Source").Disabled(strings.TrimSpace(state.entry) == "").Clicked() {
						a.addImportEntry(state)
					}
				})
				if state.entryKind == "Text / cURL" {
					ui.TextArea(c, &state.entry).Label("Collection text").Font("monospace").FontSize(12).Height(160)
				}
				if sources := a.list("import_source"); len(sources) > 0 {
					ui.Text(c, "Linked Sources").FontWeight(600)
					for _, source := range sources {
						ui.Row(c).Key(s(source, "id")).Gap(8).Children(func() {
							ui.Column(c).Grow(1).Gap(3).Children(func() {
								ui.Text(c, cmp.Or(s(source, "originLabel"), s(source, "importer")))
								ui.Text(c, s(source, "origin")).MaxLines(2).FontSize(11).TextColor(p.muted)
							})
							if ui.Button(c, "Reimport").Label("Reimport " + s(source, "originLabel")).Clicked() {
								a.reimportSource(source)
							}
							if ui.Button(c, "Unlink").Label("Unlink " + s(source, "originLabel")).Clicked() {
								a.unlinkImportSource(s(source, "id"))
							}
						})
					}
				}
			})
		} else {
			a.importPreview(c, p, state)
		}
		if state.error != "" {
			ui.Text(c, state.error).TextColor(p.red).Padding(8, 20).MaxLines(3).Selectable()
		}
		ui.Row(c).Padding(14, 20).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
			if state.busy {
				ui.Text(c, map[bool]string{true: "Importing…", false: "Reading sources…"}[state.applying]).TextColor(p.muted)
			}
			ui.Box(c).Grow(1)
			if state.plan != nil {
				if ui.Button(c, "Edit Sources").Disabled(state.busy).Clicked() {
					state.plan = nil
					state.detail = ""
					state.error = ""
				}
				if ui.Button(c, "Refresh Preview").Disabled(state.busy).Clicked() {
					a.previewImport()
				}
				if ui.PrimaryButton(c, "Apply Import").Disabled(state.busy || !canApplyImport(state.plan)).Clicked() {
					a.applyImport()
				}
			} else if ui.PrimaryButton(c, "Preview Import").Disabled(state.busy).Clicked() {
				a.previewImport()
			}
		})
	})
}

func (a *App) importDestination(c *ui.Context, p colors, state *importDialog) {
	ui.Text(c, "Destination").FontWeight(600)
	ui.Row(c).Gap(10).Children(func() {
		label := "New Workspace"
		if state.destination.WorkspaceID != "" {
			label = cmp.Or(s(a.models[state.destination.WorkspaceID], "name"), "Workspace")
		}
		ui.MenuButton(c, label, func(menu *ui.Menu) {
			set := func(id string) {
				state.destination = engine.ImportDestination{WorkspaceID: id}
				a.loadImportFolders(state)
			}
			if menu.Item("New Workspace").Checked(state.destination.WorkspaceID == "").Chosen() {
				set("")
			}
			for _, workspace := range a.list("workspace") {
				if menu.Item(s(workspace, "name")).Checked(state.destination.WorkspaceID == s(workspace, "id")).Chosen() {
					set(s(workspace, "id"))
				}
			}
		}).Label("Import destination workspace").Grow(1).Basis(0).Justify(ui.SpaceBetween).Padding(5, 10)
		if state.destination.WorkspaceID != "" {
			label := "Workspace Root"
			for _, folder := range state.folders {
				if s(folder, "id") == state.destination.FolderID {
					label = s(folder, "name")
				}
			}
			ui.MenuButton(c, label, func(menu *ui.Menu) {
				if menu.Item("Workspace Root").Checked(state.destination.FolderID == "").Chosen() {
					state.destination.FolderID = ""
				}
				var add func(string, int)
				add = func(parent string, depth int) {
					if depth > 32 {
						return
					}
					for _, f := range state.folders {
						if s(f, "folderId") == parent {
							if menu.Item(strings.Repeat("  ", depth) + s(f, "name")).Checked(state.destination.FolderID == s(f, "id")).Chosen() {
								state.destination.FolderID = s(f, "id")
							}
							add(s(f, "id"), depth+1)
						}
					}
				}
				add("", 0)
			}).Label("Import destination folder").Grow(1).Basis(0).Justify(ui.SpaceBetween).Padding(5, 10)
		}
	})
}

func (a *App) importPreview(c *ui.Context, p colors, state *importDialog) {
	plan := state.plan
	ui.Row(c).Padding(12, 20).Gap(12).Disabled(state.busy).Children(func() {
		label := "Import into " + s(a.models[plan.Destination.WorkspaceID], "name")
		if plan.Destination.WorkspaceID == "" {
			label = "Select a workspace to import"
		}
		if workspaces := plan.NewWorkspaces(); len(workspaces) > 0 {
			names := []string{}
			for _, w := range workspaces {
				names = append(names, s(w, "name"))
			}
			label = "Create workspace: " + strings.Join(names, ", ")
		}
		ui.Text(c, label).Grow(1).MaxLines(2).FontWeight(600)
		if ui.Button(c, "Select Changes").Clicked() {
			for _, item := range plan.Items {
				item.Selected = slices.Contains([]string{"create", "update", "conflict"}, item.Action)
			}
		}
		if ui.Button(c, "Deselect All").Clicked() {
			for _, item := range plan.Items {
				item.Selected = false
			}
		}
	})
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).MinWidth(0).MinHeight(0).Padding(8, 16).Gap(3).Disabled(state.busy).Children(func() {
			for _, warning := range plan.Warnings {
				ui.Text(c, warning.Title+": "+warning.Detail).FontSize(12).TextColor(p.muted).MaxLines(3).Padding(4)
			}
			ids := map[string]bool{}
			childrenByParent := map[string][]*engine.ImportItem{}
			for _, item := range plan.Items {
				ids[item.SourceID+":"+item.ID] = true
				key := item.SourceID + ":" + item.ParentID
				childrenByParent[key] = append(childrenByParent[key], item)
			}
			var row func(*engine.ImportItem, int)
			row = func(item *engine.ImportItem, depth int) {
				children := len(childrenByParent[item.SourceID+":"+item.ID]) > 0
				background, actionColor := ui.Transparent, p.muted
				if state.detail == item.Key() {
					background = p.accent.Alpha(.12)
				}
				if item.Action == "delete" || item.Action == "conflict" {
					actionColor = p.red
				}
				ui.Row(c).Key(item.Key()).Height(30).Gap(6).Padding(0, 4, 0, float32(4+min(depth, 12)*16)).Radius(4).Background(background).Children(func() {
					if children {
						if chevronToggle(c, p, !state.collapsed[item.ID], item.Name).Clicked() {
							state.collapsed[item.ID] = !state.collapsed[item.ID]
						}
					} else {
						ui.Box(c).Width(20)
					}
					if ui.Checkbox(c, &item.Selected, "").Label("Import " + item.Name).Disabled(item.Action == "unchanged").Changed() {
						selectImportItem(plan, item)
					}
					if ui.ButtonBase(c).Grow(1).MinWidth(0).Justify(ui.Start).Children(func() { ui.Text(c, cmp.Or(item.Name, item.Kind)).MaxLines(1) }).Label("Inspect " + item.Name).Clicked() {
						state.detail = item.Key()
					}
					ui.Text(c, importActionLabel(item.Action)).FontSize(11).TextColor(actionColor)
				})
				if children && !state.collapsed[item.ID] {
					for _, child := range childrenByParent[item.SourceID+":"+item.ID] {
						row(child, depth+1)
					}
				}
			}
			for _, source := range plan.Sources {
				if len(plan.Sources) > 1 {
					ui.Text(c, source.Label).FontWeight(600).Padding(10, 4)
				}
				for _, item := range plan.Items {
					if item.SourceID == source.ID && !ids[item.SourceID+":"+item.ParentID] {
						row(item, 0)
					}
				}
			}
		})
		if state.detail != "" {
			for _, item := range plan.Items {
				if item.Key() == state.detail {
					a.importItemDetails(c, p, state, item)
					break
				}
			}
		}
	})
}
func importActionLabel(action string) string {
	return map[string]string{"create": "New", "update": "Update", "delete": "Delete", "unchanged": "Unchanged", "ignored": "Ignored", "keep_local": "Local changes", "conflict": "Conflict"}[action]
}
func selectImportItem(plan *engine.ImportPlan, item *engine.ImportItem) {
	for _, child := range plan.Items {
		if child.ParentID == item.ID && child.SourceID == item.SourceID && child.Action != "unchanged" {
			child.Selected = item.Selected
			selectImportChildren(plan, child)
		}
	}
	if item.Selected {
		id := item.ParentID
		for range len(plan.Items) {
			index := slices.IndexFunc(plan.Items, func(parent *engine.ImportItem) bool { return parent.ID == id && parent.SourceID == item.SourceID })
			if index < 0 {
				break
			}
			parent := plan.Items[index]
			if parent.Action == "create" || parent.Action == "ignored" {
				parent.Selected = true
			}
			id = parent.ParentID
		}
	}
}
func selectImportChildren(plan *engine.ImportPlan, item *engine.ImportItem) {
	for _, child := range plan.Items {
		if child.ParentID == item.ID && child.SourceID == item.SourceID && child.Action != "unchanged" {
			child.Selected = item.Selected
			selectImportChildren(plan, child)
		}
	}
}
func (a *App) importItemDetails(c *ui.Context, p colors, state *importDialog, item *engine.ImportItem) {
	ui.Scroll(c).Width(365).MinHeight(0).Padding(16).Gap(10).BorderWidth(0, 0, 0, 1).BorderColor(p.border).Children(func() {
		ui.Text(c, cmp.Or(item.Name, item.Kind)).FontWeight(600)
		ui.Text(c, strings.ReplaceAll(item.Kind, "_", " ")+" · "+importActionLabel(item.Action)).FontSize(12).TextColor(p.muted)
		if item.Action == "conflict" {
			choice := "Keep Mine"
			if item.Resolution == "take_source" {
				choice = "Take Source"
			}
			if ui.Select(c, &choice, []string{"Keep Mine", "Take Source"}).Label("Conflict resolution").FillWidth().Disabled(state.busy).Changed() {
				item.Resolution = "keep_mine"
				if choice == "Take Source" {
					item.Resolution = "take_source"
					item.Selected = true
				}
			}
		}
		fields := item.ChangedFields
		if len(fields) == 0 {
			fields = []string{"name", "url", "method", "description", "headers", "urlParameters", "authentication", "variables", "body", "message"}
		}
		for _, field := range fields {
			if emptyImportDetail(item.Before[field]) && emptyImportDetail(item.After[field]) {
				continue
			}
			ui.Text(c, fieldLabel(field)).FontSize(12).FontWeight(600)
			for _, side := range []struct {
				name  string
				model engine.Object
			}{{"Current", item.Before}, {"Source", item.After}} {
				if len(side.model) == 0 {
					continue
				}
				data, _ := json.Marshal(side.model[field], jsontext.WithIndent("  "), json.Deterministic(true))
				ui.Text(c, side.name).FontSize(10).TextColor(p.muted)
				ui.Text(c, string(data)).Font("monospace").FontSize(11).Selectable().MaxLines(24)
			}
		}
	})
}
func emptyImportDetail(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func canApplyImport(plan *engine.ImportPlan) bool {
	if plan == nil {
		return false
	}
	if plan.Destination.WorkspaceID != "" {
		return true
	}
	return len(plan.NewWorkspaces()) > 0
}
