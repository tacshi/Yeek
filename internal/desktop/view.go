package desktop

import (
	"cmp"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func (a *App) View(c *ui.Context) {
	a.inView = true
	c.SetZoom(float32(cmp.Or(n(a.settings, "interfaceScale"), 1)))
	p := a.theme(c)
	if !a.dialogOpen && a.templateForm == nil && len(a.valuePrompts) == 0 && !a.palette.open && !a.switcher.open {
		a.handleHotkeys(c)
	}
	// The sidebar sets this again while it builds, for the next frame's ⌘B and ⌘F.
	a.sidebarFocused, a.tree.focused = false, false
	ui.Column(c).Fill().Background(p.background).Children(func() {
		a.header(c, p)
		if len(a.list("workspace")) == 0 {
			a.emptyWorkspace(c, p)
		} else if a.hideSidebar {
			ui.Box(c).Grow(1).MinHeight(0).Children(func() { a.workbench(c, p) })
		} else {
			ui.SplitQuiet(c, &a.sidebarWidth, false, func() { a.sidebar(c, p) }, func() { a.workbench(c, p) }).Grow(1).MinHeight(0)
		}
		if a.errorMessage != "" {
			ui.Row(c).Background(p.red.Alpha(.12)).BorderWidth(1, 0, 0, 0).BorderColor(p.red.Alpha(.4)).Padding(7, 12).Gap(10).Children(func() {
				ui.Text(c, a.errorMessage).TextColor(p.red).Grow(1).MaxLines(3).Selectable()
				if iconButton(c, "close", "Dismiss error").Clicked() {
					a.errorMessage = ""
				}
			})
		}
	})
	a.dialogs(c, p)
	if (!a.dialogOpen || a.dialog != "imports") && a.imports != nil && a.imports.cancel != nil && !a.imports.applying {
		a.imports.cancel()
		a.imports.cancel = nil
		a.imports.generation++
		a.imports.busy = false
	}
	a.templateDialog(c, p)
	a.paletteView(c, p)
	a.switcherView(c, p)
	a.valuePromptDialog(c, p)
	a.toastsView(c, p)
	a.inView = false
	actions := a.afterInput
	a.afterInput = nil
	for _, action := range actions {
		action()
	}
	if d := a.drafts[a.active]; d != nil && d.Dirty {
		snapshot := stringifyDraft(d)
		if snapshot != d.SavedVersion {
			d.SavedVersion = snapshot
			d.LastEdit = c.Now()
		}
		if c.Now().Sub(d.LastEdit) >= 350*time.Millisecond {
			a.save(d)
		} else {
			c.After(350 * time.Millisecond)
		}
	}
}

// Widgets apply queued text while the view is built. Snapshotting or replacing
// a draft before every input has run can lose the last keystroke in that frame.
func (a *App) deferUntilInputs(action func()) bool {
	if !a.inView {
		return false
	}
	a.afterInput = append(a.afterInput, action)
	return true
}

// headerHeight is the app header's height; the macOS traffic lights are centred in it.
const headerHeight = 40

func (a *App) header(c *ui.Context, p colors) {
	bar := c.TitleBar()
	ui.Row(c).Height(max(headerHeight, bar.Height)).Shrink(0).Padding(0, bar.Right+6, 0, bar.Left+6).Gap(2).Background(p.header).BorderWidth(0, 0, 1, 0).BorderColor(p.border).DragWindow().Children(func() {
		// The active environment's color tints the header, as in Yaak.
		if color, ok := envColor(s(a.models[a.environment], "color"), p); ok {
			tint := func(alpha float32) ui.LinearGradient {
				return ui.LinearGradient{From: color.Alpha(alpha), To: color.Alpha(0), Angle: 90, Start: .15, End: .4}
			}
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).LinearGradient(tint(.07))
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(-1).Height(1).LinearGradient(tint(.2))
		}
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Children(func() {
			glyph := "panelOpen"
			if a.hideSidebar {
				glyph = "panelShut"
			}
			if iconButton(c, glyph, "Toggle sidebar").Clicked() {
				a.hideSidebar = !a.hideSidebar
			}
			if a.workspace != "" {
				iconButton(c, "plusCircle", "Add Resource").Menu(func(m *ui.Menu) { a.createMenu(m, "") })
				a.cookieMenu(c)
			}
			name := "Yeek"
			if w := a.models[a.workspace]; w != nil {
				name = s(w, "name")
			}
			headerMenu(c, name, func(m *ui.Menu) {
				for _, w := range a.list("workspace") {
					if m.Item(s(w, "name")).Checked(s(w, "id") == a.workspace).Chosen() {
						a.openWorkspace(s(w, "id"))
					}
				}
				m.Separator()
				if m.Item("Workspace Settings…").Disabled(a.workspace == "").Chosen() {
					a.prompt("workspace_settings", "Workspace Settings", "", a.workspace)
				}
				if m.Item("Rename Workspace…").Disabled(a.workspace == "").Chosen() {
					a.prompt("rename", "Rename Workspace", s(a.models[a.workspace], "name"), a.workspace)
				}
				if m.Item("Import…").Chosen() {
					a.importFile()
				}
				if m.Item("Export…").Disabled(a.workspace == "").Chosen() {
					a.exportFile()
				}
				m.Separator()
				if m.Item("New Workspace…").Chosen() {
					a.prompt("workspace", "New Workspace", "", "")
				}
				if m.Item("Delete Workspace…").Disabled(a.workspace == "").Chosen() {
					a.prompt("delete", "Delete Workspace", s(a.models[a.workspace], "name"), a.workspace)
				}
			}).TextColor(p.text).MaxWidth(230)
			if a.workspace != "" {
				icon(c, "chevron").FontSize(12).TextColor(p.subtle)
				a.environmentButton(c, p)
			}
		})
		ui.Row(c).Shrink(1).MinWidth(0).MaxWidthPercent(30).Justify(ui.Center).Children(func() {
			active := a.models[a.active]
			if active == nil || a.activeFolder != "" {
				return
			}
			headerMenu(c, requestName(active), func(m *ui.Menu) {
				for _, id := range a.tabs {
					if request := a.models[id]; request != nil {
						if m.Item(requestName(request)).Checked(id == a.active).Chosen() {
							a.openRequest(id)
						}
					}
				}
			}).TextColor(p.text).MinWidth(0)
		})
		ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(2).Justify(ui.End).Children(func() {
			layout, label := "columns", "Change to vertical layout"
			if a.vertical {
				layout, label = "rows", "Change to horizontal layout"
			}
			if iconButton(c, layout, label).Clicked() {
				a.vertical = !a.vertical
				a.requestWidth = 400
			}
			if iconButton(c, "search", "Search or execute a command ("+a.hotkeyText("command_palette.toggle")+")").Clicked() {
				a.togglePalette()
			}
			// Yaak's SettingsDropdown, less its update, license, CLI and feedback items.
			iconButton(c, "gear", "Settings").Menu(func(m *ui.Menu) {
				if a.hotkeyItem(m.Item("Settings"), "settings.show").Chosen() {
					a.prompt("settings", "Settings", "", "")
				}
				if a.hotkeyItem(m.Item("Keyboard shortcuts"), "hotkeys.showHelp").Chosen() {
					a.prompt("shortcuts", "Keyboard Shortcuts", "", "")
				}
				if m.Item("Plugins").Chosen() {
					a.prompt("settings", "Settings", "", "")
					a.modalTab = settingsPlugins
				}
				m.Separator()
				m.Item("Share Workspace(s)").Disabled(true)
				if m.Item("Import Data").Chosen() {
					a.importFile()
				}
				if m.Item("Export Data").Disabled(a.workspace == "").Chosen() {
					a.exportFile()
				}
				m.Separator()
				m.Item("Yeek v" + appVersion).Disabled(true)
			})
		})
	})
}

