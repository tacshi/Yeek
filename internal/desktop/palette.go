package desktop

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// paletteState is Yaak's command palette (⌘K): actions, then requests,
// environments and workspaces to switch to, filtered as you type.
type paletteState struct {
	open     bool
	query    string
	selected string // key of the highlighted item
}

type paletteItem struct {
	key, label, search, action string
	request                    engine.Object // a request row shows its method and folders
	run                        func()
}

type paletteGroup struct {
	label string
	items []paletteItem
}

// paletteMaxPerGroup matches Yaak's MAX_PER_GROUP.
const paletteMaxPerGroup = 8

func (a *App) togglePalette() {
	if a.palette.open {
		a.palette.open = false
		return
	}
	a.palette = paletteState{open: true}
}

func (a *App) paletteGroups() []paletteGroup {
	groups := []paletteGroup{}
	if a.workspace == "" {
		return groups
	}
	wid := a.workspace
	actions := []paletteItem{
		{key: "settings.open", label: "Open Settings", action: "settings.show", run: func() { a.prompt("settings", "Settings", "", "") }},
		{key: "workspace_settings.open", label: "Open Workspace Settings", action: "workspace_settings.show", run: func() { a.prompt("workspace_settings", "Workspace Settings", "", wid) }},
		{key: "app.create", label: "Create Workspace", run: func() { a.prompt("workspace", "New Workspace", "", "") }},
		{key: "model.create", label: "Create HTTP Request", run: func() { a.addRequest("http_request", "") }},
		{key: "grpc_request.create", label: "Create GRPC Request", run: func() { a.addRequest("grpc_request", "") }},
		{key: "websocket_request.create", label: "Create Websocket Request", run: func() { a.addRequest("websocket_request", "") }},
		{key: "folder.create", label: "Create Folder", run: func() { a.prompt("folder", "New Folder", "", "") }},
		{key: "cookies.show", label: "Show Cookies", action: "cookies_editor.show", run: func() { a.openCookies(s(a.activeCookieJar(), "id")) }},
		{key: "environment.edit", label: "Edit Environment", action: "environment_editor.toggle", run: a.openEnvironments},
		{key: "sidebar.toggle", label: "Toggle Sidebar", action: "sidebar.focus", run: func() { a.hideSidebar = !a.hideSidebar }},
	}
	if active := a.models[a.active]; active != nil {
		id := a.active
		if s(active, "model") == "http_request" {
			actions = append(actions, paletteItem{key: "request.send", label: "Send Request", action: "request.send", run: a.send})
		}
		for i, action := range a.Engine.PluginActions() {
			name := action.Name
			actions = append(actions, paletteItem{key: "request_action." + name + string(rune('a'+i)), label: action.Label, run: func() {
				a.run(func() (func(), error) { return nil, a.Engine.RunPluginAction(a.ctx, name, wid, id) })
			}})
		}
		actions = append(actions,
			paletteItem{key: "http_request.rename", label: "Rename Request", run: func() { a.prompt("rename", "Rename Request", s(active, "name"), id) }},
			paletteItem{key: "sidebar.selected.delete", label: "Delete Request", run: func() { a.prompt("delete", "Delete "+requestName(active), requestName(active), id) }},
		)
	}
	slices.SortStableFunc(actions, func(x, y paletteItem) int { return strings.Compare(x.label, y.label) })
	groups = append(groups, paletteGroup{"Actions", actions})

	requests := paletteGroup{label: "Switch Request"}
	for _, m := range a.recentFirst(a.allRequests()) {
		id := s(m, "id")
		requests.items = append(requests.items, paletteItem{key: "switch-request-" + id, label: requestName(m), search: strings.Join(a.requestPath(m), " "), request: m, run: func() { a.openRequest(id); a.revealInSidebar(id) }})
	}
	groups = append(groups, requests)

	environments := paletteGroup{label: "Switch Environment"}
	for _, env := range a.list("environment") {
		if parent := s(env, "parentModel"); parent == "workspace" || parent == "folder" || s(env, "id") == a.environment {
			continue
		}
		id := s(env, "id")
		environments.items = append(environments.items, paletteItem{key: "switch-environment-" + id, label: s(env, "name"), run: func() { a.environment = id }})
	}
	groups = append(groups, environments)

	workspaces := paletteGroup{label: "Switch Workspace"}
	for _, w := range a.list("workspace") {
		id := s(w, "id")
		workspaces.items = append(workspaces.items, paletteItem{key: "switch-workspace-" + id, label: s(w, "name"), run: func() { a.switchWorkspace(id) }})
	}
	return append(groups, workspaces)
}

