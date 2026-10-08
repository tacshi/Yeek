package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func paletteApp(t *testing.T) (*App, *ui.Tester, []string) {
	t.Helper()
	a, e := cookieApp(t)
	folder, _ := e.Save(t.Context(), engine.Object{"model": "folder", "workspaceId": a.workspace, "name": "Owners"})
	a.applyModel(folder)
	ids := []string{}
	for _, m := range []engine.Object{
		{"model": "http_request", "workspaceId": a.workspace, "folderId": s(folder, "id"), "name": "Person search", "method": "GET"},
		{"model": "http_request", "workspaceId": a.workspace, "name": "Create title", "method": "POST"},
		{"model": "websocket_request", "workspaceId": a.workspace, "name": "Live feed"},
	} {
		saved, _ := e.Save(t.Context(), m)
		a.applyModel(saved)
		ids = append(ids, s(saved, "id"))
	}
	env, _ := e.Save(t.Context(), engine.Object{"model": "environment", "workspaceId": a.workspace, "parentModel": "environment", "name": "Staging"})
	a.applyModel(env)
	return a, ui.NewTester(a.View, 1360, 860), ids
}

func TestCommandPaletteGroupsFilterAndRun(t *testing.T) {
	a, tt, ids := paletteApp(t)
	tt.Key(ui.Cmd, ui.KeyK)
	if !a.palette.open {
		t.Fatal("⌘K did not open the palette")
	}
	for _, text := range []string{"ACTIONS", "SWITCH REQUEST", "SWITCH ENVIRONMENT", "SWITCH WORKSPACE", "Open Settings", "Create HTTP Request", "Staging"} {
		if !tt.HasText(text) {
			t.Fatalf("palette missing %q: %v", text, tt.Texts())
		}
	}
	if path := os.Getenv("YEEK_PALETTE"); path != "" {
		f, _ := os.Create(path) // #nosec G304 G703 -- the test runner explicitly chooses the snapshot destination.
		_ = png.Encode(f, tt.Image())
		_ = f.Close()
	}
	tt.Type("persrch")
	if tt.HasText("ACTIONS") || tt.HasText("Create title") && tt.HasText("SWITCH WORKSPACE") || !tt.HasText("Person search") || !tt.HasText("Owners") {
		t.Fatalf("fuzzy filter: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if a.palette.open || a.active != ids[0] {
		t.Fatalf("Enter did not open the request: active=%s", a.active)
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("staging")
	tt.Key(0, ui.KeyEnter)
	if s(a.models[a.environment], "name") != "Staging" {
		t.Fatal("environment not switched")
	}
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Key(ui.Cmd, ui.KeyK)
	if a.palette.open {
		t.Fatal("⌘K did not close the palette")
	}
}

func TestRequestSwitcherCyclesRecentRequests(t *testing.T) {
	a, tt, ids := paletteApp(t)
	for _, id := range ids {
		a.openRequest(id)
	}
	tt.Frame()
	if a.tabs[0] != ids[2] || a.tabs[1] != ids[1] {
		t.Fatalf("recent order = %v", a.tabs)
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	if !a.switcher.open || a.switcher.index != 1 {
		t.Fatalf("switcher = %+v", a.switcher)
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	if a.switcher.index != 2 {
		t.Fatalf("switcher index = %d", a.switcher.index)
	}
	tt.Key(0, ui.KeyEnter)
	if a.switcher.open || a.active != ids[0] {
		t.Fatalf("switcher opened %s", a.active)
	}
	tt.Key(ui.Cmd, ui.KeyP)
	if !a.switcher.open || a.switcher.index != 0 || !tt.HasText("Live feed") {
		t.Fatalf("⌘P switcher = %+v", a.switcher)
	}
	tt.Key(0, ui.KeyEscape)
	if a.switcher.open {
		t.Fatal("Escape did not close the switcher")
	}
}

func TestFocusHotkeys(t *testing.T) {
	a, tt, ids := paletteApp(t)
	a.openRequest(ids[0])
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyL)
	tt.Frame()
	if !tt.Focused("Request URL") {
		t.Fatal("⌘L did not focus the URL")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !a.sidebarFocusedNow(tt) {
		t.Fatal("⌘B did not focus the sidebar")
	}
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !tt.Focused("Filter requests") {
		t.Fatal("⌘F did not focus the sidebar filter")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !a.hideSidebar {
		t.Fatal("⌘B did not hide the focused sidebar")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyE)
	if a.dialog != "environments" || !a.dialogOpen {
		t.Fatal("⌘⇧E did not open environments")
	}
}

func (a *App) sidebarFocusedNow(tt *ui.Tester) bool {
	tt.Frame()
	return a.sidebarFocused
}
