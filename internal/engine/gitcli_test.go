package engine

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