// allRequests is every request of the workspace.
func (a *App) allRequests() []engine.Object {
	result := []engine.Object{}
	for _, kind := range []string{"http_request", "grpc_request", "websocket_request"} {
		result = append(result, a.list(kind)...)
	}
	return result
}

// recentFirst orders requests as Yaak does: recently opened first, the rest by creation.
func (a *App) recentFirst(requests []engine.Object) []engine.Object {
	slices.SortStableFunc(requests, func(x, y engine.Object) int {
		xi, yi := slices.Index(a.tabs, s(x, "id")), slices.Index(a.tabs, s(y, "id"))
		switch {
		case xi >= 0 && yi >= 0:
			return cmp.Compare(xi, yi)
		case xi >= 0:
			return -1
		case yi >= 0:
			return 1
		}
		return strings.Compare(s(x, "createdAt"), s(y, "createdAt"))
	})
	return requests
}

// requestPath is the names of a request's folders, outermost first, then its own.
func (a *App) requestPath(m engine.Object) []string {
	path := []string{requestName(m)}
	for id, depth := s(m, "folderId"), 0; id != "" && depth < 32; id, depth = s(a.models[id], "folderId"), depth+1 {
		path = append([]string{s(a.models[id], "name")}, path...)
	}
	return path
}

// fuzzyScore matches query against text as fuzzbunny does in spirit: a
// substring scores highest, earlier and at a word start better; otherwise the
// query's characters in order, closer together better. ok is false for no match.
func fuzzyScore(text, query string) (int, bool) {
	text, query = strings.ToLower(text), strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0, true
	}
	if i := strings.Index(text, query); i >= 0 {
		score := 10000 - i
		if i == 0 || !unicode.IsLetter(rune(text[i-1])) && !unicode.IsDigit(rune(text[i-1])) {
			score += 500
		}
		return score, true
	}
	runes, pos, gaps, last := []rune(text), 0, 0, -1
	for _, q := range query {
		found := false
		for ; pos < len(runes); pos++ {
			if runes[pos] == q {
				if last >= 0 {
					gaps += pos - last - 1
				}
				last, found = pos, true
				pos++
				break
			}
		}
		if !found {
			return 0, false
		}
	}
	return 5000 - gaps, true
}

// filteredPalette applies the query, best matches first, at most eight per group.
func (a *App) filteredPalette() ([]paletteGroup, []paletteItem) {
	groups := a.paletteGroups()
	query := strings.TrimSpace(a.palette.query)
	all := []paletteItem{}
	for gi := range groups {
		type scored struct {
			item  paletteItem
			score int
		}
		matches := []scored{}
		for _, item := range groups[gi].items {
			if score, ok := fuzzyScore(cmp.Or(item.search, item.label), query); ok {
				matches = append(matches, scored{item, score})
			}
		}
		if query != "" {
			slices.SortStableFunc(matches, func(x, y scored) int { return cmp.Compare(y.score, x.score) })
		}
		groups[gi].items = groups[gi].items[:0]
		for _, m := range matches[:min(len(matches), paletteMaxPerGroup)] {
			groups[gi].items = append(groups[gi].items, m.item)
		}
		all = append(all, groups[gi].items...)
	}
	groups = slices.DeleteFunc(groups, func(g paletteGroup) bool { return len(g.items) == 0 })
	return groups, all
}

