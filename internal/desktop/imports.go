package desktop

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// importSource is Yaak's ImportSourcePath: a file, a folder or a URL (kind
// "" lets the engine tell a path's), or Yeek's pasted text, which has no path.
type importSource struct {
	path, kind, content string
}

func (s importSource) input() engine.ImportInput {
	if s.kind == "text" {
		return engine.ImportInput{Kind: "text", Content: s.content}
	}
	kind := s.kind
	if kind == "folder" || kind == "file" {
		kind = ""
	}
	return engine.ImportInput{Kind: kind, Origin: s.path}
}

// importDetection is Yaak's ImportSourceDetection of a source's format.
type importDetection struct {
	status, importer, message string
}

// importDialog is Yaak's ImportDataDialog: sources, a destination and a
// preview of the plan to commit.
type importDialog struct {
	sources        []importSource
	detections     map[string]*importDetection
	choice, other  string
	originSources  []engine.Object
	originKey      string
	pathInput      string
	showPathInput  bool
	plan           *engine.ImportPlan
	error          string
	busy, applying bool
	toggled        map[string]bool
	generation     uint64
	cancel         context.CancelFunc
	texts          int
}

func (a *App) openImport(inputs []engine.ImportInput, destination engine.ImportDestination) {
	if a.deferUntilInputs(func() { a.openImport(inputs, destination) }) {
		return
	}
	if a.imports != nil && a.imports.cancel != nil {
		a.imports.cancel()
	}
	state := &importDialog{detections: map[string]*importDetection{}, toggled: map[string]bool{}}
	a.imports = state
	a.prompt("imports", "Import Data", "", "")
	if destination.WorkspaceID != "" && destination.WorkspaceID == a.workspace && len(inputs) > 0 {
		state.choice = "current"
	}
	if inputs == nil && a.workspace != "" {
		// The current workspace's linked sources, ready to import again.
		for _, source := range a.list("import_source") {
			if s(source, "workspaceId") == a.workspace {
				inputs = append(inputs, engine.ImportInput{Origin: s(source, "origin")})
			}
		}
	}
	var added []importSource
	for _, input := range inputs {
		if input.Kind == "text" {
			added = append(added, importSource{kind: "text", content: input.Content})
		} else {
			added = append(added, sourcePath(input.Origin))
		}
	}
	a.addImportSources(state, added)
}

// sourcePath is Yaak's toSourcePath: a URL unless it looks like a local path.
func sourcePath(path string) importSource {
	if isImportFilePath(path) {
		return importSource{path: path}
	}
	return importSource{path: path, kind: "url"}
}

// isImportFilePath is Yaak's: a bare hostname is a URL, a local path needs
// an absolute or explicitly relative start.
func isImportFilePath(value string) bool {
	for _, prefix := range []string{"/", "./", "../", "~/", `\\`} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return len(value) > 2 && (value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z') && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

// addImportSources adds the sources not in the list yet and detects their formats.
func (a *App) addImportSources(state *importDialog, added []importSource) {
	state.error = ""
	for _, source := range added {
		if source.kind == "text" {
			state.texts++
			source.path = fmt.Sprintf("Pasted text %d", state.texts)
		}
		if strings.TrimSpace(source.path) == "" || slices.ContainsFunc(state.sources, func(s importSource) bool { return s.path == source.path }) {
			continue
		}
		state.sources = append(state.sources, source)
		detection := &importDetection{status: "loading"}
		state.detections[source.path] = detection
		input := source.input()
		a.background(func() (func(), error) {
			importer, err := a.Engine.DetectImportSource(a.ctx, input)
			return func() {
				if err != nil {
					detection.status, detection.message = "error", err.Error()
					return
				}
				detection.status, detection.importer = "ok", importer
			}, nil
		})
	}
	a.findImportOrigins(state)
}

func (a *App) removeImportSource(state *importDialog, path string) {
	state.error = ""
	state.sources = slices.DeleteFunc(state.sources, func(s importSource) bool { return s.path == path })
	delete(state.detections, path)
	a.findImportOrigins(state)
}

// findImportOrigins looks up the linked sources of the dialog's sources,
// for the destination they were last imported into.
func (a *App) findImportOrigins(state *importDialog) {
	var origins []string
	for _, source := range state.sources {
		if source.kind != "text" {
			origins = append(origins, source.path)
		}
	}
	key := strings.Join(origins, "\x00")
	state.originKey = key
	if len(origins) == 0 {
		state.originSources = nil
		return
	}
	a.background(func() (func(), error) {
		var found []engine.Object
		for _, origin := range origins {
			sources, err := a.Engine.ImportSourcesForOrigin(a.ctx, origin)
			if err != nil {
				return nil, nil
			}
			found = append(found, sources...)
		}
		return func() {
			if state.originKey == key {
				state.originSources = found
			}
		}, nil
	})
}

// linkedWorkspace is the one workspace the sources were last imported into, if one.
func (a *App) linkedWorkspace(state *importDialog) engine.Object {
	var ids []string
	for _, source := range state.originSources {
		if id := s(source, "workspaceId"); !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		return nil
	}
	if w := a.models[ids[0]]; s(w, "model") == "workspace" {
		return w
	}
	return nil
}

// importDestinationKind is the destination chosen, else Yaak's default:
// back into the current workspace when the sources are linked to it, a new
// one otherwise.
func (a *App) importDestinationKind(state *importDialog) string {
	if state.choice != "" {
		return state.choice
	}
	if linked := a.linkedWorkspace(state); linked != nil && s(linked, "id") == a.workspace && a.workspace != "" {
		return "current"
	}
	return "new"
}

func (a *App) importDestinationWorkspace(state *importDialog) string {
	switch a.importDestinationKind(state) {
	case "current":
		return a.workspace
	case "other":
		return state.other
	}
	return ""
}

func (a *App) previewImport() {
	if a.deferUntilInputs(a.previewImport) {
		return
	}
	state := a.imports
	if state == nil || state.busy || len(state.sources) == 0 {
		return
	}
	a.saveActive()
	state.error = ""
	state.busy = true
	state.generation++
	generation := state.generation
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	var inputs []engine.ImportInput
	for _, source := range state.sources {
		inputs = append(inputs, source.input())
	}
	destination := engine.ImportDestination{WorkspaceID: a.importDestinationWorkspace(state)}
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
			state.toggled = map[string]bool{}
		}, nil
	})
}