// environmentButton is Yaak's EnvironmentActionsDropdown: the active
// environment's name, or "Environment" / "No Environment" when only the base
// environment applies. It lists the sub-environments and opens the editor
// directly when there are none.
func (a *App) environmentButton(c *ui.Context, p colors) {
	subs := []engine.Object{}
	hasBaseVars := false
	for _, env := range a.list("environment") {
		switch s(env, "parentModel") {
		case "workspace":
			for _, v := range oslice(env, "variables") {
				if b(v, "enabled") && (s(v, "name") != "" || s(v, "value") != "") {
					hasBaseVars = true
				}
			}
		case "folder":
		default:
			subs = append(subs, env)
		}
	}
	active := a.models[a.environment]
	label := "No Environment"
	switch {
	case active != nil:
		label = s(active, "name")
	case hasBaseVars:
		label = "Environment"
	}
	color, colored := envColor(s(active, "color"), p)
	var button ui.Element
	if len(subs) == 0 {
		button = ui.ButtonBase(c).Label(label).Padding(4, 8).Gap(6).Radius(5)
		if button.Hovered() {
			button.Background(p.border.Alpha(.4))
		}
		button.Children(func() {
			if colored {
				colorDot(c, color, 8)
			}
			ui.Text(c, label).FontSize(13).SingleLine()
		})
		if button.Clicked() {
			a.openEnvironments()
		}
	} else {
		if colored {
			colorDot(c, color, 8)
		}
		button = headerMenu(c, label, func(m *ui.Menu) {
			for _, env := range subs {
				if m.Item(s(env, "name")).Checked(a.environment == s(env, "id")).Chosen() {
					a.environment = s(env, "id")
				}
			}
			m.Separator()
			if m.Item("Manage Environments").Chosen() {
				a.openEnvironments()
			}
		})
	}
	button.TextColor(p.muted).MaxWidth(230)
	if active == nil && !hasBaseVars {
		button.TextColor(p.subtle).Italic()
	}
}

// headerMenu is the borderless dropdown button Yaak uses in its app header.
func headerMenu(c *ui.Context, label string, build func(m *ui.Menu)) ui.Element {
	return ui.MenuButton(c, label, build).Background(ui.Transparent).Border(0, ui.Transparent).FontSize(13).Padding(4, 8)
}

// createMenu is Yaak's getCreateDropdownItems: what it creates goes inside
// folder when it is set, and a folder is offered unless hideFolder.
func (a *App) createMenu(m *ui.Menu, folder string, hideFolder ...bool) {
	for _, item := range []struct{ name, kind string }{{"HTTP", "http_request"}, {"GraphQL", "graphql"}, {"gRPC", "grpc_request"}, {"WebSocket", "websocket_request"}} {
		if m.Item(item.name).Chosen() {
			if item.kind == "graphql" {
				a.newGraphQL(folder)
			} else {
				a.addRequest(item.kind, folder)
			}
			if folder != "" {
				a.expanded[folder] = true
			}
		}
	}
	if len(hideFolder) > 0 && hideFolder[0] {
		return
	}
	m.Separator()
	if m.Item("Folder").Chosen() {
		a.prompt("folder", "New Folder", "Folder", folder)
	}
}
func (a *App) emptyWorkspace(c *ui.Context, p colors) {
	ui.Column(c).Grow(1).Center().Gap(22).Children(func() {
		icon(c, "braces").FontSize(52).TextColor(p.accent)
		ui.Text(c, "Yeek").FontSize(27).FontWeight(600)
		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, "Create Workspace").Padding(9, 18).Clicked() {
				a.prompt("workspace", "New Workspace", "", "")
			}
			if ui.Button(c, "Import Collection").Padding(9, 18).Clicked() {
				a.importFile()
			}
		})
	})
}
func (a *App) sidebar(c *ui.Context, p colors) {
	ui.Column(c).Fill().Background(p.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
		if len(a.treeItems("")) > 0 {
			ui.Row(c).Shrink(0).Padding(12, 2, 0, 12).Gap(2).Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Height(28).Radius(4).Border(1, p.border).Background(p.background).Padding(0, 2, 0, 0).Children(func() {
					filter := ui.TextInput(c, &a.search).Placeholder("Search").Label("Filter requests").Grow(1).MinWidth(0).Height(26).Background(ui.Transparent).Border(0, ui.Transparent).FontSize(12)
					if a.focusFilter {
						a.focusFilter = false
						filter.Focus()
					}
					if filter.Focused() {
						a.sidebarFocused = true
					}
					if a.search != "" && smallIconButton(c, "close", "Clear filter").Clicked() {
						a.search = ""
					}
				})
				iconButton(c, "moreV", "Show sidebar actions menu").Menu(func(m *ui.Menu) {
					if m.Item("Focus Active Request").Disabled(a.active == "").Chosen() {
						a.revealInSidebar(a.active)
					}
					if m.Item("Expand All Folders").Chosen() {
						for _, f := range a.list("folder") {
							a.expanded[s(f, "id")] = true
						}
					}
					if m.Item("Collapse All Folders").Chosen() {
						clear(a.expanded)
					}
				})
			})
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 12, 8, 8).Gap(1).Children(func() {
			if !a.requestTree(c, p) && a.search != "" {
				ui.Column(c).Padding(12).Center().Children(func() {
					ui.Text(c, "No results for “"+a.search+"”").FontSize(12).TextColor(p.muted).MaxLines(2)
				})
			}
		})
		a.gitButton(c, p)
	})
}

// revealInSidebar expands the folders that contain id.
func (a *App) revealInSidebar(id string) {
	for parent, depth := s(a.models[id], "folderId"), 0; parent != "" && depth < 32; parent, depth = s(a.models[parent], "folderId"), depth+1 {
		a.expanded[parent] = true
	}
}

// treeMatch reports whether a sidebar item, or for a folder anything inside it, matches the filter.
func (a *App) treeMatch(m engine.Object, depth int) bool {
	query := strings.ToLower(strings.TrimSpace(a.search))
	if query == "" {
		return true
	}
	text := strings.ToLower(requestName(m) + " " + s(m, "url"))
	if s(m, "model") == "http_request" {
		text = strings.ToLower(s(m, "method")) + " " + text
	}
	if strings.Contains(text, query) {
		return true
	}
	if s(m, "model") != "folder" || depth > 32 {
		return false
	}
	for _, child := range a.treeItems(s(m, "id")) {
		if a.treeMatch(child, depth+1) {
			return true
		}
	}
	return false
}

// treeItems returns a parent's direct children in sidebar order.
func (a *App) treeItems(parent string) []engine.Object {
	items := []engine.Object{}
	for _, kind := range []string{"folder", "http_request", "grpc_request", "websocket_request"} {
		for _, m := range a.list(kind) {
			if s(m, "folderId") == parent {
				items = append(items, m)
			}
		}
	}
	slices.SortStableFunc(items, func(x, y engine.Object) int {
		if n(x, "sortPriority") < n(y, "sortPriority") {
			return -1
		}
		if n(x, "sortPriority") > n(y, "sortPriority") {
			return 1
		}
		if c := strings.Compare(s(x, "createdAt"), s(y, "createdAt")); c != 0 {
			return c
		}
		return strings.Compare(s(x, "id"), s(y, "id"))
	})
	return items
}

