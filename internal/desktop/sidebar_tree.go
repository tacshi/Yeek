package desktop

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// treeState is the sidebar's selection, as Yaak's Tree keeps it: the
// selected ids, the anchor Shift extends from, and the row last selected,
// which has the keyboard focus. renaming is the row edited in place.
type treeState struct {
	selected            []string
	anchor, last        string
	syncedActive, focus string
	focused             bool
	renaming, rename    string
	renameFrames        int
}

// treeDrag is the sidebar rows a drag moves.
type treeDrag struct{ ids []string }

type treeRow struct {
	m     engine.Object
	depth int
}

// visibleTree lists the sidebar's rows in order, as the filter and the
// open folders show them.
func (a *App) visibleTree() []treeRow {
	var rows []treeRow
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		if depth > 32 {
			return
		}
		for _, m := range a.treeItems(parent) {
			if !a.treeMatch(m, 0) {
				continue
			}
			rows = append(rows, treeRow{m, depth})
			if id := s(m, "id"); s(m, "model") == "folder" && (a.expanded[id] || a.search != "") {
				walk(id, depth+1)
			}
		}
	}
	walk("", 0)
	return rows
}

func (a *App) requestTree(c *ui.Context, p colors) bool {
	t := &a.tree
	if t.syncedActive != a.active {
		t.syncedActive = a.active
		if a.active != "" && !slices.Contains(t.selected, a.active) {
			t.selected, t.anchor, t.last = []string{a.active}, a.active, a.active
		}
	}
	rows := a.visibleTree()
	for _, r := range rows {
		a.treeRow(c, p, r, rows)
	}
	if len(rows) == 0 && a.search == "" {
		ui.Column(c).Padding(18, 12).Gap(12).Children(func() {
			ui.Text(c, "No requests yet").TextColor(p.muted).FontSize(12)
			if ui.Button(c, "New HTTP Request").Clicked() {
				a.addRequest("http_request", "")
			}
		})
		return false
	}
	// Dropping past the last row moves to the end of the workspace.
	end := ui.Box(c).Key("tree-end").Grow(1).MinHeight(32).FillWidth()
	if _, over := ui.DragOver[treeDrag](end); over {
		end.Children(func() { ui.Box(c).Height(2).FillWidth().Background(p.accent) })
	}
	if drag, ok := ui.Drop[treeDrag](end); ok {
		a.moveTreeItems(drag.ids, "", len(a.treeItems("")))
	}
	return len(rows) > 0
}

