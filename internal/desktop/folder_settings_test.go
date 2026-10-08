package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestFolderSettingsTabsAutosaveAndVariables(t *testing.T) {
	a, e := cookieApp(t)
	parent, _ := e.Save(t.Context(), engine.Object{"model": "folder", "workspaceId": a.workspace, "name": "API"})
	folder, _ := e.Save(t.Context(), engine.Object{"model": "folder", "workspaceId": a.workspace, "folderId": s(parent, "id"), "name": "Users"})
	a.applyModel(parent)
	a.applyModel(folder)
	a.openScope(folder)
	tt := ui.NewTester(a.View, 1360, 860)
	for _, text := range []string{"API", "Users", "General", "Settings", "Headers", "Auth", "Variables", "Folder Name", "Delete Folder", s(folder, "id")} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	if path := os.Getenv("YEEK_FOLDER"); path != "" {
		f, _ := os.Create(path) // #nosec G304 G703 -- the test runner explicitly chooses the snapshot destination.
		_ = png.Encode(f, tt.Image())
		_ = f.Close()
	}
	// The name saves as it is typed, with no Save button.
	// The field sits under its "Folder Name" caption.
	caption, _ := tt.Find("Folder Name")
	tt.ClickAt(caption.X+40, caption.Y+caption.H+16)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Users v2")
	saved, _ := e.Store.Get(t.Context(), s(folder, "id"))
	if s(saved, "name") != "Users v2" || tt.HasText("Save") {
		t.Fatalf("name = %q", s(saved, "name"))
	}
	// Variables: create the folder environment, then edit it.
	if err := tt.Click("Variables"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Override Variables for requests within this folder.") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Create Folder Environment"); err != nil {
		t.Fatal(err)
	}
	env := a.folderEnvironment(s(folder, "id"))
	if env == nil || s(env, "name") != "Folder Environment" {
		t.Fatalf("folder environment = %v", env)
	}
	tt.Frame()
	if err := tt.Click("Variable 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("base_url")
	saved, _ = e.Store.Get(t.Context(), s(env, "id"))
	if rows := oslice(saved, "variables"); len(rows) != 1 || s(rows[0], "name") != "base_url" {
		t.Fatalf("variables = %v", saved["variables"])
	}
	if !tt.HasText("Show Values") || tt.HasText("Folder Environment") {
		t.Fatal("folder environment editor should hide the name and offer Show Values")
	}
}