func (a *App) applyImport() {
	if a.deferUntilInputs(a.applyImport) {
		return
	}
	state := a.imports
	if state == nil || state.plan == nil || state.busy {
		return
	}
	a.saveActive()
	state.busy, state.applying, state.error = true, true, ""
	plan := state.plan
	a.background(func() (func(), error) {
		imported, err := a.Engine.CommitImport(a.ctx, plan)
		workspace, environment := "", ""
		counts := map[string]int{}
		for _, m := range imported {
			kind := s(m, "model")
			counts[kind]++
			if kind == "workspace" && workspace == "" {
				workspace = s(m, "id")
			}
			if kind == "environment" && environment == "" {
				environment = s(m, "id")
			}
		}
		if workspace == "" {
			// An import into a workspace that was there writes no workspace.
			workspace = plan.Destination.WorkspaceID
		}
		var models []engine.Object
		if err == nil && workspace != "" {
			models, err = a.Engine.Snapshot(a.ctx, workspace)
		}
		return func() {
			state.busy, state.applying = false, false
			if err != nil {
				state.error = err.Error()
				return
			}
			for _, item := range plan.Items {
				delete(a.drafts, item.ID)
			}
			if workspace != "" {
				if a.workspace != workspace {
					a.active, a.tabs, a.cookieJar, a.environment = "", nil, "", ""
				}
				a.workspace = workspace
				a.replace(models)
				a.tabs = slices.DeleteFunc(a.tabs, func(id string) bool { return a.models[id] == nil })
				if a.models[a.active] == nil {
					a.active = ""
				} else if a.drafts[a.active] == nil {
					a.drafts[a.active] = newDraft(a.models[a.active])
				}
				// Yaak opens the workspace at the first environment imported.
				if a.models[environment] != nil {
					a.environment = environment
				}
				a.activeCookieJar()
			}
			if a.imports == state {
				a.importCounts = counts
				a.prompt("import_complete", "Import Complete", "", "")
			}
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
			a.importSourcesStep(c, p, state)
		} else {
			a.importPreview(c, p, state)
		}
	})
}