// folderRequests returns the HTTP requests under a folder, depth first, in sidebar order.
func (a *App) folderRequests(folder string) []string {
	var ids []string
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		if depth > 32 {
			return
		}
		for _, m := range a.treeItems(parent) {
			switch s(m, "model") {
			case "folder":
				walk(s(m, "id"), depth+1)
			case "http_request":
				ids = append(ids, s(m, "id"))
			}
		}
	}
	walk(folder, 0)
	return ids
}
func (a *App) workbench(c *ui.Context, p colors) {
	if folder := a.models[a.activeFolder]; a.activeFolder != "" && folder != nil {
		a.folderLayout(c, p, folder)
		return
	}
	d := a.drafts[a.active]
	if d == nil {
		ui.Column(c).Fill().Children(func() {
			a.hotkeyList(c, p, []string{"model.create", "sidebar.focus", "settings.show"}, func() {
				ui.Row(c).Gap(6).Justify(ui.Center).Children(func() {
					if ui.Button(c, "Import").FontSize(12).Padding(4, 10).Clicked() {
						a.importFile()
					}
					ui.MenuButton(c, "New Request", func(m *ui.Menu) { a.createMenu(m, "", true) }).FontSize(12).Padding(4, 10)
				})
			})
		})
		return
	}
	request := func() {
		ui.Box(c).Fill().Padding(12, 3, 12, 12).Children(func() { a.requestPane(c, p, d) })
	}
	response := func() {
		ui.Box(c).Fill().Padding(12, 12, 12, 3).Children(func() {
			if d.Kind == "http_request" {
				a.responsePane(c, p, d)
			} else {
				a.protocolResponse(c, p, d)
			}
		})
	}
	if a.vertical {
		request = func() {
			ui.Box(c).Fill().Padding(12, 12, 3, 12).Children(func() { a.requestPane(c, p, d) })
		}
		response = func() {
			ui.Box(c).Fill().Padding(3, 12, 12, 12).Children(func() {
				if d.Kind == "http_request" {
					a.responsePane(c, p, d)
				} else {
					a.protocolResponse(c, p, d)
				}
			})
		}
	}
	panes := func() { ui.SplitQuiet(c, &a.requestWidth, a.vertical, request, response).Fill() }
	if !a.graphQLDocsOpen(d) {
		panes()
		return
	}
	// Yaak's GraphQL layout: the documentation explorer takes a third.
	if a.docsSplit == 0 {
		w, _ := c.Size()
		if !a.hideSidebar {
			w -= float32(a.sidebarWidth)
		}
		a.docsSplit = w * 2 / 3
	}
	ui.SplitQuiet(c, &a.docsSplit, false, panes, func() { a.graphQLDocsPane(c, p, d) }).Fill()
}
func (a *App) urlBar(c *ui.Context, p colors, d *Draft) {
	ui.Row(c).Shrink(0).Children(func() {
		bar := ui.Row(c).Grow(1).MinWidth(0).Height(34).Radius(5).Border(1, p.border).Background(p.background).Gap(2).Padding(2)
		bar.Children(func() {
			if d.Kind == "http_request" {
				method := strings.ToUpper(d.Method)
				ui.MenuButton(c, method, func(m *ui.Menu) {
					for _, option := range []string{"GET", "PUT", "POST", "PATCH", "DELETE", "OPTIONS", "QUERY", "HEAD"} {
						if m.Item(option).Checked(option == method).Chosen() {
							d.Method, d.Dirty = option, true
						}
					}
					m.Separator()
					if m.Item("Custom…").Chosen() {
						a.deferUntilInputs(func() { a.methodError = ""; a.prompt("custom_method", "Custom Method", d.Method, d.ID) })
					}
				}).Label("HTTP method").Height(28).Padding(0, 8).Background(ui.Transparent).Border(0, ui.Transparent).TextColor(a.methodColor(method, p)).Font("monospace").FontSize(12)
			}
			placeholder := map[string]string{"websocket_request": "wss://example.com", "grpc_request": "localhost:50051"}[d.Kind]
			if placeholder == "" {
				placeholder = "https://example.com"
			}
			input := a.templateInput(c, p, &d.URL, "url", "Request URL", placeholder, false).Grow(1).MinWidth(0).Height(28)
			if a.focusURL {
				// ⌘L focuses the URL and selects it, as Yaak's url_bar.focus does.
				a.focusURL = false
				doc := a.editorDocument("input::url")
				doc.state.Focus()
				doc.state.Select(0, utf8.RuneCountInString(d.URL))
			}
			if input.Changed() {
				d.Dirty = true
				a.handleURLPaste(d)
			}
			if input.Submitted() {
				a.send()
			}
			switch d.Kind {
			case "grpc_request":
				// Its buttons are beside the bar (grpcControls).
			case "websocket_request":
				name, label := "arrowUpDown", "Connect"
				if a.connected(d) {
					if smallIconButton(c, "close", "Close connection").Clicked() {
						id := s(a.connections[d.ID], "id")
						a.run(func() (func(), error) { return nil, a.Engine.CloseWebSocket(id) })
					}
					name, label = "send", "Send Message"
				}
				if iconButton(c, name, label).Width(32).Clicked() {
					a.send()
				}
			default:
				name, label := "send", "Send Request"
				if a.running[d.ID] {
					name, label = "close", "Cancel Request"
				}
				if iconButton(c, name, label).Width(32).Clicked() {
					a.send()
				}
			}
		})
		if d.Kind == "grpc_request" {
			ui.Row(c).Shrink(0).Gap(6).Margin(0, 0, 0, 6).Children(func() { a.grpcControls(c, p, d) })
		}
	})
}

// tabItem is one of Yaak's text tabs: an optional count badge, and a menu
// (a chevron) that opens when the tab is already selected.
type tabItem struct {
	Label string
	Badge *countBadge
	Menu  func(m *ui.Menu)
}

// countBadge is Yaak's CountBadge: a number or a dot, optionally paired with a
// second as "sent/received".
type countBadge struct {
	count, count2 int
	dot, dot2     bool
	pair          bool
}

// badgeCount is a badge for n, or none when it is zero.
func badgeCount(n int) *countBadge {
	if n == 0 {
		return nil
	}
	return &countBadge{count: n}
}

// badgeDot is a dot marking that something is set.
func badgeDot(on bool) *countBadge {
	if !on {
		return nil
	}
	return &countBadge{dot: true}
}

// badgePair shows both counts, zeros included.
func badgePair(a, b int) *countBadge { return &countBadge{count: a, count2: b, pair: true} }

// viewSingle shows only the first count, zero included, as CountBadge's showZero.
func (b *countBadge) viewSingle(c *ui.Context, p colors) {
	(&countBadge{count: b.count}).view(c, p)
}

func (b *countBadge) view(c *ui.Context, p colors) {
	if b == nil {
		return
	}
	part := func(n int, dot bool) {
		if dot {
			ui.Box(c).Size(4, 4).Radius(2).Background(p.muted)
			return
		}
		ui.Text(c, fmt.Sprint(n)).Font("monospace").FontSize(9).TextColor(p.muted)
	}
	ui.Row(c).Height(16).Padding(0, 4).Gap(1).Radius(2).Border(1, p.border).Opacity(.7).Children(func() {
		part(b.count, b.dot)
		if b.pair {
			ui.Text(c, "/").Font("monospace").FontSize(9).TextColor(p.muted)
			part(b.count2, b.dot2)
		}
	})
}

func tabBar(c *ui.Context, p colors, index *int, items []tabItem) {
	parts := ui.TabsBase(c, index, len(items))
	parts.List.Height(32).Padding(0, 4).Gap(1).Children(func() {
		for i, item := range items {
			active := *index == i
			tab := parts.Tab(i).Height(26).Padding(0, 8).Gap(4).Radius(4).Justify(ui.Center).Label(item.Label)
			if tab.Hovered() && !active {
				tab.Background(p.border.Alpha(.3))
			}
			if active && item.Menu != nil {
				tab.Menu(item.Menu)
			}
			color := p.muted
			if active {
				color = p.text
			}
			tab.Children(func() {
				ui.Text(c, item.Label).FontSize(13).TextColor(color).SingleLine()
				item.Badge.view(c, p)
				if item.Menu != nil {
					chevron := p.subtle
					if active {
						chevron = p.muted
					}
					icon(c, "down").FontSize(11).TextColor(chevron)
				}
			})
		}
	})
}
func (a *App) requestPane(c *ui.Context, p colors, d *Draft) {
	if d.Kind == "http_request" || d.Kind == "websocket_request" {
		a.syncPathPlaceholders(d)
	}
	ui.Column(c).Fill().MinWidth(0).Gap(4).Children(func() {
		a.urlBar(c, p, d)
		if d.Kind != "http_request" {
			a.protocolRequest(c, p, d)
			return
		}
		tabBar(c, p, &d.Tab, []tabItem{
			{Label: bodyTabLabel(d.BodyType), Badge: badgeCount(countNamed(d.Form, d.BodyType == "application/x-www-form-urlencoded" || d.BodyType == "multipart/form-data")), Menu: func(m *ui.Menu) { a.bodyTypeMenu(m, d) }},
			{Label: "Params", Badge: badgeCount(countNamed(d.Parameters, true))},
			{Label: "Headers", Badge: badgeCount(a.headerCount(d))},
			{Label: authTabLabel(a, d.AuthType), Menu: func(m *ui.Menu) { a.authTypeMenu(m, d) }},
			{Label: "Settings", Badge: badgeCount(overriddenSettings(d))},
			{Label: "Info", Badge: badgeDot(strings.TrimSpace(d.Description) != "")},
		})
		switch d.Tab {
		case 0:
			if a.confirmLargeRequestBody(c, p, d) {
				a.bodyEditor(c, p, d)
			}
		case 1:
			a.kvEditor(c, p, &d.Parameters, "Parameter", "Value", &d.Dirty)
		case 2:
			a.headersEditor(c, p, d)
		case 3:
			a.authEditor(c, p, d)
		case 4:
			a.requestSettings(c, p, d)
		case 5:
			a.descriptionEditor(c, p, d, "Request description")
		}
	})
}