// paletteView is the command palette: a search field over grouped results,
// near the top of the window, without a title.
func (a *App) paletteView(c *ui.Context, p colors) {
	if !a.palette.open {
		return
	}
	groups, items := a.filteredPalette()
	index := slices.IndexFunc(items, func(i paletteItem) bool { return i.key == a.palette.selected })
	if index < 0 && len(items) > 0 {
		index = 0
	}
	choose := func(item paletteItem) {
		a.palette.open = false
		item.run()
	}
	ui.DialogBase(c, &a.palette.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .3)).Justify(ui.Start).Padding(64, 0, 0, 0)
		panel.Key("command-palette").Width(700).MaxWidthPercent(80).MaxHeight(480).Padding(8, 0).Radius(10).Background(p.background).Border(1, p.border).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, .25)).Gap(0)
		ui.Row(c).Padding(0, 8).Children(func() {
			field := ui.Row(c).Grow(1).Height(36).Radius(6).Border(1, p.border).Gap(4).Padding(0, 8, 0, 4)
			field.Children(func() {
				ui.Box(c).Width(32).Justify(ui.Center).Children(func() { icon(c, "search").TextColor(p.muted) })
				input := ui.TextInput(c, &a.palette.query).Label("Command").Placeholder("Search or type a command").AutoFocus().Grow(1).Background(ui.Transparent).Border(0, ui.Transparent).FontSize(14)
				move := func(delta int) {
					if len(items) > 0 {
						a.palette.selected = items[((index+delta)%len(items)+len(items))%len(items)].key
					}
				}
				if input.Shortcut(0, ui.KeyDown) || input.Shortcut(ui.Ctrl, ui.KeyN) {
					move(1)
				}
				if input.Shortcut(0, ui.KeyUp) || input.Shortcut(ui.Ctrl, ui.KeyK) {
					move(-1)
				}
				if input.Shortcut(ui.Cmd, ui.KeyK) {
					a.palette.open = false
				}
				if input.Submitted() && index >= 0 {
					choose(items[index])
				}
			})
		})
		ui.Scroll(c).Shrink(1).MinHeight(0).Padding(8, 18, 4, 6).Children(func() {
			for _, group := range groups {
				ui.Column(c).Key(group.label).Padding(0, 0, 6, 0).Children(func() {
					ui.Text(c, strings.ToUpper(group.label)).FontSize(11).FontWeight(600).TextColor(p.muted).Height(26).Padding(6, 6, 0, 6)
					for _, item := range group.items {
						active := index >= 0 && items[index].key == item.key
						row := ui.ButtonBase(c).Key(item.key).Label(item.label).FillWidth().Height(30).Padding(0, 6).Gap(8).Radius(4).Justify(ui.Start)
						if active {
							row.Background(p.border.Alpha(.55)).ScrollIntoView()
						}
						row.Children(func() {
							color := p.muted
							if active || row.Hovered() {
								color = p.text
							}
							if item.request != nil {
								method := requestMethod(item.request)
								ui.Text(c, shortMethod(method)).Font("monospace").FontSize(11).TextColor(a.methodColor(method, p)).Shrink(0).Padding(0, 6, 0, 0)
								for i, part := range a.requestPath(item.request) {
									if i > 0 {
										icon(c, "chevron").FontSize(11).TextColor(p.subtle)
									}
									ui.Text(c, part).FontSize(13).TextColor(color).SingleLine().MinWidth(0)
								}
							} else {
								ui.Text(c, item.label).FontSize(13).TextColor(color).SingleLine().Grow(1).MinWidth(0)
							}
							ui.Spacer(c)
							if item.action != "" {
								ui.Text(c, a.hotkeyText(item.action)).Font("monospace").FontSize(11).TextColor(p.subtle).Shrink(0)
							}
						})
						if row.Clicked() {
							choose(item)
						}
					}
				})
			}
			if len(items) == 0 {
				ui.Text(c, "No matches").FontSize(13).TextColor(p.muted).Padding(10)
			}
		})
	})
}