// importSourcesStep is the dialog's first step: the sources, where they go.
func (a *App) importSourcesStep(c *ui.Context, p colors, state *importDialog) {
	// The empty drop zone fills the dialog, as Yaak's flex-1 does; a list scrolls.
	body := ui.Column(c)
	if len(state.sources) > 0 {
		body = ui.Scroll(c)
	}
	body.Grow(1).MinHeight(0).Padding(8, 24, 16, 24).Gap(16).Children(func() {
		a.importSourceList(c, p, state)
		if state.error != "" {
			ui.Text(c, state.error).FontSize(13).Padding(10, 12).Radius(6).Border(1, p.orange.Alpha(.45)).Background(p.orange.Alpha(.08)).MaxLines(8).Selectable().FillWidth()
		}
		if linked := a.linkedWorkspace(state); linked != nil && s(linked, "id") != a.importDestinationWorkspace(state) {
			ui.Row(c).Gap(0).Children(func() {
				label := "These sources were"
				if len(state.sources) == 1 {
					label = "This source was"
				}
				ui.Text(c, label+" last imported into").FontSize(12).TextColor(p.muted).Margin(0, 3.3, 0, 0)
				if inlineLink(c, p, s(linked, "name"), false, 12).Clicked() {
					if s(linked, "id") == a.workspace {
						state.choice = "current"
					} else {
						state.choice, state.other = "other", s(linked, "id")
					}
				}
			})
		}
	})
	allDetected := true
	for _, source := range state.sources {
		if d := state.detections[source.path]; d == nil || d.status != "ok" {
			allDetected = false
		}
	}
	kind := a.importDestinationKind(state)
	dialogFooter(c, p, func() {
		workspaces := a.list("workspace")
		if len(workspaces) == 0 {
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "Import into").FontSize(12).TextColor(p.muted)
			options := []string{"New Workspace"}
			values := []string{"new"}
			if a.workspace != "" {
				options, values = append(options, "Current Workspace"), append(values, "current")
			}
			options, values = append(options, "Other Workspace"), append(values, "other")
			selected := options[max(0, slices.Index(values, kind))]
			if ui.Select(c, &selected, options).Label("Import into").FontSize(12).Changed() {
				state.choice = values[slices.Index(options, selected)]
			}
			if kind == "other" {
				names, ids := []string{"Select a workspace"}, []string{""}
				for _, w := range workspaces {
					if s(w, "id") != a.workspace {
						names, ids = append(names, s(w, "name")), append(ids, s(w, "id"))
					}
				}
				workspace := names[max(0, slices.Index(ids, state.other))]
				if ui.Select(c, &workspace, names).Label("Workspace").FontSize(12).Changed() {
					state.other = ids[slices.Index(names, workspace)]
				}
			}
		})
	}, func() {
		if ui.Button(c, "Cancel").Disabled(state.busy).Clicked() {
			if state.cancel != nil {
				state.cancel()
			}
			a.dialogOpen = false
		}
		label := "Preview Import"
		if state.busy {
			label = "Analyzing"
		}
		if ui.PrimaryButton(c, label).Disabled(len(state.sources) == 0 || !allDetected || state.busy || kind == "other" && state.other == "").Clicked() {
			a.previewImport()
		}
	})
}

// importSourceList is Yaak's ImportSourceList.
func (a *App) importSourceList(c *ui.Context, p colors, state *importDialog) {
	pick := func(directory bool) {
		a.background(func() (func(), error) {
			title := "Select Import Files"
			if directory {
				title = "Select Import Folder"
			}
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: title, Multiple: true, Directory: directory})
			return func() {
				if a.imports != state {
					return
				}
				if err != nil {
					state.error = err.Error()
					return
				}
				kind := "file"
				if directory {
					kind = "folder"
				}
				var added []importSource
				for _, path := range paths {
					added = append(added, importSource{path: path, kind: kind})
				}
				a.addImportSources(state, added)
			}, nil
		})
	}
	openPath := func() { state.showPathInput = true }
	pathInput := func() {
		if !state.showPathInput {
			return
		}
		trimmed := strings.TrimSpace(state.pathInput)
		add := func() {
			if trimmed == "" || state.busy {
				return
			}
			a.addImportSources(state, []importSource{sourcePath(trimmed)})
			state.pathInput, state.showPathInput = "", false
		}
		ui.Row(c).FillWidth().Gap(4).Padding(2, 4, 2, 0).Radius(6).Border(1, p.border).Children(func() {
			field := ui.TextInput(c, &state.pathInput).Label("URL or file path").Placeholder("Paste a URL or a file path").AutoFocus().Grow(1).Border(0, ui.Transparent).Disabled(state.busy)
			if field.Submitted() {
				add()
			}
			if field.Shortcut(0, ui.KeyEscape) {
				state.pathInput, state.showPathInput = "", false
			}
			if trimmed != "" && ui.Button(c, "Add").FontSize(11).Padding(2, 8).Clicked() {
				add()
			}
			if smallIconButton(c, "close", "Cancel").Clicked() {
				state.pathInput, state.showPathInput = "", false
			}
		})
	}
	var area ui.Element
	if len(state.sources) == 0 {
		ui.Column(c).Grow(1).Gap(8).Children(func() {
			area = ui.Column(c).Grow(1).MinHeight(112).Padding(20, 16).Gap(8).Center().Radius(8).Border(1, p.border).BorderStyle(ui.BorderDashed)
			hovering := area.FileDragOver()
			if hovering {
				area.Border(1, p.notice).BorderStyle(ui.BorderSolid).Background(p.border.Alpha(.35))
			}
			area.Children(func() {
				icon(c, "folderInput").FontSize(40).TextColor(p.subtle).Margin(0, 0, 4, 0)
				if hovering {
					ui.Text(c, "Drop to add").FontSize(13).TextColor(p.muted)
				} else {
					// A text's own end spaces don't lay out, so a space is a margin.
					const space = 3.6
					ui.Row(c).Gap(0).Children(func() {
						ui.Text(c, "Choose").FontSize(13).TextColor(p.muted).Margin(0, space, 0, 0)
						if inlineLink(c, p, "files", state.busy, 13).Clicked() {
							pick(false)
						}
						ui.Text(c, ", a").FontSize(13).TextColor(p.muted).Margin(0, space, 0, 0)
						if inlineLink(c, p, "folder", state.busy, 13).Clicked() {
							pick(true)
						}
						ui.Text(c, ", or a").FontSize(13).TextColor(p.muted).Margin(0, space, 0, 0)
						if inlineLink(c, p, "URL", state.busy, 13).Clicked() {
							openPath()
						}
						ui.Text(c, ", or drag them here").FontSize(13).TextColor(p.muted)
					})
				}
				ui.Text(c, "Supports OpenAPI, Swagger, Postman, Insomnia, Bruno, curl, and Yaak exports").FontSize(11).TextColor(p.subtle)
			})
			pathInput()
		})
	} else {
		var items []pathListItem
		for _, source := range state.sources {
			detection := state.detections[source.path]
			item := pathListItem{path: source.path, icon: importSourceIcon(source), failed: detection != nil && detection.status == "error"}
			item.subtitle = func() { importDetectionSubtitle(c, p, source, detection) }
			items = append(items, item)
		}
		area = pathList(c, p, items, func(path string) { a.removeImportSource(state, path) }, []pathListAdd{
			{"Files", func() { pick(false) }},
			{"Folder", func() { pick(true) }},
			{"URL or file path", openPath},
		}, "Add files, a folder, or a URL", "or drop them here", false, state.busy, pathInput)
		if area.FileDragOver() {
			area.Background(p.border.Alpha(.35))
		}
	}
	if dropped := area.DroppedFiles(); len(dropped) > 0 && !state.busy {
		var added []importSource
		for _, path := range dropped {
			added = append(added, importSource{path: path})
		}
		a.addImportSources(state, added)
	}
}