// headerCount follows Yaak's Headers tab badge: inherited headers plus the draft's own, by name.
func (a *App) headerCount(d *Draft) int {
	return len(a.inheritedHeaders(d.Model)) + countNamed(d.Headers, true)
}

// overriddenSettings counts the settings a request or folder overrides, as Yaak's Settings badge does.
func overriddenSettings(d *Draft) int {
	keys := []string{"settingValidateCertificates"}
	if d.Kind != "grpc_request" {
		keys = append(keys, "settingSendCookies", "settingStoreCookies")
	}
	if d.Kind == "http_request" || d.Kind == "folder" {
		keys = append(keys, "settingFollowRedirects", "settingRequestTimeout", "settingHttpVersion")
	}
	if d.Kind != "http_request" {
		keys = append(keys, "settingRequestMessageSize")
	}
	count := 0
	for _, key := range keys {
		if b(o(d.Model, key), "enabled") {
			count++
		}
	}
	return count
}
func cookieBadge(response engine.Object) *countBadge {
	sent, received := engine.ResponseCookies(response)
	if len(sent) == 0 && len(received) == 0 {
		return nil
	}
	return badgePair(len(sent), len(received))
}
func (a *App) responseEventCount(response engine.Object) int {
	count := 0
	for _, m := range a.list("http_response_event") {
		if s(m, "responseId") == s(response, "id") {
			count++
		}
	}
	return count
}
func countNamed(rows []KV, enabled bool) int {
	if !enabled {
		return 0
	}
	count := 0
	for _, row := range rows {
		if strings.TrimSpace(row.Name) != "" {
			count++
		}
	}
	return count
}

type bodyTypeOption struct{ label, short, value string }

// bodyTypes mirrors the body menu of Yaak's request pane; an empty label is a separator.
var bodyTypes = []bodyTypeOption{
	{"Url Encoded", "", "application/x-www-form-urlencoded"},
	{"Multi-Part", "", "multipart/form-data"},
	{},
	{"GraphQL", "", "graphql"},
	{"JSON", "", "application/json"},
	{"XML", "", "application/xml"},
	{"Other", "Text", "text/plain"},
	{},
	{"Binary File", "", "binary"},
	{"No Body", "Body", ""},
}

func bodyTabLabel(bodyType string) string {
	for _, option := range bodyTypes {
		if option.label != "" && option.value == bodyType {
			return cmp.Or(option.short, option.label)
		}
	}
	switch {
	case strings.Contains(bodyType, "json"):
		return "JSON"
	case strings.Contains(bodyType, "xml"):
		return "XML"
	}
	return "Text"
}
func (a *App) bodyTypeMenu(m *ui.Menu, d *Draft) {
	current := d.BodyType
	if bodyTabLabel(current) == "Text" {
		current = "text/plain"
	}
	for _, option := range bodyTypes {
		if option.label == "" {
			m.Separator()
			continue
		}
		if m.Item(option.label).Checked(option.value == current).Chosen() && option.value != current {
			a.changeBodyType(d, option.value)
		}
	}
}

type authOption struct{ label, short, value string }

func (a *App) authOptions() []authOption {
	options := []authOption{{"Basic Auth", "Basic", "basic"}, {"Bearer Token", "Bearer", "bearer"}, {"API Key", "API Key", "apikey"}, {"OAuth 2.0", "OAuth 2", "oauth2"}, {"OAuth 1.0", "OAuth 1", "oauth1"}, {"Digest", "Digest", "digest"}, {"NTLM", "NTLM", "windows"}, {"JWT Bearer", "JWT", "jwt"}, {"AWS Signature", "AWS", "awsv4"}}
	for _, auth := range a.Engine.PluginAuthentication() {
		options = append(options, authOption{auth.Label, auth.Label, auth.Name})
	}
	return options
}
func authTabLabel(a *App, authType string) string {
	switch authType {
	case "":
		return "Auth"
	case "none":
		return "No Auth"
	}
	for _, option := range a.authOptions() {
		if option.value == authType {
			return option.short
		}
	}
	return "Auth"
}
func (a *App) authTypeMenu(m *ui.Menu, d *Draft) {
	choose := func(value string) { a.deferUntilInputs(func() { d.AuthType = value; d.Dirty = true }) }
	for _, option := range a.authOptions() {
		if m.Item(option.label).Checked(d.AuthType == option.value).Chosen() {
			choose(option.value)
		}
	}
	m.Separator()
	if d.Kind == "workspace" {
		// A workspace has nothing to inherit from: no type means no authentication.
		if m.Item("No Auth").Checked(d.AuthType == "" || d.AuthType == "none").Chosen() {
			choose("")
		}
		return
	}
	if m.Item("Inherit from Parent").Checked(d.AuthType == "").Chosen() {
		choose("")
	}
	if m.Item("No Auth").Checked(d.AuthType == "none").Chosen() {
		choose("none")
	}
}

// inheritedHeaders are the enabled headers a request or folder receives from
// Yeek's defaults, its workspace and its folders, the innermost winning, as Yaak shows them.
func (a *App) inheritedHeaders(m engine.Object) []KV {
	if s(m, "model") == "workspace" {
		return kvRows(engine.Object{"headers": a.Engine.DefaultHeaders()}, "headers")
	}
	chain := []engine.Object{}
	for id, depth := s(m, "folderId"), 0; id != "" && depth < 32; id, depth = s(a.models[id], "folderId"), depth+1 {
		if folder := a.models[id]; folder != nil {
			chain = append(chain, folder)
		}
	}
	rows := kvRows(engine.Object{"headers": a.Engine.DefaultHeaders()}, "headers")
	rows = append(rows, kvRows(a.models[s(m, "workspaceId")], "headers")...)
	for i := len(chain) - 1; i >= 0; i-- {
		rows = append(rows, kvRows(chain[i], "headers")...)
	}
	merged := []KV{}
	for _, row := range rows {
		if !row.Enabled || row.Name == "" && row.Value == "" {
			continue
		}
		merged = slices.DeleteFunc(merged, func(h KV) bool { return strings.EqualFold(h.Name, row.Name) })
		merged = append(merged, row)
	}
	return merged
}

// headersEditor is the Headers tab: a collapsible list of inherited headers
// that the draft does not override, above its own headers.
func (a *App) headersEditor(c *ui.Context, p colors, d *Draft) {
	own := map[string]bool{}
	for _, h := range d.Headers {
		if h.Name != "" {
			own[strings.ToLower(h.Name)] = true
		}
	}
	inherited := slices.DeleteFunc(a.inheritedHeaders(d.Model), func(h KV) bool { return own[strings.ToLower(h.Name)] })
	ui.Column(c).Grow(1).MinHeight(0).Gap(6).Padding(6, 0, 0, 0).Children(func() {
		if len(inherited) > 0 {
			open := !d.InheritedClosed
			box := ui.Column(c).Shrink(0).Radius(6).Border(1, p.border).Background(p.border.Alpha(.18)).Padding(2, 0, 2, 2)
			box.Children(func() {
				summary := ui.ButtonBase(c).Label("Inherited headers").Height(28).Gap(6).Justify(ui.Start)
				summary.Children(func() {
					glyph := "chevron"
					if open {
						glyph = "down"
					}
					icon(c, glyph).FontSize(11).TextColor(p.subtle)
					label := "Inherited"
					if d.Kind == "workspace" {
						label = "Defaults"
					}
					ui.Text(c, label).FontSize(12).TextColor(p.muted)
					ui.Text(c, fmt.Sprint(len(inherited))).FontSize(10).TextColor(p.muted).Padding(0, 5).Radius(8).Border(1, p.border)
				})
				if summary.Clicked() {
					d.InheritedClosed = open
				}
				if !open {
					return
				}
				ui.Column(c).Gap(4).Padding(2, 0, 6, 0).Children(func() {
					for i, h := range inherited {
						ui.Row(c).Key(i).Height(28).Gap(6).Opacity(.6).Children(func() {
							on := true
							ui.Checkbox(c, &on, "").Label("Inherited " + h.Name).Width(22).Disabled(true)
							for _, text := range []string{h.Name, h.Value} {
								ui.Row(c).Grow(1).Basis(0).MinWidth(0).Height(28).Padding(0, 8).Radius(4).Border(1, p.border).Background(p.background).Children(func() {
									ui.Text(c, text).Font("monospace").FontSize(12).SingleLine().MinWidth(0).Selectable()
								})
							}
							ui.Box(c).Width(22).Shrink(0)
						})
					}
				})
			})
		}
		a.kvEditor(c, p, &d.Headers, "Header", "Value", &d.Dirty)
	})
}