func (a *App) treeRow(c *ui.Context, p colors, r treeRow, rows []treeRow) {
	t := &a.tree
	m := r.m
	id := s(m, "id")
	folder := s(m, "model") == "folder"
	name := requestName(m)
	open := a.expanded[id] || a.search != ""
	selected := slices.Contains(t.selected, id)
	renaming := t.renaming == id
	row := ui.Row(c).Key(id).Height(28).Padding(0, 6, 0, float32(6+r.depth*12)).Gap(6).Radius(4).Focusable().Label(name)
	if !renaming {
		dragged := []string{id}
		if selected {
			dragged = slices.Clone(t.selected)
		}
		row.Drag(treeDrag{dragged})
		if row.Dragging() && !selected {
			t.selected, t.anchor, t.last = []string{id}, id, id
		}
	}
	if a.focusSidebar && (id == t.last || t.last == "" && id == s(rows[0].m, "id")) {
		a.focusSidebar = false
		row.Focus()
	}
	if t.focus == id {
		t.focus = ""
		row.Focus().ScrollIntoView()
	}
	if row.Focused() {
		a.sidebarFocused, t.focused = true, true
	}
	// Where a drag would drop: before the row, or after it, which for a
	// folder is its first child.
	side := 0
	if _, over := ui.DragOver[treeDrag](row); over {
		side = dropSide(row)
	}
	switch {
	case side == dropAfter && folder:
		row.Border(1, p.accent)
	case selected:
		row.Background(p.border.Alpha(.55))
	case row.Hovered():
		row.Background(p.border.Alpha(.3))
	}
	row.Children(func() {
		if side == dropBefore || side == dropAfter && !folder {
			line := ui.Box(c).Absolute().Left(float32(r.depth * 12)).Right(0).Height(2).Background(p.accent)
			if side == dropBefore {
				line.Top(-1)
			} else {
				line.Bottom(-1)
			}
		}
		if folder {
			glyph := "chevron"
			if open {
				glyph = "down"
			}
			chevron := ui.ButtonBase(c).Label("Toggle folder").Size(14, 20).Justify(ui.Center)
			chevron.Children(func() { icon(c, glyph).FontSize(11).TextColor(p.subtle) })
			if chevron.Clicked() {
				a.expanded[id] = !a.expanded[id]
			}
			icon(c, "folder").FontSize(14).TextColor(p.muted)
		} else {
			method := requestMethod(m)
			tag := ui.Text(c, shortMethod(method)).SingleLine().Shrink(0).Tooltip(method).Font("monospace").FontSize(11).TextColor(a.methodColor(method, p))
			if id != a.active {
				tag.Opacity(.75)
			}
		}
		if renaming {
			a.renameInput(c, p, m)
			return
		}
		label := ui.Text(c, name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
		if id != a.active && id != a.activeFolder {
			label.TextColor(p.text.Alpha(.85))
		}
		if a.running[id] {
			ui.Spinner(c).Size(12, 12).TextColor(p.subtle)
		} else if response := a.responses[id]; response != nil && !folder {
			ui.Text(c, statusLabel(response, true)).Font("monospace").FontSize(11).TextColor(statusColor(response, p))
		}
	})
	if !renaming {
		row.HandleInput(func(ev ui.InputEvent) bool {
			return ev.Kind == ui.InputKeyDown && a.treeKey(ev, rows)
		})
	}
	if row.DoubleClicked() {
		if folder {
			a.expanded[id] = !a.expanded[id]
		} else {
			a.startRename(id)
		}
	} else if row.Clicked() {
		mods := row.ClickModifiers()
		a.selectTreeItem(id, mods, rows)
		if mods&(ui.Shift|ui.Cmd) == 0 && !folder {
			a.openRequest(id)
		}
	}
	if row.RightClicked() && !selected {
		t.selected, t.last = []string{id}, id
	}
	row.ContextMenu(func(menu *ui.Menu) { a.treeMenu(menu, m) })
	if drag, ok := ui.Drop[treeDrag](row); ok {
		a.dropTreeItems(drag.ids, m, dropSide(row))
	}
}

// Where over a row a drag drops.
const (
	dropBefore = iota + 1
	dropAfter
)

func dropSide(row ui.Element) int {
	_, y, _ := row.PointerPosition()
	if y < row.Bounds().H/2 {
		return dropBefore
	}
	return dropAfter
}

// selectTreeItem is Yaak's handleSelect: Shift selects the rows from the
// anchor, Cmd adds or removes a row, and a plain click selects one row.
func (a *App) selectTreeItem(id string, mods ui.Modifiers, rows []treeRow) {
	t := &a.tree
	t.last = id
	index := func(id string) int {
		return slices.IndexFunc(rows, func(r treeRow) bool { return s(r.m, "id") == id })
	}
	switch {
	case mods&ui.Shift != 0:
		from, to := index(t.anchor), index(id)
		if len(t.selected) == 0 || from < 0 || to < 0 {
			t.selected, t.anchor = []string{id}, id
			return
		}
		from, to = min(from, to), max(from, to)
		t.selected = t.selected[:0]
		for _, r := range rows[from : to+1] {
			t.selected = append(t.selected, s(r.m, "id"))
		}
	case mods&ui.Cmd != 0:
		if i := slices.Index(t.selected, id); i >= 0 {
			t.selected = slices.Delete(t.selected, i, i+1)
		} else {
			t.selected = append(t.selected, id)
		}
	default:
		t.selected, t.anchor = []string{id}, id
	}
}

// treeKey handles the keys of a focused sidebar row, as Yaak's Tree does.
func (a *App) treeKey(ev ui.InputEvent, rows []treeRow) bool {
	t := &a.tree
	at := slices.IndexFunc(rows, func(r treeRow) bool { return s(r.m, "id") == t.last })
	move := func(to int) {
		if to >= 0 && to < len(rows) {
			id := s(rows[to].m, "id")
			a.selectTreeItem(id, ev.Mods&ui.Shift, rows)
			t.focus = id
		}
	}
	mods := ev.Mods &^ ui.Shift
	var current engine.Object
	if at >= 0 {
		current = rows[at].m
	}
	isFolder := s(current, "model") == "folder"
	switch {
	case mods == 0 && (ev.Key == ui.KeyUp || ev.Key == ui.KeyK):
		move(at - 1)
	case mods == 0 && (ev.Key == ui.KeyDown || ev.Key == ui.KeyJ):
		move(at + 1)
	case mods == 0 && (ev.Key == ui.KeyRight || ev.Key == ui.KeyL):
		if isFolder && !a.expanded[t.last] {
			a.expanded[t.last] = true
		} else {
			move(at + 1)
		}
	case mods == 0 && (ev.Key == ui.KeyLeft || ev.Key == ui.KeyH):
		if isFolder && a.expanded[t.last] {
			a.expanded[t.last] = false
		} else if parent := s(current, "folderId"); parent != "" {
			move(slices.IndexFunc(rows, func(r treeRow) bool { return s(r.m, "id") == parent }))
		}
	case ev.Key == ui.KeyEscape && ev.Mods == 0:
		if t.last != "" {
			t.selected = []string{t.last}
		}
	case a.hotkeyMatches("sidebar.selected.rename", ev):
		if len(t.selected) == 1 {
			a.startRename(t.selected[0])
		}
	case a.hotkeyMatches("sidebar.selected.delete", ev):
		a.deleteTreeItems(t.selected)
	case a.hotkeyMatches("sidebar.selected.move", ev):
		a.moveToWorkspace(slices.DeleteFunc(a.selectedTreeModels(), func(m engine.Object) bool { return !strings.HasSuffix(s(m, "model"), "_request") }))
	default:
		return false
	}
	return true
}

// hotkeyItem shows the action's first key by a menu item.
func (a *App) hotkeyItem(item *ui.MenuItem, action string) *ui.MenuItem {
	if keys := a.hotkeyKeys(action); len(keys) > 0 {
		item.Shortcut(keys[0].mods, keys[0].key)
	}
	return item
}

func (a *App) hotkeyMatches(action string, ev ui.InputEvent) bool {
	for _, k := range a.hotkeyKeys(action) {
		if k.mods == ev.Mods && k.key == ev.Key {
			return true
		}
	}
	return false
}

// selectedTreeModels returns the selected sidebar items, in sidebar order.
func (a *App) selectedTreeModels() []engine.Object {
	var models []engine.Object
	for _, r := range a.visibleTree() {
		if slices.Contains(a.tree.selected, s(r.m, "id")) {
			models = append(models, r.m)
		}
	}
	return models
}

func (a *App) startRename(id string) {
	if m := a.models[id]; m != nil {
		a.tree.renaming, a.tree.rename, a.tree.renameFrames = id, requestName(m), 0
	}
}

// renameInput edits a row's name in place: Enter or leaving the field
// saves it, and Escape gives up.
func (a *App) renameInput(c *ui.Context, p colors, m engine.Object) {
	t := &a.tree
	input := ui.TextInput(c, &t.rename).Label("Rename "+requestName(m)).Placeholder(s(m, "name")).Grow(1).MinWidth(0).Height(24).FontSize(13).Padding(0, 4).Background(p.background).Border(1, p.accent)
	t.renameFrames++
	if t.renameFrames == 1 {
		input.Focus().SelectAll()
	}
	input.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Kind == ui.InputKeyDown && ev.Key == ui.KeyEscape && ev.Mods == 0 {
			t.renaming, t.focus = "", s(m, "id")
			return true
		}
		return false
	})
	if t.renaming == "" {
		return
	}
	if input.Submitted() || t.renameFrames > 2 && !input.Focused() {
		id := s(m, "id")
		if t.rename != requestName(m) {
			a.patchModel(id, engine.Object{"name": t.rename})
		}
		t.renaming = ""
		if input.Submitted() {
			t.focus = id
		}
	}
}

