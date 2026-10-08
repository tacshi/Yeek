package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestGitFooterSetupAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com", "GIT_CONFIG_GLOBAL": filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM": "1"} {
		t.Setenv(k, v)
	}
	a, e, _ := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("Setup FS Sync or Git") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Setup FS Sync or Git"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Contains(menu, "Open Workspace Settings") || !slices.Contains(menu, "Hide This Message") {
		t.Fatal(menu)
	}
	tt.CloseMenu()

	dir := t.TempDir()
	a.setSyncDir(a.workspace, dir)
	if err := a.syncNow(a.workspace, dir); err != nil {
		t.Fatal(err)
	}
	a.gitRefreshed = a.gitRefreshed.AddDate(-1, 0, 0)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Setup Git") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Setup Git"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Initialize Git Repo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.gitInfo == nil || a.gitNoRepo {
		t.Fatal("repository not initialized")
	}
	head := a.gitInfo.Head
	if err := tt.Click("Git: " + head); err != nil {
		t.Fatal(tt.Texts())
	}
	menu := tt.Menu()
	for _, want := range []string{"View History...", "Manage Remotes...", "New Branch...", "Push", "Pull", "Commit...", "Reset Changes"} {
		if !slices.Contains(menu, want) {
			t.Fatalf("menu lacks %q: %v", want, menu)
		}
	}
	if err := tt.ChooseMenuItem("Commit..."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Alpha") || !tt.HasText("Added") {
		t.Fatal(tt.Texts())
	}
	for _, f := range a.gitState.Files {
		if err := tt.Click("Stage change " + f.Path); err != nil {
			t.Fatal(err)
		}
	}
	if err := tt.Click("Commit message"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Initial")
	if err := tt.Click("Commit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dialogOpen || len(a.gitState.Files) != 0 || len(a.gitState.Commits) != 1 || a.gitState.Commits[0].Message != "Initial" {
		t.Fatal(a.dialogOpen, a.gitState)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) < 5 {
		t.Fatal("workspace not synced to the directory", entries)
	}
	_ = e
	_ = engine.SyncNewest
}