// kvEditor edits name/value rows; text rows can switch to bulk editing as in
// Yaak, while multipart rows, which can hold files, stay rows.
func (a *App) kvEditor(c *ui.Context, p colors, rows *[]KV, name, value string, dirty *bool, files ...bool) {
	if len(files) > 0 && files[0] {
		a.pairEditor(c, p, rows, name, value, dirty, files...)
		return
	}
	a.pairOrBulkEditor(c, p, rows, name, value, dirty)
}
func (a *App) pairEditor(c *ui.Context, p colors, rows *[]KV, name, value string, dirty *bool, files ...bool) {
	namePlaceholder := "name"
	if name == "Header" {
		namePlaceholder = "Header-Name"
	}
	if len(*rows) == 0 || (*rows)[len(*rows)-1].Name != "" || (*rows)[len(*rows)-1].Value != "" || (*rows)[len(*rows)-1].FileMode || (*rows)[len(*rows)-1].File != "" {
		*rows = append(*rows, KV{Enabled: true})
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(8, 12, 8, 2).Gap(4).Children(func() {
		deleted := -1
		for i := range *rows {
			row := &(*rows)[i]
			if row.ID == "" {
				row.ID = uuid.NewV4().String()
			}
			empty := row.Name == "" && row.Value == "" && !row.FileMode && row.File == ""
			line := ui.Row(c).Key(row.ID).Height(28).Gap(6)
			if !row.Enabled && !empty {
				line.Opacity(.6)
			}
			border := p.border
			if empty {
				border = p.border.Alpha(.6)
			}
			// Secret header values are obscured, as Yaak's password inputs are.
			mask := a.kvMask || name == "Header" && secretHeader(row.Name)
			line.Children(func() {
				if ui.Checkbox(c, &row.Enabled, "").Label(fmt.Sprintf("Enable %s %d", name, i+1)).Width(22).Opacity(map[bool]float32{true: .3, false: 1}[empty]).Changed() {
					*dirty = true
				}
				var nameInput ui.Element
				nameBorder := border
				if name == "Variable" {
					nameInput = ui.TextInput(c, &row.Name).Label(fmt.Sprintf("%s %d", name, i+1)).Placeholder("VAR_NAME").Font("monospace").FontSize(12)
					// Yaak's variable names: a letter or underscore, then letters, digits, _ . or -.
					if row.Name != "" && !variableNameRaw.MatchString(row.Name) {
						nameBorder = p.red
					}
				} else {
					if name == "Header" {
						// Yaak's header name and value completions.
						doc := a.templateDoc(row.ID + ":name")
						doc.presets, doc.presetMin = headerNameOptions, headerMinMatch
						doc = a.templateDoc(row.ID + ":value")
						doc.presets, doc.presetMin = headerValuePresets(row.Name), headerMinMatch
						if !validHeaderName(row.Name) {
							nameBorder = p.red
						}
					}
					nameInput = a.templateInput(c, p, &row.Name, row.ID+":name", fmt.Sprintf("%s %d", name, i+1), namePlaceholder, false)
				}
				if nameInput.Grow(1).MinWidth(0).Height(28).Background(p.background).Border(1, nameBorder).Radius(4).Changed() {
					*dirty = true
				}
				if len(files) > 0 && files[0] {
					a.multipartValue(c, p, rows, i, dirty)
				} else if mask && !empty && !a.revealed[row.ID] && !a.templateDoc(row.ID+":value").state.Focused {
					// A masked value, as in Yaak's environment editor until "Show Values":
					// clicking it edits it as a template again.
					masked := ui.ButtonBase(c).Label(fmt.Sprintf("%s value %d", name, i+1)).Grow(1).MinWidth(0).Height(28).Padding(0, 8).Radius(4).Border(1, border).Background(p.background).Justify(ui.Start)
					masked.Children(func() {
						ui.Text(c, strings.Repeat("•", min(utf8.RuneCountInString(row.Value), 24))).Font("monospace").FontSize(12).TextColor(p.muted).SingleLine()
					})
					if masked.Clicked() {
						a.revealRow(row.ID, true)
						a.templateDoc(row.ID + ":value").state.Focus()
					}
				} else if a.templateInput(c, p, &row.Value, row.ID+":value", fmt.Sprintf("%s value %d", name, i+1), "value", false).Grow(1).MinWidth(0).Height(28).Background(p.background).Border(1, border).Radius(4).Changed() {
					*dirty = true
				}
				if mask && !empty {
					glyph, label := "eye", "Show "+name+" value"
					if a.revealed[row.ID] {
						glyph, label = "eyeOff", "Obscure "+name+" value"
					}
					if smallIconButton(c, glyph, label).Clicked() {
						a.revealRow(row.ID, !a.revealed[row.ID])
					}
				}
				smallIconButton(c, "more", fmt.Sprintf("Actions for %s %d", name, i+1)).Menu(func(m *ui.Menu) {
					if m.Item("Delete").Chosen() {
						deleted = i
					}
					if m.Item("Duplicate").Chosen() {
						copy := *row
						copy.ID = uuid.NewV4().String()
						*rows = append(*rows, copy)
						*dirty = true
					}
					if len(files) > 0 && files[0] {
						if m.Item("Field Options…").Chosen() {
							a.openMultipartPart(row.ID)
						}
						if m.Item("Choose file…").Chosen() {
							a.pickFormFile(rows, row.ID, dirty)
						}
						if row.FileMode && m.Item("Use text value").Chosen() {
							a.changeFormKind(rows, row.ID, false, dirty)
						}
					}
					fieldValue := row.Value
					if row.FileMode {
						fieldValue = row.File
					}
					if m.Item("Encrypt value").Disabled(fieldValue == "").Chosen() {
						id, value, workspace, file := row.ID, fieldValue, a.workspace, row.FileMode
						a.run(func() (func(), error) {
							secured, err := a.Engine.SecureValue(a.ctx, workspace, value)
							return func() {
								for i := range *rows {
									if (*rows)[i].ID == id && (*rows)[i].FileMode == file {
										if file {
											(*rows)[i].File = secured
										} else {
											(*rows)[i].Value = secured
										}
										*dirty = true
									}
								}
							}, err
						})
					}
					if m.Item("Copy value").Chosen() {
						c.WriteClipboard(fieldValue)
					}
				})
			})
		}
		if deleted >= 0 {
			*rows = slices.Delete(*rows, deleted, deleted+1)
			*dirty = true
		}
	})
}
func (a *App) bodyEditor(c *ui.Context, p colors, d *Draft) {
	if d.BodyType == "application/json" {
		a.bodyTools(c, p, d)
	}
	a.bodyContent(c, p, d)
}
func (a *App) bodyTools(c *ui.Context, p colors, d *Draft) {
	ui.Row(c).Height(32).Padding(0, 4).Gap(6).Children(func() {
		ui.Spacer(c)
		if d.BodyType == "application/json" {
			iconButton(c, "gear", "JSON body settings").Menu(func(menu *ui.Menu) {
				body := o(d.Model, "body")
				if menu.Item("Strip Comments and Trailing Commas").Checked(!b(body, "sendJsonComments")).Chosen() {
					body["sendJsonComments"] = !b(body, "sendJsonComments")
					d.Model["body"] = body
					d.Dirty = true
				}
			})
		}
		if ui.Button(c, "Format").FontSize(11).Clicked() {
			a.formatRequestBody(d)
		}
	})
}
func (a *App) bodyContent(c *ui.Context, p colors, d *Draft) {
	if d.BodyType == "application/x-www-form-urlencoded" || d.BodyType == "multipart/form-data" {
		a.kvEditor(c, p, &d.Form, "Name", "Value", &d.Dirty, d.BodyType == "multipart/form-data")
		return
	}
	if d.BodyType == "" {
		ui.Column(c).Grow(1).Center().Children(func() { ui.Text(c, "No Body").FontSize(13).TextColor(p.subtle) })
		return
	}
	if d.BodyType == "graphql" {
		a.graphQLQueryHeader(c, p, d)
		// Yaak's GraphQLEditor: the query with its actions in the corner,
		// then the variables under a dashed rule.
		ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			a.codeInput(c, &d.Query, "GraphQL query", &d.Dirty)
			ui.Row(c).Absolute().Top(4).Right(16).Gap(4).Children(func() {
				a.graphQLTools(c, p, d)
				if ui.Button(c, "Format").FontSize(11).Padding(2, 8).Clicked() {
					a.formatRequestBody(d)
				}
			})
		})
		ui.Row(c).Padding(4, 12).Gap(8).Children(func() {
			dashed := func() {
				ui.Box(c).Grow(1).Height(1).Children(func() {}).Draw(func(pt *ui.Painter, r ui.Rect) {
					for x := r.X; x < r.X+r.W; x += 6 {
						pt.Fill(ui.Rect{X: x, Y: r.Y, W: 3, H: 1}, p.border, 0)
					}
				})
			}
			dashed()
			ui.Text(c, "Variables").FontSize(12).TextColor(p.muted)
			dashed()
		})
		ui.Box(c).Height(120).Shrink(0).Children(func() { a.codeInput(c, &d.Variables, "GraphQL variables", &d.Dirty) })
		a.graphQLProblems(c, p, d)
		return
	}
	if d.BodyType == "binary" {
		a.binaryBodyEditor(c, p, d)
		return
	}
	a.codeInput(c, &d.Body, "Request body", &d.Dirty)
}