func importDetectionSubtitle(c *ui.Context, p colors, source importSource, detection *importDetection) {
	_, parent := splitPath(source.path)
	if source.kind == "text" {
		parent = ""
	}
	switch {
	case detection == nil:
	case detection.status == "loading":
		ui.Spinner(c).Size(10, 10).TextColor(p.subtle)
		ui.Text(c, "Detecting format").FontSize(11).TextColor(p.subtle)
		return
	case detection.status == "error":
		ui.Text(c, detection.message).FontSize(11).TextColor(p.red).SingleLine().MinWidth(0)
		return
	case detection.status == "ok":
		ui.Text(c, detection.importer).FontSize(11).TextColor(p.subtle)
		if parent != "" {
			ui.Text(c, "·").FontSize(11).TextColor(p.subtle)
		}
	}
	if parent != "" {
		ui.Text(c, parent).Font("monospace").FontSize(11).TextColor(p.subtle).SingleLine().MinWidth(0)
	}
}

func importSourceIcon(source importSource) string {
	switch source.kind {
	case "url":
		return "globe"
	case "folder":
		return "folder"
	case "file":
		return "file"
	}
	return "import"
}

// inlineLink is Yaak's InlineButton: underlined text that acts.
func inlineLink(c *ui.Context, p colors, label string, disabled bool, size float32) ui.Element {
	link := ui.ButtonBase(c).Label(label).Disabled(disabled)
	link.Children(func() {
		text := ui.Text(c, label).FontSize(size).Underline()
		if link.Hovered() {
			text.TextColor(p.text)
		} else {
			text.TextColor(p.muted)
		}
	})
	return link
}

// dialogFooter is Yaak's DialogFooter: an optional left slot, then the
// actions at the end, under a rule.
func dialogFooter(c *ui.Context, p colors, left, actions func()) {
	ui.Row(c).Padding(12, 24).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
		if left != nil {
			ui.Row(c).MinWidth(0).Children(left)
		}
		ui.Box(c).Grow(1)
		ui.Row(c).Gap(8).Shrink(0).Children(actions)
	})
}

// importTreeNode is a row of the preview's tree (Yaak's TreeRow): the
// destination workspace, the group of its environments, or a plan item.
type importTreeNode struct {
	key, kind, label string
	isNew            bool
	item             *engine.ImportItem
	children         []*importTreeNode
}

