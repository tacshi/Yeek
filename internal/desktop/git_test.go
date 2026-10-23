package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.refreshGit()
	tt.Frame()
	workspaceName := requestName(a.models[a.workspace])
	for _, want := range []string{workspaceName, "Folder", "Inner", "Alpha", "untracked", "Other files", "README.md"} {
		if !tt.HasText(want) {
			t.Fatalf("tree lacks %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Stage change Folder"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tree, _ := a.commitTree()
	folder := tree.children[slices.IndexFunc(tree.children, func(n *commitNode) bool { return s(n.entry.model, "name") == "Folder" })]
	if checked, _ := folder.checkedStatus(); !checked || !folder.children[0].entry.staged {
		t.Fatal("folder not staged", folder.entry, folder.children[0].entry)
	}
	if checked, mixed := tree.checkedStatus(); checked || !mixed {
		t.Fatal("workspace not mixed", checked, mixed)
	}
	snapshot(t, tt, "git_commit_tree")
	if err := tt.Click("Collapse Folder"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Inner") {
		t.Fatal("collapsed folder shows its requests")
	}
	if err := tt.Click("Expand Folder"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Unstage change " + workspaceName); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Stage change README.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, f := range a.gitState.Files {
		if f.Staging != "A" {
			t.Fatal("not staged", f)
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

	var file string
	for _, entry := range entries {
		data, _ := os.ReadFile(filepath.Join(dir, entry.Name())) // #nosec G304 -- a file of the test's directory.
		if strings.Contains(string(data), "name: Alpha") {
			file = entry.Name()
			if err := os.WriteFile(filepath.Join(dir, file), []byte(strings.Replace(string(data), "name: Alpha", "name: Beta", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if file == "" {
		t.Fatal("no file for Alpha")
	}
	a.refreshGit()
	tt.Frame()
	if err := tt.Click("Git: " + head); err != nil {
		t.Fatal(tt.Texts())
	}
	if err := tt.ChooseMenuItem("Commit..."); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Select a change to view diff") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("modified"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.gitDiff == nil || a.gitDiff.diff == nil || !tt.HasText("Discard Changes") {
		t.Fatal("no diff", tt.Texts())
	}
	if old, next := a.gitDiff.diff.Old.String(), a.gitDiff.diff.New.String(); !strings.Contains(old, "name: Alpha") || !strings.Contains(next, "name: Beta") {
		t.Fatalf("diff: %q → %q", old, next)
	}
	snapshot(t, tt, "git_commit_diff")
	if err := tt.Click("Discard Changes"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.dialogOpen || a.dialog != "git_commit" {
		t.Fatal("Cancel did not go back to the commit dialog", a.dialog)
	}
	if err := tt.Click("Discard Changes"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Confirm Discard"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if data, _ := os.ReadFile(filepath.Join(dir, file)); !strings.Contains(string(data), "name: Alpha") { // #nosec G304 -- a file of the test's directory.
		t.Fatalf("not discarded: %s", data)
	}
	if !a.dialogOpen || a.dialog != "git_commit" || len(a.gitState.Files) != 0 || !tt.HasText("No changes since last commit") {
		t.Fatal(a.dialog, a.gitState.Files, tt.Texts())
	}

	a.dialogOpen = false
	data, _ := os.ReadFile(filepath.Join(dir, file)) // #nosec G304 -- a file of the test's directory.
	if err := os.WriteFile(filepath.Join(dir, file), []byte(strings.Replace(string(data), "name: Alpha", "name: Delta", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Engine.GitStage(dir, []string{file}, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Engine.GitCommitStaged(t.Context(), dir, "Rename"); err != nil {
		t.Fatal(err)
	}
	if err := a.syncNow(a.workspace, dir); err != nil {
		t.Fatal(err)
	}
	reload := func() {
		all, err := a.Engine.Store.List(t.Context(), "", "")
		if err != nil {
			t.Fatal(err)
		}
		a.replace(all)
	}
	reload()
	a.refreshGit()
	tt.Frame()
	if err := tt.RightClick("Delta"); err != nil {
		t.Fatal(tt.Texts())
	}
	if menu := tt.Menu(); !slices.Contains(menu, "View History") || slices.Contains(menu, "Restore Changes") {
		t.Fatal(menu)
	}
	if err := tt.ChooseMenuItem("View History"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	h := a.fileHistory
	if a.dialog != "git_file_history" || h == nil || len(h.commits) != 2 || h.selected != h.commits[0].Hash || !tt.HasText("Restore File") {
		t.Fatal(a.dialog, h, tt.Texts())
	}
	if h.diff == nil || !strings.Contains(h.diff.Old.String(), "name: Alpha") || !strings.Contains(h.diff.New.String(), "name: Delta") {
		t.Fatal("history diff", h.diff)
	}
	snapshot(t, tt, "git_file_history")
	if err := tt.Click("Initial"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if h.selected != h.commits[1].Hash || h.diff.Old.String() != "" || !strings.Contains(h.diff.New.String(), "name: Alpha") {
		t.Fatal("first commit diff", h.selected, h.diff.Old.String())
	}
	if err := tt.Click("Restore File"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Confirm Restore"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if data, _ := os.ReadFile(filepath.Join(dir, file)); !strings.Contains(string(data), "name: Alpha") { // #nosec G304 -- a file of the test's directory.
		t.Fatalf("not restored: %s", data)
	}
	if a.dialog != "git_file_history" || !a.dialogOpen || !tt.HasText("Restore File") {
		t.Fatal("not back in the history", a.dialog)
	}
	a.dialogOpen = false
	reload()
	a.refreshGit()
	tt.Frame()
	if err := tt.RightClick("Alpha"); err != nil {
		t.Fatal(tt.Texts())
	}
	if err := tt.ChooseMenuItem("Restore Changes"); err != nil {
		t.Fatal(tt.Menu(), err)
	}
	if err := tt.Click("Confirm Restore"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if data, _ := os.ReadFile(filepath.Join(dir, file)); !strings.Contains(string(data), "name: Delta") { // #nosec G304 -- a file of the test's directory.
		t.Fatalf("changes not restored: %s", data)
	}
	_ = e
	_ = engine.SyncNewest
}
