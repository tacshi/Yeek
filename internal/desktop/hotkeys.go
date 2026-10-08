package desktop

import (
	"cmp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// keyCombo is one key with its modifiers; ui.Cmd is Command on macOS and
// Control elsewhere.
type keyCombo struct {
	mods ui.Modifiers
	key  ui.Key
	name string // how the key is shown, as "K" or "↩"
}

// hotkey mirrors an entry of Yaak's hotkey table: the action, its label in
// Yaak's words, and its keys (the first is the one shown).
type hotkey struct {
	action, label string
	keys          []keyCombo
}

var hotkeys = []hotkey{
	{"app.zoom_in", "Zoom In", []keyCombo{{ui.Cmd, ui.KeyEqual, "="}}},
	{"app.zoom_out", "Zoom Out", []keyCombo{{ui.Cmd, ui.KeyMinus, "-"}}},
	{"app.zoom_reset", "Zoom to Actual Size", []keyCombo{{ui.Cmd, ui.Key0, "0"}}},
	{"command_palette.toggle", "Toggle Command Palette", []keyCombo{{ui.Cmd, ui.KeyK, "K"}}},
	{"cookies_editor.show", "Show Cookies", []keyCombo{{ui.Cmd | ui.Shift, ui.KeyK, "K"}}},
	{"editor.autocomplete", "Trigger Autocomplete", []keyCombo{{ui.Ctrl, ui.KeySpace, "Space"}}},
	{"environment_editor.toggle", "Edit Environments", []keyCombo{{ui.Cmd | ui.Shift, ui.KeyE, "E"}}},
	{"request.rename", "Rename Active Request", []keyCombo{{ui.Ctrl | ui.Shift, ui.KeyR, "R"}}},
	{"request.send", "Send Active Request", []keyCombo{{ui.Cmd, ui.KeyEnter, "↩"}, {ui.Cmd, ui.KeyR, "R"}}},
	{"hotkeys.showHelp", "Show Keyboard Shortcuts", []keyCombo{{ui.Cmd | ui.Shift, ui.KeySlash, "/"}}},
	{"model.create", "New Request", []keyCombo{{ui.Cmd, ui.KeyN, "N"}}},
	{"model.duplicate", "Duplicate Request", []keyCombo{{ui.Cmd, ui.KeyD, "D"}}},
	{"switcher.next", "Go To Previous Request", []keyCombo{{ui.Ctrl | ui.Shift, ui.KeyTab, "Tab"}}},
	{"switcher.prev", "Go To Next Request", []keyCombo{{ui.Ctrl, ui.KeyTab, "Tab"}}},
	{"switcher.toggle", "Toggle Request Switcher", []keyCombo{{ui.Cmd, ui.KeyP, "P"}}},
	{"settings.show", "Open Settings", []keyCombo{{ui.Cmd, ui.KeyComma, ","}}},
	{"sidebar.filter", "Filter Sidebar", []keyCombo{{ui.Cmd, ui.KeyF, "F"}}},
	{"sidebar.expand_all", "Expand All Folders", []keyCombo{{ui.Cmd | ui.Shift, ui.KeyEqual, "="}}},
	{"sidebar.collapse_all", "Collapse All Folders", []keyCombo{{ui.Cmd | ui.Shift, ui.KeyMinus, "-"}}},
	{"sidebar.selected.delete", "Delete Selected Sidebar Item", []keyCombo{{0, ui.KeyDelete, "Delete"}, {ui.Cmd, ui.KeyBackspace, "⌫"}}},
	{"sidebar.selected.duplicate", "Duplicate Selected Sidebar Item", []keyCombo{{ui.Cmd, ui.KeyD, "D"}}},
	{"sidebar.selected.move", "Move Selected to Workspace", nil},
	{"sidebar.selected.rename", "Rename Selected Sidebar Item", []keyCombo{{0, ui.KeyEnter, "↩"}}},
	{"sidebar.focus", "Focus or Toggle Sidebar", []keyCombo{{ui.Cmd, ui.KeyB, "B"}}},
	{"url_bar.focus", "Focus URL", []keyCombo{{ui.Cmd, ui.KeyL, "L"}}},
	{"workspace_settings.show", "Open Workspace Settings", []keyCombo{{ui.Cmd, ui.KeySemicolon, ";"}}},
}

func findHotkey(action string) (hotkey, bool) {
	for _, h := range hotkeys {
		if h.action == action {
			return h, true
		}
	}
	return hotkey{}, false
}

// hotkeyKeys are the action's keys: the user's from Settings → Shortcuts
// when set (an empty list turns the action off), else the defaults.
func (a *App) hotkeyKeys(action string) []keyCombo {
	if custom, ok := o(a.settings, "hotkeys")[action].([]any); ok {
		keys := []keyCombo{}
		for _, v := range custom {
			text, _ := v.(string)
			if k, ok := parseHotkey(text); ok {
				keys = append(keys, k)
			}
		}
		return keys
	}
	h, _ := findHotkey(action)
	return h.keys
}

// hotkeyText shows the action's first key as the platform writes it: the
// symbols ⌃ ⌥ ⇧ ⌘ on macOS, words joined with + elsewhere.
func (a *App) hotkeyText(action string) string {
	keys := a.hotkeyKeys(action)
	if len(keys) == 0 {
		return ""
	}
	return comboText(keys[0])
}

func comboText(k keyCombo) string {
	mac := runtime.GOOS == "darwin"
	parts := []string{}
	add := func(mod ui.Modifiers, symbol, word string) {
		if k.mods&mod != 0 {
			parts = append(parts, map[bool]string{true: symbol, false: word}[mac])
		}
	}
	add(ui.Ctrl, "⌃", "Ctrl")
	add(ui.Alt, "⌥", "Alt")
	add(ui.Shift, "⇧", "Shift")
	if mac && k.mods&ui.Cmd != 0 && ui.Cmd != ui.Ctrl {
		parts = append(parts, "⌘")
	}
	parts = append(parts, k.name)
	if mac {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts, "+")
}

// pressed reports whether one of the action's keys was pressed this frame,
// unless a focused element took it first.
func (a *App) pressed(c *ui.Context, action string) bool {
	hit := false
	for _, k := range a.hotkeyKeys(action) {
		if c.Shortcut(k.mods, k.key) {
			hit = true
		}
	}
	return hit
}

// hotkeyTable lists actions with their keys, as Yaak's HotkeyList does.
func hotkeyTable(actions ...string) []hotkey {
	result := make([]hotkey, 0, len(actions))
	for _, action := range actions {
		if h, ok := findHotkey(action); ok {
			result = append(result, h)
		}
	}
	return result
}

// handleHotkeys runs the window's shortcuts while no dialog is open.
func (a *App) handleHotkeys(c *ui.Context) {
	if len(a.toasts) > 0 && c.Shortcut(0, ui.KeyEscape) {
		a.toasts = nil
	}
	if a.pressed(c, "command_palette.toggle") {
		a.togglePalette()
	}
	// Yaak's useZoom.
	scale := cmp.Or(n(a.settings, "interfaceScale"), 1)
	if a.pressed(c, "app.zoom_in") {
		a.saveSetting("interfaceScale", min(1.8, scale*1.1))
	}
	if a.pressed(c, "app.zoom_out") {
		a.saveSetting("interfaceScale", max(0.4, scale*0.9))
	}
	if a.pressed(c, "app.zoom_reset") {
		a.saveSetting("interfaceScale", 1.0)
	}
	if a.pressed(c, "settings.show") {
		a.prompt("settings", "Settings", "", "")
	}
	if a.pressed(c, "hotkeys.showHelp") {
		a.prompt("shortcuts", "Keyboard Shortcuts", "", "")
	}
	if a.workspace == "" {
		return
	}
	// The focused sidebar sends and duplicates its selected rows instead.
	tree := a.tree.focused && a.tree.renaming == ""
	if a.pressed(c, "request.send") {
		if requests := a.selectedTreeModels(); tree && len(requests) > 0 {
			a.sendRequests(treeIDs(slices.DeleteFunc(requests, func(m engine.Object) bool { return s(m, "model") != "http_request" })))
		} else {
			a.send()
		}
	}
	if a.pressed(c, "model.create") {
		a.addRequest("http_request", "")
	}
	if tree && a.pressed(c, "sidebar.selected.duplicate") {
		for _, m := range a.selectedTreeModels() {
			a.duplicate(s(m, "id"))
		}
	}
	if a.pressed(c, "model.duplicate") && a.active != "" {
		a.duplicate(a.active)
	}
	if c.Shortcut(ui.Cmd, ui.KeyS) {
		a.saveActive()
	}
	if a.pressed(c, "switcher.toggle") {
		a.toggleSwitcher(0)
	}
	if a.pressed(c, "switcher.prev") {
		a.toggleSwitcher(1)
	}
	if a.pressed(c, "switcher.next") {
		a.toggleSwitcher(-1)
	}
	if a.pressed(c, "cookies_editor.show") {
		a.openCookies(s(a.activeCookieJar(), "id"))
	}
	if a.pressed(c, "environment_editor.toggle") {
		a.openEnvironments()
	}
	if a.pressed(c, "workspace_settings.show") {
		a.prompt("workspace_settings", "Workspace Settings", "", a.workspace)
	}
	if a.pressed(c, "request.rename") && a.models[a.active] != nil {
		a.prompt("rename", "Rename Request", s(a.models[a.active], "name"), a.active)
	}
	if a.pressed(c, "url_bar.focus") && a.active != "" {
		a.focusURL = true
	}
	if a.pressed(c, "sidebar.focus") {
		switch {
		case !a.hideSidebar && a.sidebarFocused:
			a.hideSidebar = true
		default:
			a.hideSidebar = false
			a.focusSidebar = true
			a.revealInSidebar(a.active)
		}
	}
	if a.sidebarFocused && a.pressed(c, "sidebar.filter") {
		a.focusFilter = true
	}
	if a.pressed(c, "sidebar.expand_all") {
		for _, f := range a.list("folder") {
			a.expanded[s(f, "id")] = true
		}
	}
	if a.pressed(c, "sidebar.collapse_all") {
		clear(a.expanded)
	}
}

// keyNames are the key names of Yaak's hotkey strings, such as "Meta+Shift+k".
var keyNames = func() map[string]ui.Key {
	names := map[string]ui.Key{"Enter": ui.KeyEnter, "Escape": ui.KeyEscape, "Backspace": ui.KeyBackspace, "Tab": ui.KeyTab, "Space": ui.KeySpace, "Delete": ui.KeyDelete,
		"ArrowUp": ui.KeyUp, "ArrowDown": ui.KeyDown, "ArrowLeft": ui.KeyLeft, "ArrowRight": ui.KeyRight, "Home": ui.KeyHome, "End": ui.KeyEnd, "PageUp": ui.KeyPageUp, "PageDown": ui.KeyPageDown,
		"Equal": ui.KeyEqual, "Minus": ui.KeyMinus, "BracketLeft": ui.KeyBracketLeft, "BracketRight": ui.KeyBracketRight, "Backquote": ui.KeyBackquote,
		",": ui.KeyComma, ".": ui.KeyPeriod, "/": ui.KeySlash, ";": ui.KeySemicolon, "'": ui.KeyQuote, "\\": ui.KeyBackslash}
	for i, k := range []ui.Key{ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM, ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ} {
		names[string(rune('a'+i))] = k
	}
	for i, k := range []ui.Key{ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9} {
		names[string(rune('0'+i))] = k
	}
	for i, k := range []ui.Key{ui.KeyF1, ui.KeyF2, ui.KeyF3, ui.KeyF4, ui.KeyF5, ui.KeyF6, ui.KeyF7, ui.KeyF8, ui.KeyF9, ui.KeyF10, ui.KeyF11, ui.KeyF12} {
		names["F"+strconv.Itoa(i+1)] = k
	}
	return names
}()

// displayNames show a key as Yaak's Hotkey component does.
var displayNames = map[string]string{"Enter": "↩", "Escape": "Esc", "Backspace": "⌫", "Delete": "⌦", "ArrowUp": "↑", "ArrowDown": "↓", "ArrowLeft": "←", "ArrowRight": "→", "Equal": "=", "Minus": "-", "BracketLeft": "[", "BracketRight": "]", "Backquote": "`"}

// parseHotkey reads a hotkey string. "Meta" is Command on macOS and Control
// elsewhere, as Yaak's defaults use it.
func parseHotkey(text string) (keyCombo, bool) {
	parts := strings.Split(text, "+")
	if len(parts) == 0 {
		return keyCombo{}, false
	}
	var mods ui.Modifiers
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "Meta":
			mods |= ui.Cmd
		case "Control":
			mods |= ui.Ctrl
		case "Alt":
			mods |= ui.Alt
		case "Shift":
			mods |= ui.Shift
		default:
			return keyCombo{}, false
		}
	}
	name := parts[len(parts)-1]
	key, ok := keyNames[name]
	if !ok {
		key, ok = keyNames[strings.ToLower(name)]
	}
	if !ok {
		return keyCombo{}, false
	}
	shown := displayNames[name]
	if shown == "" {
		shown = strings.ToUpper(name)
	}
	return keyCombo{mods, key, shown}, true
}

// formatHotkey writes a key as a hotkey string, modifiers in Yaak's order.
func formatHotkey(mods ui.Modifiers, key ui.Key) (string, bool) {
	name := ""
	for n, k := range keyNames {
		if k == key && (name == "" || len(n) < len(name) || len(n) == len(name) && n < name) {
			name = n
		}
	}
	if name == "" {
		return "", false
	}
	parts := []string{}
	mac := runtime.GOOS == "darwin"
	if mods&ui.Cmd != 0 && (mac || ui.Cmd != ui.Ctrl) {
		parts = append(parts, "Meta")
	}
	if mods&ui.Ctrl != 0 && (mac || ui.Cmd == ui.Ctrl && mods&ui.Cmd == 0) {
		parts = append(parts, "Control")
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, name), "+"), true
}