// importPreview is the dialog's second step: the plan, to check and apply.
func (a *App) importPreview(c *ui.Context, p colors, state *importDialog) {
	plan := state.plan
	var items []*engine.ImportItem
	for _, item := range plan.Items {
		// The workspace roots the tree; commit always applies it.
		if item.Kind != "workspace" {
			items = append(items, item)
		}
	}
	byID := map[string]*engine.ImportItem{}
	for _, item := range items {
		byID[item.SourceID+":"+item.ID] = item
	}
	ancestors := func(item *engine.ImportItem) []*engine.ImportItem {
		var out []*engine.ImportItem
		seen := map[string]bool{}
		for parent := byID[item.SourceID+":"+item.ParentID]; parent != nil && !seen[parent.ID]; parent = byID[item.SourceID+":"+parent.ParentID] {
			seen[parent.ID] = true
			out = append(out, parent)
		}
		return out
	}
	// Deleting a folder takes its contents with it, leaving them nothing to decide.
	disabled := map[*engine.ImportItem]bool{}
	for _, item := range items {
		if item.Action != "delete" {
			continue
		}
		for _, parent := range ancestors(item) {
			if parent.Action == "delete" && parent.Selected {
				disabled[item] = true
			}
		}
	}
	unchanged, changes := 0, 0
	for _, item := range items {
		switch {
		case item.Action == "unchanged":
			unchanged++
		case disabled[item]:
		case item.Action == "conflict":
			if item.Resolution == "take_source" {
				changes++
			}
		case item.Selected:
			changes++
		}
	}
	destinationLabel, destinationValue := "Destination", cmp.Or(s(a.models[plan.Destination.WorkspaceID], "name"), "Unknown workspace")
	var newWorkspaces []*engine.ImportItem
	for _, item := range plan.Items {
		if item.Kind == "workspace" && item.Action == "create" {
			newWorkspaces = append(newWorkspaces, item)
		}
	}
	if plan.Destination.WorkspaceID == "" {
		var names []string
		for _, w := range newWorkspaces {
			if w.Name != "" {
				names = append(names, w.Name)
			}
		}
		switch len(names) {
		case 0:
			destinationLabel, destinationValue = "New workspace", "Untitled"
		case 1:
			destinationLabel, destinationValue = "New workspace", names[0]
		default:
			destinationLabel, destinationValue = fmt.Sprintf("%d new workspaces", len(names)), strings.Join(names, ", ")
		}
	}
	var roots []*importTreeNode
	if plan.Destination.WorkspaceID != "" {
		roots = append(roots, &importTreeNode{key: plan.Destination.WorkspaceID, kind: "destination", label: s(a.models[plan.Destination.WorkspaceID], "name"), children: importItemTree(items)})
	} else if len(newWorkspaces) == 1 {
		roots = append(roots, &importTreeNode{key: newWorkspaces[0].ID, kind: "destination", label: newWorkspaces[0].Name, isNew: true, children: importItemTree(items)})
	} else {
		for _, w := range newWorkspaces {
			var inside []*engine.ImportItem
			for _, item := range items {
				if plan.SourceWorkspaceID(item) == w.ID {
					inside = append(inside, item)
				}
			}
			roots = append(roots, &importTreeNode{key: w.ID, kind: "destination", label: w.Name, isNew: true, children: importItemTree(inside)})
		}
	}
	collapsed := func(n *importTreeNode) bool {
		if v, ok := state.toggled[n.key]; ok {
			return v
		}
		return n.kind == "destination" && len(roots) > 1 || n.item != nil && n.item.Action == "ignored"
	}
	toggle := func(n *importTreeNode, checked bool) {
		targets := map[*engine.ImportItem]bool{}
		for _, item := range importTogglable(n) {
			targets[item] = true
		}
		if checked && n.item != nil {
			// Checking anything brings back the folders it lives in.
			for _, parent := range ancestors(n.item) {
				if parent.Kind == "folder" && (parent.Action == "create" || parent.Action == "ignored") {
					targets[parent] = true
				}
			}
		}
		for item := range targets {
			item.Selected = checked
		}
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 24, 16, 24).Gap(16).Disabled(state.busy).Children(func() {
		ui.Column(c).FillWidth().Radius(8).Border(1, p.border).Children(func() {
			previewRow := func(label, value string, last bool) {
				row := ui.Row(c).Padding(8, 12).Gap(16)
				if !last {
					row.BorderWidth(0, 0, 1, 0).BorderColor(p.border)
				}
				row.Children(func() {
					ui.Text(c, label).FontSize(13).TextColor(p.muted)
					ui.Box(c).Grow(1)
					ui.Text(c, value).FontSize(13).FontWeight(500).MaxLines(3)
				})
			}
			previewRow("Detected format", plan.Importer(), false)
			previewRow(destinationLabel, destinationValue, true)
		})
		seen := map[string]bool{}
		for _, warning := range plan.Warnings {
			key := warning.Level + ":" + warning.Title + ":" + warning.Detail
			if seen[key] {
				continue
			}
			seen[key] = true
			color, glyph := p.blue, "info"
			if warning.Level == "warning" {
				color, glyph = p.orange, "alert"
			}
			ui.Row(c).Key(key).FillWidth().Padding(10, 12).Gap(10).Radius(6).Border(1, color.Alpha(.45)).Background(color.Alpha(.08)).AlignItems(ui.Start).Children(func() {
				icon(c, glyph).FontSize(13).TextColor(color).Margin(2, 0, 0, 0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, warning.Title).FontSize(13).FontWeight(500)
					ui.Text(c, warning.Detail).FontSize(11).TextColor(p.muted).MaxLines(4)
				})
			})
		}
		for _, root := range roots {
			ui.Column(c).Key(root.key).FillWidth().Padding(8, 12).Radius(8).Border(1, p.border).Children(func() {
				a.importTreeRow(c, p, state, root, 0, collapsed, toggle, disabled)
			})
		}
	})
	dialogFooter(c, p, func() {
		if unchanged > 0 {
			ui.Text(c, fmt.Sprintf("%d %s unchanged", unchanged, pluralize("resource", unchanged))).FontSize(11).TextColor(p.muted)
		}
	}, func() {
		if ui.Button(c, "Back").Disabled(state.busy).Clicked() {
			state.plan = nil
		}
		label := "Done"
		switch {
		case state.applying:
			label = "Importing"
		case changes == 1:
			label = "Apply 1 Change"
		case changes > 1:
			label = fmt.Sprintf("Apply %d Changes", changes)
		}
		if ui.PrimaryButton(c, label).Disabled(state.busy).Clicked() {
			a.applyImport()
		}
	})
}