// patchModel saves fields of a model, and of its open draft, which saves
// the rest later.
func (a *App) patchModel(id string, patch engine.Object) {
	m := a.models[id]
	if m == nil {
		return
	}
	saved := deepCopy(m)
	for k, v := range patch {
		saved[k] = v
	}
	a.models[id] = saved
	a.modelVersion++
	if d := a.drafts[id]; d != nil {
		for k, v := range patch {
			d.Model[k] = v
		}
		if name, ok := patch["name"].(string); ok {
			d.Name = name
		}
	}
	update := engine.Object{"model": s(m, "model"), "id": id, "createdAt": m["createdAt"]}
	for k, v := range patch {
		update[k] = v
	}
	a.saveModel(update)
}

// dropTreeItems drops dragged rows before or after a row, or into a folder
// dropped after, as its first child.
func (a *App) dropTreeItems(ids []string, target engine.Object, side int) {
	id, parent := s(target, "id"), s(target, "folderId")
	if slices.Contains(ids, id) {
		return
	}
	if side == dropAfter && s(target, "model") == "folder" {
		a.expanded[id] = true
		a.moveTreeItems(ids, id, 0)
		return
	}
	at := slices.IndexFunc(a.treeItems(parent), func(m engine.Object) bool { return s(m, "id") == id })
	if side == dropAfter {
		at++
	}
	a.moveTreeItems(ids, parent, at)
}