// descriptionEditor is Yaak's Info tab: the name as an editable heading over
// the description.
func (a *App) descriptionEditor(c *ui.Context, p colors, d *Draft, label string) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(4).Padding(4, 0, 0, 0).Children(func() {
		name := ui.TextInput(c, &d.Name).Label("Name").Placeholder(requestName(d.Model)).FillWidth().Height(36).Padding(0, 4).Background(ui.Transparent).Border(0, ui.Transparent).FontSize(19)
		if name.Changed() {
			d.Dirty = true
		}
		a.markdownEditor(c, p, d, label)
	})
}

// markdownEditor is Yaak's MarkdownEditor: the description rendered until the
// pencil switches to editing, which it starts in when there is none.
func (a *App) markdownEditor(c *ui.Context, p colors, d *Draft, label string) {
	if d.DescriptionMode == "" {
		d.DescriptionMode = "preview"
		if strings.TrimSpace(d.Description) == "" {
			d.DescriptionMode = "edit"
		}
	}
	area := ui.Box(c).Grow(1).MinHeight(0)
	area.Children(func() {
		if d.DescriptionMode == "edit" {
			ui.Column(c).Fill().Children(func() { a.codeInput(c, &d.Description, label, &d.Dirty) })
		} else {
			ui.Scroll(c).Fill().Padding(6, 44, 12, 4).Children(func() {
				if strings.TrimSpace(d.Description) == "" {
					ui.Text(c, "No description").FontSize(13).TextColor(p.subtle)
					return
				}
				markdownView(c, p, d.Description)
			})
		}
		if !area.Hovered() && d.DescriptionMode == "preview" {
			return
		}
		ui.Row(c).Absolute().Top(4).Right(4).Gap(2).Padding(2).Radius(6).Background(p.border.Alpha(.45)).Children(func() {
			for _, mode := range []struct{ value, glyph, label string }{{"preview", "eye", "Preview mode"}, {"edit", "pencil", "Edit mode"}} {
				button := sizedIconButton(c, mode.glyph, mode.label, 26, 15)
				if d.DescriptionMode == mode.value {
					button.Background(p.background).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, .12))
				}
				if button.Clicked() {
					d.DescriptionMode = mode.value
				}
			}
		})
	})
}
func (a *App) codeInput(c *ui.Context, value *string, label string, dirty *bool) {
	language := "text"
	switch label {
	case "GraphQL query":
		language = "graphql"
	case "GraphQL variables":
		language = "json"
	case "Request description", "Folder description", "Workspace description":
		language = "markdown"
	case "Message body":
		language = "json"
	case "Request body":
		if d := a.drafts[a.active]; d != nil {
			switch d.BodyType {
			case "application/json":
				language = "json"
			case "application/xml":
				language = "xml"
			case "text/html":
				language = "html"
			case "text/yaml", "application/yaml":
				language = "yaml"
			}
		}
	}
	if a.nativeEditor(c, a.theme(c), value, label, language, false) {
		*dirty = true
	}
}

func (a *App) authEditor(c *ui.Context, p colors, d *Draft) {
	switch d.AuthType {
	case "":
		message := "Authentication is inherited from the folder or workspace"
		if d.Kind == "workspace" {
			message = "No authentication"
		}
		ui.Column(c).Grow(1).Center().Children(func() { ui.Text(c, message).FontSize(13).TextColor(p.subtle) })
		return
	case "none":
		ui.Column(c).Grow(1).Center().Children(func() { ui.Text(c, "No authentication").FontSize(13).TextColor(p.subtle) })
		return
	}
	ui.Scroll(c).Grow(1).Padding(8, 12, 8, 4).Gap(16).Children(func() {
		a.authEnabledControl(c, p, d)
		ui.Column(c).Gap(16).Disabled(d.AuthDisabled == true).Children(func() { a.authFields(c, p, d) })
	})
}