// importItemTree is Yaak's buildItemTree: items under their folders, the
// environments of the workspace grouped under "Variables".
func importItemTree(items []*engine.ImportItem) []*importTreeNode {
	byID := map[string]*engine.ImportItem{}
	for _, item := range items {
		byID[item.SourceID+":"+item.ID] = item
	}
	children := map[*engine.ImportItem][]*engine.ImportItem{}
	var roots []*engine.ImportItem
	for _, item := range items {
		if parent := byID[item.SourceID+":"+item.ParentID]; parent != nil && parent != item {
			children[parent] = append(children[parent], item)
		} else {
			roots = append(roots, item)
		}
	}
	byKind := func(list []*engine.ImportItem) []*engine.ImportItem {
		var out []*engine.ImportItem
		for _, kind := range []string{"environment", "folder", ""} {
			for _, item := range list {
				if item.Kind == kind || kind == "" && item.Kind != "environment" && item.Kind != "folder" {
					out = append(out, item)
				}
			}
		}
		return out
	}
	var node func(*engine.ImportItem, map[*engine.ImportItem]bool) *importTreeNode
	node = func(item *engine.ImportItem, seen map[*engine.ImportItem]bool) *importTreeNode {
		n := &importTreeNode{key: item.Key(), kind: "item", item: item}
		if seen[item] {
			return n
		}
		inner := map[*engine.ImportItem]bool{item: true}
		maps.Copy(inner, seen)
		for _, child := range byKind(children[item]) {
			n.children = append(n.children, node(child, inner))
		}
		return n
	}
	var environments, others []*importTreeNode
	for _, item := range byKind(roots) {
		if item.Kind == "environment" {
			environments = append(environments, node(item, nil))
		} else {
			others = append(others, node(item, nil))
		}
	}
	if len(environments) == 0 {
		return others
	}
	return append([]*importTreeNode{{key: "group:environments", kind: "group", label: "Variables", children: environments}}, others...)
}

// importTogglable is Yaak's togglableItems: what a row's checkbox decides,
// its own item and those below; unchanged and conflicting ones decide nothing.
func importTogglable(n *importTreeNode) []*engine.ImportItem {
	var out []*engine.ImportItem
	if n.item != nil && n.item.Action != "unchanged" && n.item.Action != "conflict" {
		out = append(out, n.item)
	}
	for _, child := range n.children {
		out = append(out, importTogglable(child)...)
	}
	return out
}

// importRelevant is Yaak's isRelevant: a heading, or a change; a row shows
// when it or one below it is relevant.
func importRelevant(n *importTreeNode) bool {
	if n.kind == "destination" || n.item != nil && n.item.Action != "unchanged" {
		return true
	}
	return slices.ContainsFunc(n.children, importRelevant)
}

