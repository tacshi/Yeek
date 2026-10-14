package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitFixture(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Test", "GIT_AUTHOR_EMAIL": "test@example.com", "GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com", "GIT_CONFIG_GLOBAL": filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM": "1"} {
		t.Setenv(k, v)
	}
}

func commitFile(t *testing.T, e *Engine, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.GitStage(dir, []string{name}, true); err != nil {
		t.Fatal(err)
	}
	if err := e.GitCommitStaged(t.Context(), dir, message); err != nil {
		t.Fatal(err)
	}
}

func TestGitBranchesAndCommits(t *testing.T) {
	gitFixture(t)
	e := testEngine(t)
	dir := t.TempDir()
	if err := e.GitInit(dir); err != nil {
		t.Fatal(err)
	}
	if err := e.GitCommitStaged(t.Context(), dir, "nothing"); err == nil || err.Error() != "No staged changes to commit" {
		t.Fatal(err)
	}
	commitFile(t, e, dir, "yaak.rq_1.yaml", "model: http_request\n", "Add request")
	info, err := e.GitBranchInfo(t.Context(), dir)
	if err != nil || info.Head == "" || len(info.LocalBranches) != 1 {
		t.Fatal(info, err)
	}
	main := info.Head
	if err := e.GitCreateBranch(t.Context(), dir, "feature", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GitCheckoutBranch(t.Context(), dir, "feature", false); err != nil {
		t.Fatal(err)
	}
	commitFile(t, e, dir, "yaak.rq_2.yaml", "model: http_request\n", "Feature")
	if _, err := e.GitCheckoutBranch(t.Context(), dir, main, false); err != nil {
		t.Fatal(err)
	}
	if err := e.GitDeleteLocalBranch(t.Context(), dir, "feature", false); !errors.Is(err, ErrNotFullyMerged) {
		t.Fatal("unmerged branch deleted", err)
	}
	if err := e.GitMergeBranch(t.Context(), dir, "feature"); err != nil {
		t.Fatal(err)
	}
	if err := e.GitRenameBranch(t.Context(), dir, "feature", "done"); err != nil {
		t.Fatal(err)
	}
	if err := e.GitDeleteLocalBranch(t.Context(), dir, "done", false); err != nil {
		t.Fatal(err)
	}
	status, err := e.GitStatus(t.Context(), dir)
	if err != nil || len(status.Commits) != 2 || status.Commits[0].Email != "test@example.com" {
		t.Fatal(status, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaak.rq_1.yaml"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.GitResetChanges(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if status, _ = e.GitStatus(t.Context(), dir); len(status.Files) != 0 {
		t.Fatal(status.Files)
	}
}

func TestGitPushPullAndDiverged(t *testing.T) {
	gitFixture(t)
	e := testEngine(t)
	remote, local, other := t.TempDir(), t.TempDir(), t.TempDir()
	if out, ok, err := gitCommand(t.Context(), "", "", "init", "--bare", remote); err != nil || !ok {
		t.Fatal(out, err)
	}
	if err := e.GitInit(local); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GitPush(t.Context(), local); err == nil {
		t.Fatal("pushed without a commit")
	}
	commitFile(t, e, local, "a.yaml", "a", "first")
	if _, err := e.GitPush(t.Context(), local); !errors.Is(err, ErrNoRemote) {
		t.Fatal(err)
	}
	if err := e.GitRemote(local, "origin", remote, false); err != nil {
		t.Fatal(err)
	}
	if remotes, _ := e.GitRemotes(t.Context(), local); len(remotes) != 1 || remotes[0].URL != remote {
		t.Fatal(remotes)
	}
	if r, err := e.GitPush(t.Context(), local); err != nil || r.Kind != "success" {
		t.Fatal(r, err)
	}
	info, _ := e.GitBranchInfo(t.Context(), local)
	if out, ok, err := gitCommand(t.Context(), "", "", "clone", "-b", info.Head, remote, other); err != nil || !ok {
		t.Fatal(out, err)
	}
	commitFile(t, e, other, "b.yaml", "b", "from other")
	if r, err := e.GitPush(t.Context(), other); err != nil || r.Kind != "success" {
		t.Fatal(r, err)
	}
	if r, err := e.GitPull(t.Context(), local); err != nil || r.Kind != "success" {
		t.Fatal(r, err)
	}
	if r, err := e.GitPull(t.Context(), local); err != nil || r.Kind != "up_to_date" {
		t.Fatal(r, err)
	}
	// Both sides commit: the pull diverges, and a merge resolves it.
	commitFile(t, e, other, "c.yaml", "c", "other again")
	if _, err := e.GitPush(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	commitFile(t, e, local, "d.yaml", "d", "local again")
	r, err := e.GitPull(t.Context(), local)
	if err != nil || r.Kind != "diverged" || r.Remote != "origin" || r.Branch != info.Head {
		t.Fatal(r, err)
	}
	if r, err = e.GitPullMerge(t.Context(), local, r.Remote, r.Branch); err != nil || r.Kind != "success" {
		t.Fatal(r, err)
	}
	if info, _ = e.GitBranchInfo(t.Context(), local); info.Ahead != 2 || !slices.Contains(info.RemoteBranches, "origin/"+info.Head) {
		t.Fatal(info)
	}
	if err := os.WriteFile(filepath.Join(local, "a.yaml"), []byte("dirty"), 0600); err != nil {
		t.Fatal(err)
	}
	if r, _ = e.GitPull(t.Context(), local); r.Kind != "uncommitted_changes" {
		t.Fatal(r)
	}
}

func TestGitStatusAndCommitStayInSyncDir(t *testing.T) {
	gitFixture(t)
	e := testEngine(t)
	repo := t.TempDir()
	if err := e.GitInit(repo); err != nil {
		t.Fatal(err)
	}
	sync := filepath.Join(repo, "yaak")
	if err := os.Mkdir(sync, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(sync, "a.yaml"), filepath.Join(repo, "outside.txt")} {
		if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	status, err := e.GitStatus(t.Context(), sync)
	if err != nil || len(status.Files) != 1 || status.Files[0].Path != "yaak/a.yaml" {
		t.Fatal(status.Files, err)
	}
	// Staged from the sync directory, by paths in the repository.
	if err := e.GitStage(sync, []string{"yaak/a.yaml"}, true); err != nil {
		t.Fatal(err)
	}
	if err := e.GitStage(repo, []string{"outside.txt", "yaak/a.yaml"}, true); err != nil {
		t.Fatal(err)
	}
	if err := e.GitCommitStaged(t.Context(), sync, "sync only"); err != nil {
		t.Fatal(err)
	}
	out, _, _ := gitCommand(t.Context(), repo, "", "diff", "--cached", "--name-only")
	if out != "outside.txt\n" {
		t.Fatalf("staged outside the sync dir: %q", out)
	}
}

// Yaak's git_clone: a local repository clones into a new folder whose
// parent is made; a failure says why.
func TestGitClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := testEngine(t)
	source := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", source}, args...)...).CombinedOutput(); err != nil { // #nosec G204 -- git with the test's own arguments.
			t.Fatal(string(out))
		}
	}
	dir := filepath.Join(t.TempDir(), "projects", "repo")
	result, err := e.GitClone(t.Context(), source, dir)
	if err != nil || result.Kind != "success" {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GitClone(t.Context(), filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "x")); err == nil || !strings.HasPrefix(err.Error(), "Failed to clone: ") {
		t.Fatal(err)
	}
}

// The commit dialog's diff panel: a file's model at HEAD and in the
// worktree, in YAML; Discard Changes puts back HEAD's, or removes a new
// file.
func TestGitFileVersionsAndRestore(t *testing.T) {
	gitFixture(t)
	e := testEngine(t)
	repo := t.TempDir()
	if err := e.GitInit(repo); err != nil {
		t.Fatal(err)
	}
	sync := filepath.Join(repo, "yaak")
	if err := os.Mkdir(sync, 0o750); err != nil {
		t.Fatal(err)
	}
	commitFile(t, e, repo, "yaak/yaak.rq_1.yaml", "model: http_request\nid: rq_1\nname: Before\nurl: https://example.com\n", "add")
	if err := os.WriteFile(filepath.Join(sync, "yaak.rq_1.yaml"), []byte("model: http_request\nid: rq_1\nname: After\nurl: https://example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A JSON model shows as YAML; a file without a model shows nothing.
	if err := os.WriteFile(filepath.Join(sync, "yaak.fl_1.json"), []byte(`{"model":"folder","id":"fl_1","name":"New","headers":[{"name":"A"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sync, "README.md"), []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}

	prev, next, err := e.GitFileVersions(sync, "yaak/yaak.rq_1.yaml")
	if err != nil || prev != "model: http_request\nid: rq_1\nname: Before\nurl: https://example.com\n" || next != "model: http_request\nid: rq_1\nname: After\nurl: https://example.com\n" {
		t.Fatalf("modified: %q %q %v", prev, next, err)
	}
	prev, next, err = e.GitFileVersions(sync, "yaak/yaak.fl_1.json")
	if err != nil || prev != "" || next != "model: folder\nid: fl_1\nname: New\nheaders:\n  - name: A\n" {
		t.Fatalf("new json: %q %q %v", prev, next, err)
	}
	if prev, next, err = e.GitFileVersions(sync, "yaak/README.md"); err != nil || prev != "" || next != "" {
		t.Fatalf("not a model: %q %q %v", prev, next, err)
	}
	if _, _, err = e.GitFileVersions(sync, "../outside.yaml"); err == nil {
		t.Fatal("a path outside the repository was read")
	}

	if err := e.GitStage(repo, []string{"yaak/yaak.rq_1.yaml", "yaak/yaak.fl_1.json"}, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"yaak/yaak.rq_1.yaml", "yaak/yaak.fl_1.json"} {
		if err := e.GitRestore(t.Context(), sync, path); err != nil {
			t.Fatal(path, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(sync, "yaak.rq_1.yaml")); !strings.Contains(string(data), "name: Before") { // #nosec G304 -- a file of the test's directory.
		t.Fatalf("modified file not restored: %q", data)
	}
	if _, err := os.Stat(filepath.Join(sync, "yaak.fl_1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new file not removed", err)
	}
	status, err := e.GitStatus(t.Context(), sync)
	if err != nil || len(status.Files) != 1 || status.Files[0].Path != "yaak/README.md" {
		t.Fatal(status.Files, err)
	}
}

// Yaak's FileHistoryDialog: the commits that change a file, its text
// before and after each, and restoring it from one.
func TestGitFileHistory(t *testing.T) {
	gitFixture(t)
	e := testEngine(t)
	repo := t.TempDir()
	if err := e.GitInit(repo); err != nil {
		t.Fatal(err)
	}
	sync := filepath.Join(repo, "yaak")
	if err := os.Mkdir(sync, 0o750); err != nil {
		t.Fatal(err)
	}
	path, err := e.GitRepoPath(sync, "yaak.rq_1.yaml")
	if err != nil || path != "yaak/yaak.rq_1.yaml" {
		t.Fatal(path, err)
	}
	if commits, err := e.GitFileLog(t.Context(), sync, path); err != nil || len(commits) != 0 {
		t.Fatal("history before the first commit", commits, err)
	}
	commitFile(t, e, repo, path, "name: One\n", "first")
	commitFile(t, e, repo, "other.txt", "x", "unrelated")
	commitFile(t, e, repo, path, "name: Two\n", "second")
	commits, err := e.GitFileLog(t.Context(), sync, path)
	if err != nil || len(commits) != 2 || commits[0].Message != "second" || commits[1].Message != "first" {
		t.Fatal(commits, err)
	}
	if original, modified, err := e.GitFileDiffForCommit(sync, commits[0].Hash, path); err != nil || original != "name: One\n" || modified != "name: Two\n" {
		t.Fatalf("second: %q %q %v", original, modified, err)
	}
	if original, modified, err := e.GitFileDiffForCommit(sync, commits[1].Hash, path); err != nil || original != "" || modified != "name: One\n" {
		t.Fatalf("first: %q %q %v", original, modified, err)
	}
	if err := e.GitRestoreFromCommit(t.Context(), sync, commits[1].Hash, path); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(sync, "yaak.rq_1.yaml")); string(data) != "name: One\n" { // #nosec G304 -- a file of the test's directory.
		t.Fatalf("not restored: %q", data)
	}
	// A commit without the file removes it.
	unrelated, _, _ := gitCommand(t.Context(), repo, "", "rev-list", "--max-count=1", "--skip=1", "HEAD")
	if err := os.Remove(filepath.Join(sync, "yaak.rq_1.yaml")); err != nil {
		t.Fatal(err)
	}
	commitFile(t, e, repo, "yaak/yaak.rq_2.yaml", "name: Three\n", "third")
	if err := e.GitRestoreFromCommit(t.Context(), sync, strings.TrimSpace(unrelated), "yaak/yaak.rq_2.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sync, "yaak.rq_2.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file not removed", err)
	}
}

// Yaak's git_init starts repositories on main, not master.
func TestGitInitOnMain(t *testing.T) {
	gitFixture(t)
	e := testEngine(t)
	repo := t.TempDir()
	if err := e.GitInit(repo); err != nil {
		t.Fatal(err)
	}
	if info, err := e.GitBranchInfo(t.Context(), repo); err != nil || info.Head != "main" {
		t.Fatal("before the first commit", info.Head, err)
	}
	commitFile(t, e, repo, "a.txt", "a", "first")
	if status, err := e.GitStatus(t.Context(), repo); err != nil || status.Branch != "main" || !slices.Equal(status.Branches, []string{"main"}) {
		t.Fatal("after the first commit", status.Branch, status.Branches, err)
	}
}