// authFields is the form for the chosen authentication type.
func (a *App) authFields(c *ui.Context, p colors, d *Draft) {
	if d.AuthType == "oauth2" {
		a.oauthEditor(c, p, d)
		return
	}
	fields := []string{}
	switch d.AuthType {
	case "basic":
		fields = []string{"username", "password"}
	case "bearer":
		fields = []string{"token", "prefix"}
		if _, ok := d.Auth["prefix"]; !ok {
			d.Auth["prefix"] = "Bearer"
		}
	case "apikey":
		fields = []string{"location", "key", "value"}
	case "oauth1":
		fields = []string{"consumerKey", "consumerSecret", "tokenKey", "tokenSecret", "signatureMethod", "privateKey"}
	case "digest":
		fields = []string{"username", "password", "realm"}
	case "windows":
		fields = []string{"username", "password", "domain", "workstation"}
	case "jwt":
		fields = []string{"algorithm", "secret", "payload", "headers", "location", "name", "headerPrefix"}
	case "awsv4":
		fields = []string{"accessKeyId", "secretAccessKey", "region", "service", "sessionToken"}
	}
	for _, auth := range a.Engine.PluginAuthentication() {
		if auth.Name == d.AuthType {
			for _, field := range auth.Fields {
				fields = append(fields, field.Name)
				if _, ok := d.Auth[field.Name]; !ok {
					d.Auth[field.Name] = field.Default
				}
			}
		}
	}
	for _, field := range fields {
		value := d.Auth[field]
		ui.Column(c).Key(field).Gap(6).Children(func() {
			ui.Text(c, fieldLabel(field)).FontSize(12).TextColor(p.muted)
			options := map[string][]string{"location": {"header", "query"}, "credentials": {"header", "body"}, "grantType": {"client_credentials", "authorization_code", "password", "refresh_token"}, "algorithm": {"HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA"}, "signatureMethod": {"HMAC-SHA1", "HMAC-SHA256", "RSA-SHA1", "PLAINTEXT"}}
			if choices := options[field]; len(choices) > 0 {
				if value == "" {
					value = choices[0]
					d.Auth[field] = value
				}
				if ui.Select(c, &value, choices).Label(fieldLabel(field)).FillWidth().Changed() {
					d.Auth[field] = value
					d.Dirty = true
				}
				return
			}
			if field == "payload" || field == "headers" || field == "privateKey" || (field == "secret" && !strings.HasPrefix(d.Auth["algorithm"], "HS") && d.Auth["algorithm"] != "") {
				if ui.TextArea(c, &value).Height(100).Label(fieldLabel(field)).Font("monospace").FontSize(12).Changed() {
					d.Auth[field] = value
					d.Dirty = true
				}
				return
			}
			entry := a.templateInput(c, p, &value, d.ID+":auth:"+field, fieldLabel(field), "", false).FillWidth().Height(32).Border(1, p.border).Radius(4)
			if field == "password" || strings.Contains(strings.ToLower(field), "secret") {
				entry.Password()
			}
			if entry.Changed() {
				d.Auth[field] = value
				d.Dirty = true
			}
		})
	}
	if d.AuthType == "" {
		ui.Text(c, "Authentication is inherited from the parent folder or workspace.").FontSize(12).TextColor(p.muted)
	}
}
func (a *App) requestSettings(c *ui.Context, p colors, d *Draft) {
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 16, 0, 4).Children(func() { a.modelSettingsEditor(c, p, d, false) })
}
func (a *App) responsePane(c *ui.Context, p colors, d *Draft) {
	response := a.activeResponse(d.ID)
	ui.Column(c).Fill().MinWidth(0).Background(p.response).Radius(6).Border(1, p.border).Children(func() {
		if response == nil {
			a.hotkeyList(c, p, []string{"request.send", "model.create", "sidebar.focus", "url_bar.focus"})
			return
		}
		ui.Row(c).Height(36).Shrink(0).Padding(0, 6, 0, 12).Gap(8).Children(func() {
			if s(response, "state") != "closed" {
				ui.Spinner(c).Size(12, 12).TextColor(p.subtle)
			}
			mono := func(text string, color ui.Color) {
				ui.Text(c, text).Font("monospace").FontSize(12).TextColor(color).SingleLine()
			}
			mono(statusLabel(response, false), statusColor(response, p))
			mono("•", p.subtle)
			mono(formatMillis(n(response, "elapsed")), p.muted)
			mono("•", p.subtle)
			mono(sizeText(n(response, "contentLength")), p.muted)
			ui.Spacer(c)
			a.historyIcon(c, d.ID, response).Menu(func(m *ui.Menu) { a.responseHistoryMenu(m, d.ID, response) })
		})
		if message := s(response, "error"); message != "" {
			ui.Text(c, message).Margin(4, 12, 0, 12).Padding(8, 10).Radius(5).Background(p.red.Alpha(.1)).Border(1, p.red.Alpha(.35)).TextColor(p.red).FontSize(12).MaxLines(6).Selectable()
		}
		if d.ResponseMode == "" {
			d.ResponseMode = "Pretty"
		}
		ui.Box(c).Padding(0, 8).Children(func() {
			tabBar(c, p, &d.ResponseTab, []tabItem{
				{Label: responseTabLabel(d.ResponseMode), Menu: func(m *ui.Menu) { a.responseViewMenu(c, m, d, response) }},
				{Label: "Request", Badge: badgeDot(n(response, "requestContentLength") > 0)},
				{Label: "Headers", Badge: badgePair(len(oslice(response, "requestHeaders")), len(oslice(response, "headers")))},
				{Label: "Cookies", Badge: cookieBadge(response)},
				{Label: timelineTabLabel(d), Badge: badgeCount(a.responseEventCount(response)), Menu: func(m *ui.Menu) { timelineMenu(m, d) }},
			})
		})
		switch d.ResponseTab {
		case 0:
			if !a.confirmLargeResponse(c, p, response) {
				break
			}
			if n(response, "contentLength") > 2<<20 {
				ui.Text(c, "Showing the first 2 MB. Save the response to inspect the complete body.").FontSize(11).TextColor(p.muted).Padding(8, 12)
			}
			a.responseBody(c, p, d, response)
		case 1:
			if !a.confirmLargeResponseRequest(c, p, response) {
				break
			}
			rid := s(response, "id") + ".request"
			if _, loaded := a.bodies[rid]; !loaded {
				a.bodies[rid] = ""
				a.run(func() (func(), error) {
					// A request sent without a body has no stored body file.
					data, _, err := a.Engine.BodyPreview(rid, 2<<20)
					if err != nil {
						return nil, nil
					}
					return func() { a.bodies[rid] = string(data) }, nil
				})
			}
			// Yaak's Request tab is the body that was sent; its URL and headers are under Headers.
			if requestBody := a.bodies[rid]; requestBody == "" {
				emptyState(c, p, "No request body")
			} else {
				a.codeView(c, p, requestBody, "json", "Sent request body")
			}
		case 2:
			a.responseHeaders(c, p, response)
		case 3:
			a.responseCookies(c, p, response)
		case 4:
			a.timelineView(c, p, d, response)
		}
	})
}

// responseText is Yaak's HTMLOrTextViewer text path: the body, formatted
// when pretty, with the filter, breadcrumb and floating actions.
func responseBodyKey(response engine.Object) string {
	return "Response body:" + s(response, "id")
}

func (a *App) responseText(c *ui.Context, p colors, d *Draft, response engine.Object, pretty bool) {
	body := a.bodies[s(response, "id")]
	if d.PrettySource != body {
		d.PrettySource = body
		d.PrettyBody = body
		// Reformat in place, as Yaak does: keys keep their order, numbers
		// keep their digits, and escapes such as \u00e9 and \/ read plainly.
		formatted := jsontext.Value(body)
		if formatted.Indent(jsontext.WithIndent("  "), jsontext.PreserveRawStrings(false)) == nil {
			d.PrettyBody = string(formatted)
		}
	}
	body = d.PrettyBody
	if !pretty {
		body = a.bodies[s(response, "id")]
	}
	language := ""
	switch kind := responseMIME(response); {
	case strings.Contains(kind, "json") || jsontext.Value(body).IsValid():
		language = "json"
	case strings.Contains(kind, "xml"):
		language = "xml"
	case strings.Contains(kind, "html"):
		language = "html"
	}
	// Yaak filters JSON with JSONPath and XML or HTML with XPath.
	filterable := language != ""
	if filterable && d.ResponseFilter != "" {
		source := body + "\x00" + d.ResponseFilter + "\x00" + d.FilterProvider
		if d.FilterSource != source {
			d.FilterSource = source
			d.FilterPending = true
			input, expression, provider, workspace := body, d.ResponseFilter, d.FilterProvider, a.workspace
			a.background(func() (func(), error) {
				filtered, err := a.Engine.Filter(a.ctx, input, expression, provider, workspace)
				return func() {
					if d.FilterSource != source {
						return
					}
					d.FilterPending = false
					d.FilteredBody = filtered
					d.FilterError = ""
					if err != nil {
						d.FilterError = err.Error()
					}
				}, nil
			})
		}
		switch {
		case d.FilterError != "":
			body = ""
		case !d.FilterPending:
			body = d.FilteredBody
		}
	}

	area := ui.Box(c).Grow(1).MinHeight(0)
	area.Children(func() {
		// Like Yaak's response.body.<id> state key, each response opens at
		// the top and keeps its own scroll and caret.
		a.keyedEditor(c, p, &body, "Response body", responseBodyKey(response), language, true)
		if filterable {
			a.responseBreadcrumbs(c, p, d, body, language, responseBodyKey(response))
		}
		a.responseBodyActions(c, p, d, response, filterable, area.Hovered())
	})
}

