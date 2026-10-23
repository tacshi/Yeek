package desktop

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestWorkspaceCreationAndRequestEditing(t *testing.T) {
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	a.testMode = true
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Create Workspace"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Name", "Local directory sync", "Select Directory", "No directory selected", "Workspace encryption", "Enable Encryption"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	tt.Type("Local API")
	tt.Key(0, ui.KeyEnter)
	if a.workspace == "" {
		t.Fatalf("workspace not created: %v", tt.Texts())
	}
	if err := tt.Click("New HTTP Request"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Type("https://example.com/api")
	a.saveActive()
	request, err := e.Store.Get(t.Context(), a.active)
	if err != nil {
		t.Fatal(err)
	}
	if s(request, "url") != "https://example.com/api" {
		t.Fatalf("URL did not persist: %v", request)
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "Purchase") || strings.Contains(text, "License") || strings.Contains(text, "subscription") {
			t.Fatalf("commercial UI: %s", text)
		}
	}
}
func TestNativeWorkspaceSnapshot(t *testing.T) {
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	w, err := e.Save(t.Context(), engine.Object{"model": "workspace", "name": "My Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	wid := s(w, "id")
	folder, err := e.Save(t.Context(), engine.Object{"model": "folder", "workspaceId": wid, "name": "Users"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": wid, "folderId": s(folder, "id"), "name": "Get users", "method": "GET", "url": "https://api.example.com/users", "urlParameters": []any{engine.Object{"name": "limit", "value": "10", "enabled": true}, engine.Object{"name": "page", "value": "1", "enabled": true}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	a.testMode = true
	a.settings["appearance"] = "dark"
	a.expanded[s(folder, "id")] = true
	a.openRequest(s(request, "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("Params") || !tt.HasText("Headers") {
		t.Fatal(tt.Texts())
	}
	if path := os.Getenv("YEEK_SNAPSHOT"); path != "" {
		f, err := os.Create(path) // #nosec G304 G703 -- the test runner explicitly chooses the snapshot destination.
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if err := png.Encode(f, settledImage(tt)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateWorkspaceWithSyncGitAndEncryption(t *testing.T) {
	a, e := cookieApp(t)
	dir := t.TempDir()
	tt := ui.NewTester(a.View, 1360, 860)
	a.newWorkspace()
	tt.Frame()
	a.setNewWorkspaceDir(a.createWorkspace, dir)
	tt.Frame()
	if !tt.HasText("Change Directory") || !tt.HasText("Initialize Git Repo") {
		t.Fatalf("%v", tt.Texts())
	}
	if err := tt.Click("Initialize Git Repo"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Enable Encryption"); err != nil {
		t.Fatal(err)
	}
	a.createWorkspace.name = "Synced"
	tt.Frame()
	a.submitNewWorkspace(a.createWorkspace)
	tt.Frame()
	if s(a.models[a.workspace], "name") != "Synced" || a.dialog != "workspace_encryption" || !a.dialogOpen {
		t.Fatalf("workspace %v dialog %s open %v enc %v texts %v", s(a.models[a.workspace], "name"), a.dialog, a.dialogOpen, a.createWorkspace, tt.Texts())
	}
	metas, _ := e.Store.List(t.Context(), "workspace_meta", a.workspace)
	real, _ := filepath.EvalSymlinks(dir)
	if len(metas) == 0 || (s(metas[0], "settingSyncDir") != dir && s(metas[0], "settingSyncDir") != real) {
		t.Fatalf("sync dir %v", metas)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal("git not initialized", err)
	}

	finder := t.TempDir()
	for _, name := range []string{".DS_Store", "Thumbs.db", "desktop.ini"} {
		_ = os.WriteFile(filepath.Join(finder, name), []byte{0, 0, 0, 1}, 0600)
	}
	a.dialogOpen = false
	a.newWorkspace()
	a.setNewWorkspaceDir(a.createWorkspace, finder)
	tt.Frame()
	if tt.HasText("Directory is not empty. Do you want to open it instead?") || a.createWorkspace.dir != finder {
		t.Fatalf("%q %v", a.createWorkspace.dir, tt.Texts())
	}

	full := t.TempDir()
	_ = os.WriteFile(filepath.Join(full, "yaak.wk_x.yaml"), []byte("model: workspace\nid: wk_x\nname: X\n"), 0600)
	a.dialogOpen = false
	a.newWorkspace()
	a.setNewWorkspaceDir(a.createWorkspace, full)
	tt.Frame()
	if !tt.HasText("Directory is not empty. Do you want to open it instead?") || !tt.HasText("Open Workspace") || a.createWorkspace.dir != "" {
		t.Fatalf("%v", tt.Texts())
	}
}

func TestPairRowsReorderByDragging(t *testing.T) {
	a, p := templateTestApp()
	rows := []KV{{ID: "a", Name: "A", Enabled: true}, {ID: "b", Name: "B", Enabled: true}, {ID: "c", Name: "C", Enabled: true}}
	dirty := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.pairEditor(c, p, &rows, "Header", "Value", &dirty) })
	}, 700, 400)
	tt.Frame()
	names := func() string {
		var b strings.Builder
		for _, r := range rows {
			b.WriteString(r.Name + ",")
		}
		return b.String()
	}
	grip, ok := tt.Find("Drag Header 1")
	if !ok {
		t.Fatal(tt.Texts())
	}
	target, _ := tt.Find("Header 3")
	tt.Press(grip.X+grip.W/2, grip.Y+grip.H/2)
	tt.Move(grip.X+grip.W/2, grip.Y+20)
	for y := grip.Y + 20; y < target.Y+target.H-4; y += 8 {
		tt.Move(target.X+20, y)
	}
	tt.Move(target.X+20, target.Y+target.H-4)
	tt.Release(target.X+20, target.Y+target.H-4)
	tt.Frame()
	if names() != "B,C,A,," || !dirty {
		t.Fatalf("after dropping A after C: %s", names())
	}

	grip, _ = tt.Find("Drag Header 1")
	last, _ := tt.Find("Header 4")
	tt.Press(grip.X+grip.W/2, grip.Y+grip.H/2)
	tt.Move(grip.X+grip.W/2, grip.Y+20)
	tt.Release(last.X+20, last.Y+last.H-4)
	tt.Frame()
	if names() != "C,A,B,," {
		t.Fatalf("after dropping B at the end: %s", names())
	}
}