// moveTreeItems is Yaak's handleDragEnd: it moves items into parent at
// insertAt among its children, between their neighbors' sort priorities,
// or renumbers all the children when there is no room between them.
func (a *App) moveTreeItems(ids []string, parent string, insertAt int) {
	var items []engine.Object
	for _, id := range ids {
		m := a.models[id]
		if m == nil || id == parent || a.hasAncestor(parent, id) {
			continue // not into itself
		}
		items = append(items, m)
	}
	if len(items) == 0 {
		return
	}
	children := a.treeItems(parent)
	for i := len(children) - 1; i >= 0; i-- {
		if slices.Contains(ids, s(children[i], "id")) {
			children = slices.Delete(children, i, i+1)
			if i < insertAt {
				insertAt--
			}
		}
	}
	insertAt = max(0, min(insertAt, len(children)))
	var before, after float64
	if insertAt > 0 {
		before = n(children[insertAt-1], "sortPriority")
	}
	if insertAt < len(children) {
		after = n(children[insertAt], "sortPriority")
	}
	var folderID any
	if parent != "" {
		folderID = parent
	}
	if after-before < 1 {
		children = slices.Insert(children, insertAt, items...)
		for i, m := range children {
			a.patchModel(s(m, "id"), engine.Object{"sortPriority": float64(i * 1000), "folderId": folderID})
		}
		return
	}
	step := (after - before) / float64(len(items)+2)
	for i, m := range items {
		a.patchModel(s(m, "id"), engine.Object{"sortPriority": before + float64(i+1)*step, "folderId": folderID})
	}
}

// hasAncestor reports whether folder id is, or is inside, ancestor.
func (a *App) hasAncestor(id, ancestor string) bool {
	for depth := 0; id != "" && depth < 32; depth++ {
		if id == ancestor {
			return true
		}
		id = s(a.models[id], "folderId")
	}
	return false
}