// responseBreadcrumbs is Yaak's ResponseBreadcrumbBar: the JSON path of the
// line under the caret, where a crumb filters the response to that path and
// "$" clears the filter. An applied plain-path filter starts the trail, and
// the bar is tinted while a filter applies.
func (a *App) responseBreadcrumbs(c *ui.Context, p colors, d *Draft, body, language, stateKey string) {
	var cursor []pathSegment
	if language == "json" {
		if d.outline == nil || d.outlineSource != body {
			d.outline, d.outlineSource = newJSONOutline(body), body
		}
		cursor = d.outline.pathAt(body, a.editorDocument(stateKey).state.Caret)
	}
	applied := strings.TrimSpace(d.ResponseFilter)
	appliedPath, plain := []pathSegment(nil), false
	if applied != "" && language == "json" {
		appliedPath, plain = parseJSONPath(applied)
	}
	if applied == "" && len(cursor) == 0 {
		return
	}
	path := cursor
	if plain {
		// Filter output is an array of matches, so the caret's leading index is the match itself.
		path = slices.Clone(appliedPath)
		if len(cursor) > 1 {
			path = append(path, cursor[1:]...)
		}
	}
	filtered := applied != ""
	failed := filtered && d.FilterError != ""
	border, background, tint := p.border, p.response, p.accent
	switch {
	case failed:
		border, background, tint = p.red.Alpha(.4), p.red.Alpha(.1), p.red
	case filtered:
		border, background = p.accent.Alpha(.4), p.accent.Alpha(.1)
	}
	selectPath := func(count int) {
		d.ResponseFilter = ""
		if count > 0 {
			d.ResponseFilter = jsonPathString(path, count)
		}
		d.FilterSource = ""
	}
	crumb := func(label, tooltip string, isApplied, clickable bool, count int) ui.Element {
		color := p.muted
		if isApplied {
			color = tint
		}
		if !clickable {
			return ui.Text(c, label).Font("monospace").FontSize(12).TextColor(color).Padding(0, 5).Shrink(0)
		}
		button := ui.ButtonBase(c).Label(tooltip).Tooltip(tooltip).Height(20).Padding(0, 5).Radius(3).Shrink(0)
		if button.Hovered() {
			button.Background(p.border.Alpha(.5))
			color = p.text
		}
		button.Children(func() { ui.Text(c, label).Font("monospace").FontSize(12).TextColor(color) })
		if button.Clicked() {
			selectPath(count)
		}
		return button
	}
	ui.Row(c).Absolute().Top(4).Right(16).MaxWidthPercent(70).Height(24).Padding(0, 2).Radius(4).Border(1, border).Background(p.response).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, .1)).Children(func() {
		ui.Row(c).Fill().MinWidth(0).Radius(3).Background(background).Children(func() {
			if filtered && !plain {
				ui.Text(c, applied).Font("monospace").FontSize(12).TextColor(tint).SingleLine().MinWidth(0).Padding(0, 6)
				return
			}
			crumb("$", "Clear filter", filtered, len(appliedPath) > 0, 0)
			key := jsonPathString(path, len(path))
			ui.ScrollHorizontal(c).MinWidth(0).Shrink(1).Children(func() {
				ui.Row(c).Children(func() {
					for i, seg := range path {
						count := i + 1
						label, tooltip := seg.key, "Filter to "+seg.key
						if seg.isIndex {
							label, tooltip = "["+strconv.Itoa(seg.index)+"]", "Filter to element "+strconv.Itoa(seg.index)
						}
						ui.Row(c).Key(i).Shrink(0).Children(func() {
							icon(c, "chevron").FontSize(10).TextColor(p.subtle)
							last := crumb(label, tooltip, plain && count <= len(appliedPath), count != len(appliedPath) || !plain, count)
							if count == len(path) && d.crumbKey != key {
								d.crumbKey = key
								last.ScrollIntoView()
							}
						})
					}
				})
			})
		})
	})
}

// responseBodyActions are the buttons Yaak floats over the bottom-right of a
// response body: save, copy and filter, which opens a filter field in their place.
func (a *App) responseBodyActions(c *ui.Context, p colors, d *Draft, response engine.Object, filterable, hovered bool) {
	if !hovered && !d.FilterOpen && d.ResponseFilter == "" {
		return
	}
	bar := ui.Row(c).Absolute().Right(12).Bottom(12).Gap(6)
	if d.FilterOpen {
		bar.Left(12)
	}
	bar.Children(func() {
		action := func(glyph, label string) ui.Element {
			button := sizedIconButton(c, glyph, label, 28, 15).Border(1, p.border).Background(p.response).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, .12))
			if button.Hovered() {
				button.Background(p.border.Alpha(.4))
			}
			return button
		}
		if !d.FilterOpen {
			if action("save", "Save response to file").Clicked() {
				a.saveResponse(response)
			}
			if action("copy", "Copy response body").Clicked() {
				c.WriteClipboard(a.bodies[s(response, "id")])
			}
			if filterable {
				filter := action("filter", "Filter response")
				if d.ResponseFilter != "" {
					filter.Border(1, p.accent)
				}
				if filter.Clicked() {
					d.FilterOpen = true
				}
			}
			return
		}
		field := ui.Row(c).Grow(1).Height(30).Radius(5).Border(1, p.border).Background(p.response).Padding(0, 2, 0, 2).Gap(2).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, .12))
		if d.FilterError != "" {
			field.Border(1, p.red)
		}
		field.Children(func() {
			if names := a.Engine.PluginFilterNames(); len(names) > 0 {
				names = append([]string{"JSONPath / XPath"}, names...)
				provider := cmp.Or(d.FilterProvider, names[0])
				if ui.Select(c, &provider, names).Label("Filter provider").Width(140).Height(26).Background(ui.Transparent).Border(0, ui.Transparent).Changed() {
					d.FilterProvider = provider
					if provider == names[0] {
						d.FilterProvider = ""
					}
					d.FilterSource = ""
				}
			}
			placeholder := "JSONPath expression"
			if kind := responseMIME(response); strings.Contains(kind, "xml") || strings.Contains(kind, "html") {
				placeholder = "XPath expression"
			}
			input := ui.TextInput(c, &d.ResponseFilter).Placeholder(placeholder).Label("Filter expression").AutoFocus().Grow(1).MinWidth(0).Height(26).Background(ui.Transparent).Border(0, ui.Transparent).Font("monospace").FontSize(12)
			if input.Changed() || input.Submitted() {
				d.FilterSource = ""
			}
			if input.Shortcut(0, ui.KeyEscape) {
				d.FilterOpen = false
			}
			if smallIconButton(c, "close", "Close filter").Clicked() {
				d.FilterOpen = false
			}
		})
	})
	if d.FilterOpen && d.FilterError != "" {
		ui.Text(c, d.FilterError).Absolute().Left(12).Right(12).Bottom(48).FontSize(11).TextColor(p.red).MaxLines(2)
	}
}

// responseModes are Yaak's Response tab views: formatted, and the raw text.
var responseModes = []struct{ mode, label, short string }{
	{"Pretty", "Response", "Response"},
	{"Raw", "Response (Raw)", "Raw"},
}

func responseTabLabel(mode string) string {
	for _, m := range responseModes {
		if m.mode == mode {
			return m.short
		}
	}
	return "Response"
}
func (a *App) responseViewMenu(c *ui.Context, m *ui.Menu, d *Draft, response engine.Object) {
	for _, option := range responseModes {
		// An image has no raw text to show.
		if option.mode == "Raw" && strings.HasPrefix(responseMIME(response), "image/") {
			continue
		}
		if m.Item(option.label).Checked(d.ResponseMode == option.mode).Chosen() {
			d.ResponseMode = option.mode
		}
	}
	m.Separator()
	if m.Item("Save to File").Chosen() {
		a.saveResponse(response)
	}
	if m.Item("Copy Body").Chosen() {
		c.WriteClipboard(a.bodies[s(response, "id")])
	}
}

// loadResponseBody loads the preview of a response's body once.
func (a *App) loadResponseBody(response engine.Object) {
	id := s(response, "id")
	if _, loaded := a.bodies[id]; loaded {
		return
	}
	a.run(func() (func(), error) {
		body, _, err := a.Engine.BodyPreview(id, 2<<20)
		return func() { a.bodies[id] = string(body) }, err
	})
}

// hotkeyList is the shortcut table Yaak shows in empty panes, centred in
// the room left, with bottom (when set) below it.
func (a *App) hotkeyList(c *ui.Context, p colors, actions []string, bottom ...func()) {
	ui.Column(c).Grow(1).MinHeight(0).Center().Gap(14).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			for _, h := range hotkeyTable(actions...) {
				ui.Row(c).Key(h.action).Gap(32).Children(func() {
					ui.Text(c, h.label).FontSize(13).TextColor(p.muted).Grow(1)
					ui.Text(c, a.hotkeyText(h.action)).FontSize(12).Font("monospace").TextColor(p.subtle)
				})
			}
		})
		for _, slot := range bottom {
			slot()
		}
	})
}
func fieldLabel(field string) string {
	switch field {
	case "clientId":
		return "Client ID"
	case "clientSecret":
		return "Client secret"
	case "accessToken":
		return "Access token"
	case "tokenUrl":
		return "Token URL"
	case "in":
		return "Location (header or query)"
	}
	if field == "" {
		return ""
	}
	return strings.ToUpper(field[:1]) + field[1:]
}