// importTreeRow is Yaak's CheckboxTree, a row and then its children.
func (a *App) importTreeRow(c *ui.Context, p colors, state *importDialog, n *importTreeNode, depth int, collapsed func(*importTreeNode) bool, toggle func(*importTreeNode, bool), disabled map[*engine.ImportItem]bool) {
	if !importRelevant(n) {
		return
	}
	visible := slices.ContainsFunc(n.children, importRelevant)
	open := !collapsed(n)
	column := ui.Column(c).Key(n.key).FillWidth()
	if depth > 0 {
		column.Padding(0, 0, 0, 16).Margin(0, 0, 0, 8).BorderWidth(0, 0, 0, 1).BorderColor(p.border).BorderStyle(ui.BorderDashed)
	}
	column.Children(func() {
		ui.Row(c).FillWidth().Height(24).Gap(4).Children(func() {
			name := n.label
			if n.item != nil {
				name = n.item.Name
			}
			if visible {
				if chevronToggle(c, p, open, name).Clicked() {
					state.toggled[n.key] = open
				}
			} else {
				ui.Box(c).Width(16).Shrink(0)
			}
			covered := importTogglable(n)
			selected := 0
			for _, item := range covered {
				if item.Selected {
					selected++
				}
			}
			if len(covered) == 0 {
				ui.Box(c).Width(16).Margin(0, 2, 0, 0).Shrink(0)
			} else {
				checked := selected == len(covered)
				box := checkbox(c, p, &checked, selected > 0 && selected < len(covered), "Toggle "+name).Disabled(n.item != nil && disabled[n.item])
				if box.Changed() {
					toggle(n, checked)
				}
			}
			ui.Row(c).Grow(1).MinWidth(0).Gap(4).Padding(2, 4).Children(func() {
				switch n.kind {
				case "destination":
					icon(c, "box").FontSize(13).TextColor(p.muted).Shrink(0)
					ui.Text(c, n.label).FontSize(13).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
					if n.isNew {
						importChip(c, p, "new", "Created by this import", p.green)
					}
				case "group":
					ui.Text(c, n.label).FontSize(13).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
				default:
					item := n.item
					ui.Text(c, item.Name).FontSize(13).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
					if item.Action == "conflict" {
						choice := 0
						if item.Resolution == "take_source" {
							choice = 1
						}
						control := ui.Segmented(c, &choice, "Keep mine", "Take source").Label("Resolve conflict for " + item.Name).FontSize(11)
						if help := importActionHelp(item); help != "" {
							control.Tooltip(help)
						}
						if control.Changed() {
							item.Resolution = map[int]string{0: "keep_mine", 1: "take_source"}[choice]
						}
					} else if label := importActionLabel(item); label != "" {
						importChip(c, p, label, importActionHelp(item), importActionColor(p, item))
					}
				}
			})
		})
		if open {
			for _, child := range n.children {
				a.importTreeRow(c, p, state, child, depth+1, collapsed, toggle, disabled)
			}
		}
	})
}

// importChip is Yaak's Chip with an IconTooltip, as ActionChip.
func importChip(c *ui.Context, p colors, label, help string, color ui.Color) {
	chip := ui.Row(c).Gap(4).Padding(2, 8).Radius(6).Background(color.Alpha(.1)).Border(1, color.Alpha(.1)).Shrink(0)
	chip.Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(500).TextColor(color)
		if help != "" {
			icon(c, "info").FontSize(11).TextColor(color).Tooltip(help)
		}
	})
}

func importActionColor(p colors, item *engine.ImportItem) ui.Color {
	switch item.Action {
	case "create":
		return p.green
	case "update":
		return p.blue
	case "delete":
		return p.red
	case "keep_local":
		if item.Selected {
			return p.orange
		}
	}
	return p.muted
}

// importActionLabel is Yaak's actionLabel.
func importActionLabel(item *engine.ImportItem) string {
	return map[string]string{"create": "new", "update": "updated", "delete": "removed", "keep_local": "edited", "ignored": "ignored"}[item.Action]
}

// importActionHelp is Yaak's actionHelp, with the fields that changed.
func importActionHelp(item *engine.ImportItem) string {
	help := func(text string) string {
		if len(item.ChangedFields) == 0 {
			return text
		}
		fields := make([]string, len(item.ChangedFields))
		for i, field := range item.ChangedFields {
			fields[i] = importFieldLabel(field)
		}
		return text + " · " + strings.Join(fields, ", ")
	}
	switch item.Action {
	case "create":
		return "Not in this workspace yet"
	case "update":
		if item.Reason == "moved_into_ignored_folder" {
			return "Moved into a folder that isn't imported. Importing that folder brings it along"
		}
		return help("Changed in the source since the last import")
	case "delete":
		return "Gone from the source since the last import. Checking it deletes it here"
	case "keep_local":
		return help("Changed here since the last import. Checking it reverts to the source")
	case "conflict":
		return help("Changed here and in the source since the last import")
	case "ignored":
		return "Not in this workspace. Imports leave it alone until you check it"
	}
	return ""
}

