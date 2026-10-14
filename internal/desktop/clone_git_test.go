package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/yeekui"
)

func TestExtractRepoNameMatchesYaak(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/user/repo.git": "repo",
		"git@github.com:user/repo.git":     "repo",
		"https://github.com/user/repo":     "repo",
		"git@host:repo.git":                "repo",
		"":                                 "",
	} {
		if got := extractRepoName(url); got != want {
			t.Errorf("%q: %q", url, got)
		}
	}
}

// Yaak's CloneGitRepositoryDialog: the directory follows the URL until
// typed, a subdirectory can hold the workspace, and the clone opens it.
func TestCloneGitRepositoryDialog(t *testing.T) {
	a, _ := cookieApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	a.openCloneGit()
	tt.Frame()
	for _, text := range []string{"Clone Git Repository", "Repository URL", "Directory", "Workspace is in a subdirectory", "Clone Repository"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	f := a.cloneGit
	f.base = "/projects"
	f.url = "https://github.com/user/api.git"
	if f.directory() != "/projects/api" {
		t.Fatal(f.directory())
	}
	f.hasSubdirectory, f.subdirectory = true, "yaak"
	if f.workspaceDirectory() != "/projects/api/yaak" {
		t.Fatal(f.workspaceDirectory())
	}
	f.hasOverride, f.override = true, "/elsewhere"
	if f.directory() != "/elsewhere" {
		t.Fatal(f.directory())
	}

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	source := t.TempDir()
	_ = os.WriteFile(filepath.Join(source, "yaak.wk_cloned.yaml"), []byte("model: workspace\nid: wk_cloned\nname: Cloned API\n"), 0600)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "-m", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", source}, args...)...).CombinedOutput(); err != nil { // #nosec G204 -- git with the test's own arguments.
			t.Fatal(string(out))
		}
	}
	*f = cloneGitForm{base: t.TempDir(), url: source}
	a.cloneRepository(f, false)
	tt.Frame()
	if f.error != "" || a.dialogOpen {
		t.Fatalf("clone: %q open %v", f.error, a.dialogOpen)
	}
	if s(a.models[a.workspace], "name") != "Cloned API" {
		t.Fatalf("workspace %v", a.models[a.workspace])
	}
}