// switcherState is Yaak's recent-request switcher (⌘P, Ctrl+Tab): the header's
// request menu, open with one entry highlighted. Repeating Ctrl+Tab moves the
// highlight; Enter or a click opens it. Yaak also opens it when Control is
// released, which needs key events for modifiers that MyGo does not deliver.
type switcherState struct {
	open  bool
	index int
}

// toggleSwitcher opens the switcher, or moves its highlight by step while open.
// A step of 1 from closed highlights the second entry, the request before this one.
func (a *App) toggleSwitcher(step int) {
	recent := a.recentRequestIDs()
	if !a.switcher.open {
		a.switcher = switcherState{open: true}
		if step > 0 && len(recent) > 1 {
			a.switcher.index = 1
		}
		if step < 0 && len(recent) > 0 {
			a.switcher.index = len(recent) - 1
		}
		return
	}
	if step == 0 {
		a.switcher.open = false
		return
	}
	if len(recent) > 0 {
		a.switcher.index = ((a.switcher.index+step)%len(recent) + len(recent)) % len(recent)
	}
}

// recentRequestIDs is up to twenty recently opened requests, newest first.
func (a *App) recentRequestIDs() []string {
	ids := []string{}
	for _, id := range a.tabs {
		if a.models[id] != nil {
			ids = append(ids, id)
		}
	}
	return ids[:min(len(ids), 20)]
}

func (a *App) switcherView(c *ui.Context, p colors) {
	if !a.switcher.open {
		return
	}
	recent := a.recentRequestIDs()
	ui.DialogBase(c, &a.switcher.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.Transparent).Justify(ui.Start).Padding(headerHeight+4, 0, 0, 0)
		panel.Key("request-switcher").Width(320).MaxHeight(520).Padding(4).Radius(8).Background(p.background).Border(1, p.border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, .2)).Gap(1)
		open := func(id string) {
			a.switcher.open = false
			a.openRequest(id)
		}
		if panel.Shortcut(0, ui.KeyDown) || panel.Shortcut(ui.Ctrl, ui.KeyTab) {
			a.toggleSwitcher(1)
		}
		if panel.Shortcut(0, ui.KeyUp) || panel.Shortcut(ui.Ctrl|ui.Shift, ui.KeyTab) {
			a.toggleSwitcher(-1)
		}
		if panel.Shortcut(ui.Cmd, ui.KeyP) {
			a.switcher.open = false
		}
		if (panel.Shortcut(0, ui.KeyEnter) || panel.Shortcut(ui.Ctrl, ui.KeyEnter)) && a.switcher.index < len(recent) {
			open(recent[a.switcher.index])
		}
		if len(recent) == 0 {
			ui.Text(c, "No recent requests").FontSize(13).TextColor(p.subtle).Italic().Padding(6, 8)
			return
		}
		for i, id := range recent {
			m := a.models[id]
			row := ui.ButtonBase(c).Key(id).Label(requestName(m)).FillWidth().Height(28).Padding(0, 8).Gap(8).Radius(5).Justify(ui.Start)
			if i == a.switcher.index {
				row.Background(p.border.Alpha(.6))
			} else if row.Hovered() {
				row.Background(p.border.Alpha(.3))
			}
			row.Children(func() {
				method := requestMethod(m)
				ui.Text(c, shortMethod(method)).Font("monospace").FontSize(11).TextColor(a.methodColor(method, p)).Shrink(0)
				ui.Text(c, requestName(m)).FontSize(13).SingleLine().MinWidth(0)
			})
			if row.Clicked() {
				open(id)
			}
		}
	})
}