// deleteTreeItems asks before deleting sidebar items, as Yaak's
// deleteModelWithConfirm does.
func (a *App) deleteTreeItems(ids []string) {
	if len(ids) == 0 {
		return
	}
	a.deleteIDs = slices.Clone(ids)
	title := "Delete " + pluralizeCount("Item", len(ids))
	if len(ids) == 1 {
		title = "Delete " + modelTypeLabel(a.models[ids[0]])
	}
	a.prompt("delete_items", title, "", "")
}

func pluralizeCount(word string, count int) string {
	if count == 1 {
		return "1 " + word
	}
	return strconv.Itoa(count) + " " + word + "s"
}

// modelTypeLabel is Yaak's name for a kind of model.
func modelTypeLabel(m engine.Object) string {
	switch s(m, "model") {
	case "http_request":
		return "HTTP Request"
	case "grpc_request":
		return "gRPC Request"
	case "websocket_request":
		return "WebSocket Request"
	case "folder":
		return "Folder"
	case "environment":
		return "Environment"
	case "workspace":
		return "Workspace"
	}
	return "Item"
}

// treeMenu is Yaak's sidebar context menu, for the selected rows when the
// row is one of them.
func (a *App) treeMenu(menu *ui.Menu, m engine.Object) {
	items := []engine.Object{m}
	if slices.Contains(a.tree.selected, s(m, "id")) {
		items = a.selectedTreeModels()
	}
	if len(items) == 0 {
		return
	}
	one := len(items) == 1
	child := items[0]
	id := s(child, "id")
	folder := s(child, "model") == "folder"
	onlyHTTP := !slices.ContainsFunc(items, func(m engine.Object) bool { return s(m, "model") != "http_request" })
	if one && folder && menu.Item("Folder Settings").Chosen() {
		a.openScope(child)
	}
	if onlyHTTP && a.hotkeyItem(menu.Item("Send"), "request.send").Chosen() {
		a.sendRequests(treeIDs(items))
	}
	if one && folder && menu.Item("Send All").Disabled(len(a.folderRequests(id)) == 0).Chosen() {
		a.sendFolder(id)
	}
	if one && s(child, "model") == "http_request" && menu.Item("Copy as Curl").Chosen() {
		a.openRequest(id)
		a.copyCurl()
	}
	if one && s(child, "model") == "grpc_request" && menu.Item("Copy as gRPCurl").Chosen() {
		a.copyGrpcurl(id)
	}
	if one {
		for _, action := range a.Engine.PluginActions() {
			if menu.Item(action.Label).Chosen() {
				name, wid := action.Name, a.workspace
				a.run(func() (func(), error) { return nil, a.Engine.RunPluginAction(a.ctx, name, wid, id) })
			}
		}
	}
	menu.Separator()
	if one && a.hotkeyItem(menu.Item("Rename"), "sidebar.selected.rename").Chosen() {
		a.startRename(id)
	}
	if a.hotkeyItem(menu.Item("Duplicate"), "model.duplicate").Chosen() {
		for _, m := range items {
			a.duplicate(s(m, "id"))
		}
	}
	requests := slices.DeleteFunc(slices.Clone(items), func(m engine.Object) bool { return !strings.HasSuffix(s(m, "model"), "_request") })
	if len(a.list("workspace")) > 1 && len(requests) > 0 && len(requests) == len(items) {
		label := "Move"
		if len(items) > 1 {
			label = "Move " + pluralizeCount("Request", len(requests))
		}
		if a.hotkeyItem(menu.Item(label), "sidebar.selected.move").Chosen() {
			a.moveToWorkspace(requests)
		}
	}
	if a.hotkeyItem(menu.Item("Delete"), "sidebar.selected.delete").Chosen() {
		a.deleteTreeItems(treeIDs(items))
	}
	if one && folder {
		menu.Separator()
		a.createMenu(menu, id)
	}
}

func treeIDs(items []engine.Object) []string {
	ids := make([]string, 0, len(items))
	for _, m := range items {
		ids = append(ids, s(m, "id"))
	}
	return ids
}