var upperLetter = regexp.MustCompile(`([A-Z])`)

// importFieldLabel is Yaak's fieldLabel: urlParameters as "url parameters".
func importFieldLabel(field string) string {
	return strings.ToLower(upperLetter.ReplaceAllString(field, " $1"))
}

// importCompleteDialog is Yaak's Import Complete dialog: what was imported.
func (a *App) importCompleteDialog(c *ui.Context, p colors) {
	ui.Column(c).Padding(8, 24, 16, 24).Gap(12).Children(func() {
		ui.Column(c).Gap(4).Padding(0, 0, 0, 12).Children(func() {
			for _, kind := range []struct{ model, label string }{
				{"workspace", "Workspace"}, {"environment", "Environment"}, {"folder", "Folder"},
				{"http_request", "HTTP Request"}, {"grpc_request", "GRPC Request"}, {"websocket_request", "Websocket Request"},
			} {
				if count := a.importCounts[kind.model]; count > 0 {
					ui.Text(c, "•  "+pluralizeCount(kind.label, count)).FontSize(13)
				}
			}
		})
		ui.Row(c).Children(func() {
			if ui.PrimaryButton(c, "Done").Clicked() {
				a.dialogOpen = false
			}
		})
	})
}

// ImportDeepLink is Yaak's import-data deep link: a file at fileURL, once
// confirmed, or at path, imported as it is, then the first workspace it
// imported opened with its first environment.
func (a *App) ImportDeepLink(fileURL, path, name string) {
	if a.deferUntilInputs(func() { a.ImportDeepLink(fileURL, path, name) }) {
		return
	}
	a.Focus()
	if name == "" {
		name = "data"
	}
	input := engine.ImportInput{Origin: path}
	if fileURL != "" {
		if !a.confirmNative("Import "+name+" from "+fileURL+"?", "Import") {
			return
		}
		input = engine.ImportInput{Kind: "url", Origin: fileURL}
	} else if path == "" {
		a.showToast("", "Failed to import data", "danger", 0)
		return
	}
	a.saveActive()
	a.background(func() (func(), error) {
		plan, err := a.Engine.PlanImport(a.ctx, []engine.ImportInput{input}, engine.ImportDestination{})
		if err != nil {
			return nil, err
		}
		imported, err := a.Engine.CommitImport(a.ctx, plan)
		if err != nil {
			return nil, err
		}
		workspaces, environment := 0, ""
		workspace := ""
		for _, m := range imported {
			switch s(m, "model") {
			case "workspace":
				if workspace == "" {
					workspace = s(m, "id")
				}
				workspaces++
			case "environment":
				if environment == "" {
					environment = s(m, "id")
				}
			}
		}
		var models []engine.Object
		if workspace != "" {
			if models, err = a.Engine.Snapshot(a.ctx, workspace); err != nil {
				return nil, err
			}
		}
		return func() {
			a.showToast("", fmt.Sprintf("Imported data for %d workspaces", workspaces), "success", 5*time.Second)
			if workspace == "" {
				return
			}
			a.workspace, a.active, a.tabs, a.environment, a.cookieJar = workspace, "", nil, "", ""
			a.replace(models)
			if a.models[environment] != nil {
				a.environment = environment
			}
			a.activeCookieJar()
		}, nil
	})
}

// confirmNative asks with the system's message box, as Yaak's deep links
// do: true when the confirm button is chosen.
func (a *App) confirmNative(message, confirm string) bool {
	if a.confirmMessage != nil {
		return a.confirmMessage(message, confirm)
	}
	result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.Window, Type: mygo.MessageInfo, Message: message, Buttons: []string{confirm, "Cancel"}, CancelButton: 1})
	return err == nil && result.Button == 0
}

// checkbox is Yaak's Checkbox: a box, checked, unchecked or mixed
// ("indeterminate"), titled label for assistive technology.
func checkbox(c *ui.Context, p colors, checked *bool, mixed bool, label string) ui.Element {
	box := ui.CheckboxBase(c, checked).Label(label).Size(16, 16).Radius(4).Shrink(0).Border(1, p.border)
	if mixed {
		box.Mixed()
	}
	if *checked || mixed {
		box.Background(p.accent).Border(1, p.accent)
	}
	box.Children(func() {
		switch {
		case mixed:
			icon(c, "minus").FontSize(12).TextColor(ui.Hex("#ffffff"))
		case *checked:
			icon(c, "check").FontSize(12).TextColor(ui.Hex("#ffffff"))
		}
	})
	return box
}
