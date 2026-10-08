package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Yaak's Git menu changes repositories with the git command, as Yeek does.

// ErrGitNotFound is returned when git is not installed.
var ErrGitNotFound = errors.New("git was not found; install Git to use it with Yeek")

// gitCommand runs git in dir, without prompting in a terminal, and returns
// its output and error output together.
func gitCommand(ctx context.Context, dir string, stdin string, args ...string) (string, bool, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", false, ErrGitNotFound
	}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- git is run with fixed subcommands; names are arguments, not shell text.
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return "", false, err
	}
	return out.String(), err == nil, nil
}

func gitRun(ctx context.Context, dir, failure string, args ...string) error {
	out, ok, err := gitCommand(ctx, dir, "", args...)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s: %s", failure, strings.TrimSpace(out))
	}
	return nil
}

// GitBranchInfo is Yaak's: the branch checked out, the local branches and
// the remote ones, and how far the branch is ahead of and behind its
// upstream.
type GitBranchInfo struct {
	Head                          string
	LocalBranches, RemoteBranches []string
	Ahead, Behind                 int
}

func (e *Engine) GitBranchInfo(ctx context.Context, dir string) (GitBranchInfo, error) {
	var info GitBranchInfo
	if _, err := gitDir(dir); err != nil {
		return info, err
	}
	out, ok, err := gitCommand(ctx, dir, "", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return info, err
	}
	if ok {
		info.Head = strings.TrimSpace(out)
	}
	if info.Head == "HEAD" || !ok {
		// No commit yet, or a detached HEAD: the branch HEAD names.
		if out, ok, _ := gitCommand(ctx, dir, "", "symbolic-ref", "--short", "HEAD"); ok {
			info.Head = strings.TrimSpace(out)
		}
	}
	list := func(refs string) []string {
		out, ok, _ := gitCommand(ctx, dir, "", "for-each-ref", "--format=%(refname:short)", refs)
		if !ok {
			return nil
		}
		var names []string
		for name := range strings.Lines(out) {
			if name = strings.TrimSpace(name); name != "" && !strings.HasSuffix(name, "/HEAD") && name != "origin" {
				names = append(names, name)
			}
		}
		return names
	}
	info.LocalBranches, info.RemoteBranches = list("refs/heads"), list("refs/remotes")
	// As Yaak does, against origin's branch of the same name.
	if out, ok, _ := gitCommand(ctx, dir, "", "rev-list", "--left-right", "--count", "HEAD...refs/remotes/origin/"+info.Head); ok {
		if fields := strings.Fields(out); len(fields) == 2 {
			info.Ahead, _ = strconv.Atoi(fields[0])
			info.Behind, _ = strconv.Atoi(fields[1])
		}
	}
	return info, nil
}

// gitDir reports whether dir is inside a Git repository.
func gitDir(dir string) (string, error) {
	out, ok, err := gitCommand(context.Background(), dir, "", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("repository not found")
	}
	return strings.TrimSpace(out), nil
}

// GitCheckoutBranch is Yaak's git_checkout_branch.
func (e *Engine) GitCheckoutBranch(ctx context.Context, dir, branch string, force bool) (string, error) {
	branch = strings.TrimPrefix(branch, "origin/")
	args := []string{"checkout"}
	if force {
		args = append(args, "--force")
	}
	return branch, gitRun(ctx, dir, "Failed to checkout", append(args, branch)...)
}

// GitCreateBranch creates a branch from base, else from HEAD.
func (e *Engine) GitCreateBranch(ctx context.Context, dir, name, base string) error {
	args := []string{"branch", name}
	if base != "" {
		args = append(args, base)
	}
	return gitRun(ctx, dir, "Failed to create branch", args...)
}

// ErrNotFullyMerged is returned when a branch to delete is not merged.
var ErrNotFullyMerged = errors.New("branch is not fully merged")

func (e *Engine) GitDeleteLocalBranch(ctx context.Context, dir, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	out, ok, err := gitCommand(ctx, dir, "", "branch", flag, name)
	switch {
	case err != nil:
		return err
	case !ok && strings.Contains(strings.ToLower(out), "not fully merged"):
		return ErrNotFullyMerged
	case !ok:
		return fmt.Errorf("Failed to delete branch: %s", strings.TrimSpace(out)) //nolint:staticcheck // Yaak's wording, shown as is.
	}
	return nil
}

func (e *Engine) GitMergeBranch(ctx context.Context, dir, name string) error {
	out, ok, err := gitCommand(ctx, dir, "", "merge", name)
	if err != nil || ok {
		return err
	}
	if strings.Contains(strings.ToLower(out), "conflict") {
		return errors.New("Merge conflicts detected. Please resolve them manually.") //nolint:staticcheck // Yaak's wording, shown as is.
	}
	return fmt.Errorf("Failed to merge: %s", strings.TrimSpace(out)) //nolint:staticcheck // Yaak's wording, shown as is.
}

func (e *Engine) GitDeleteRemoteBranch(ctx context.Context, dir, name string) error {
	return gitRun(ctx, dir, "Failed to delete remote branch", "push", "origin", "--delete", strings.TrimPrefix(name, "origin/"))
}

func (e *Engine) GitRenameBranch(ctx context.Context, dir, oldName, newName string) error {
	return gitRun(ctx, dir, "Failed to rename branch", "branch", "-m", oldName, newName)
}

// GitResetChanges discards all uncommitted changes.
func (e *Engine) GitResetChanges(ctx context.Context, dir string) error {
	return gitRun(ctx, dir, "Failed to reset", "reset", "--hard", "HEAD")
}

// GitResult is what Yaak's push and pull come back with.
type GitResult struct {
	// Kind is success, up_to_date, needs_credentials, diverged or
	// uncommitted_changes.
	Kind, Message  string
	URL, Error     string
	Remote, Branch string
}

// ErrNoRemote is returned when a repository has no remote to push to.
var ErrNoRemote = errors.New("no remote found")

// gitRemote is the remote Yaak uses: the branch's push remote, else
// origin, else the first.
func gitRemote(ctx context.Context, dir, branch string, push bool) (name, address string, err error) {
	out, _, err := gitCommand(ctx, dir, "", "remote")
	if err != nil {
		return "", "", err
	}
	remotes := strings.Fields(out)
	if len(remotes) == 0 {
		return "", "", ErrNoRemote
	}
	name = remotes[0]
	if slices.Contains(remotes, "origin") {
		name = "origin"
	}
	if push {
		for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault", "branch." + branch + ".remote"} {
			if out, ok, _ := gitCommand(ctx, dir, "", "config", "--get", key); ok && strings.TrimSpace(out) != "" {
				name = strings.TrimSpace(out)
				break
			}
		}
	}
	out, _, _ = gitCommand(ctx, dir, "", "remote", "get-url", name)
	return name, strings.TrimSpace(out), nil
}

func (e *Engine) gitHead(ctx context.Context, dir string) (string, error) {
	info, err := e.GitBranchInfo(ctx, dir)
	if err != nil {
		return "", err
	}
	if info.Head == "" {
		return "", errors.New("no branch is checked out")
	}
	return info.Head, nil
}

// GitPush is Yaak's git_push.
func (e *Engine) GitPush(ctx context.Context, dir string) (GitResult, error) {
	branch, err := e.gitHead(ctx, dir)
	if err != nil {
		return GitResult{}, err
	}
	remote, address, err := gitRemote(ctx, dir, branch, true)
	if err != nil {
		return GitResult{}, err
	}
	out, ok, err := gitCommand(ctx, dir, "", "push", remote, branch)
	if err != nil {
		return GitResult{}, err
	}
	lower := strings.ToLower(out)
	credentials := strings.Contains(lower, "could not read") || strings.Contains(lower, "unable to access") || strings.Contains(lower, "authentication failed")
	switch {
	case (strings.Contains(lower, "rejected") || strings.Contains(lower, "failed to push")) && credentials:
		return GitResult{Kind: "needs_credentials", URL: address, Error: out}, nil
	case strings.Contains(lower, "rejected") || strings.Contains(lower, "failed to push"):
		return GitResult{}, fmt.Errorf("Failed to push: %s", out) //nolint:staticcheck // Yaak's wording, shown as is.
	case !ok && strings.Contains(lower, "could not read"):
		return GitResult{Kind: "needs_credentials", URL: address}, nil
	case !ok && credentials:
		return GitResult{Kind: "needs_credentials", URL: address, Error: out}, nil
	case !ok:
		return GitResult{}, fmt.Errorf("Failed to push: %s", out) //nolint:staticcheck // Yaak's wording, shown as is.
	case strings.Contains(lower, "up-to-date"):
		return GitResult{Kind: "up_to_date"}, nil
	}
	return GitResult{Kind: "success", Message: "Pushed to " + remote + "/" + branch}, nil
}

// GitPull is Yaak's git_pull: a fetch of the branch, then a fast-forward.
func (e *Engine) GitPull(ctx context.Context, dir string) (GitResult, error) {
	if out, ok, err := gitCommand(ctx, dir, "", "status", "--porcelain", "--untracked-files=no", "--", "."); err != nil {
		return GitResult{}, err
	} else if ok && strings.TrimSpace(out) != "" {
		return GitResult{Kind: "uncommitted_changes"}, nil
	}
	branch, err := e.gitHead(ctx, dir)
	if err != nil {
		return GitResult{}, err
	}
	remote, address, err := gitRemote(ctx, dir, branch, false)
	if err != nil {
		return GitResult{}, err
	}
	out, ok, err := gitCommand(ctx, dir, "", "fetch", remote, branch)
	if err != nil {
		return GitResult{}, err
	}
	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "could not read"):
		return GitResult{Kind: "needs_credentials", URL: address}, nil
	case strings.Contains(lower, "unable to access"):
		return GitResult{Kind: "needs_credentials", URL: address, Error: out}, nil
	case !ok:
		return GitResult{}, fmt.Errorf("Failed to fetch: %s", out) //nolint:staticcheck // Yaak's wording, shown as is.
	}
	out, ok, err = gitCommand(ctx, dir, "", "merge", "--ff-only", remote+"/"+branch)
	if err != nil {
		return GitResult{}, err
	}
	lower = strings.ToLower(out)
	if !ok {
		if strings.Contains(lower, "cannot fast-forward") || strings.Contains(lower, "not possible to fast-forward") || strings.Contains(lower, "diverged") {
			return GitResult{Kind: "diverged", Remote: remote, Branch: branch}, nil
		}
		return GitResult{}, fmt.Errorf("Failed to merge: %s", out) //nolint:staticcheck // Yaak's wording, shown as is.
	}
	if strings.Contains(lower, "up to date") {
		return GitResult{Kind: "up_to_date"}, nil
	}
	return GitResult{Kind: "success", Message: "Pulled from " + remote + "/" + branch}, nil
}

// GitPullForceReset resets the branch to the remote's, for a diverged pull.
func (e *Engine) GitPullForceReset(ctx context.Context, dir, remote, branch string) (GitResult, error) {
	if err := gitRun(ctx, dir, "Failed to fetch", "fetch", remote); err != nil {
		return GitResult{}, err
	}
	if err := gitRun(ctx, dir, "Failed to reset", "reset", "--hard", remote+"/"+branch); err != nil {
		return GitResult{}, err
	}
	return GitResult{Kind: "success", Message: "Reset to " + remote + "/" + branch}, nil
}

// GitPullMerge merges the remote's branch, for a diverged pull.
func (e *Engine) GitPullMerge(ctx context.Context, dir, remote, branch string) (GitResult, error) {
	out, ok, err := gitCommand(ctx, dir, "", "pull", "--no-rebase", remote, branch)
	if err != nil {
		return GitResult{}, err
	}
	if !ok {
		if strings.Contains(strings.ToLower(out), "conflict") {
			return GitResult{}, errors.New("Merge conflicts detected. Please resolve them manually.") //nolint:staticcheck // Yaak's wording, shown as is.
		}
		return GitResult{}, fmt.Errorf("Failed to merge pull: %s", strings.TrimSpace(out)) //nolint:staticcheck // Yaak's wording, shown as is.
	}
	return GitResult{Kind: "success", Message: "Merged from " + remote + "/" + branch}, nil
}

// GitAddCredential is Yaak's git_add_credential: git's credential helper
// keeps the username and password for the remote.
func (e *Engine) GitAddCredential(ctx context.Context, remote, username, password string) error {
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return fmt.Errorf("Failed to parse remote url %s", remote) //nolint:staticcheck // Yaak's wording, shown as is.
	}
	input := "protocol=" + u.Scheme + "\nhost=" + u.Host + "\n"
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		input += "path=" + path + "\n"
	}
	input += "username=" + username + "\npassword=" + password + "\n\n"
	_, ok, err := gitCommand(ctx, "", input, "credential", "approve")
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("Failed to approve git credential") //nolint:staticcheck // Yaak's wording, shown as is.
	}
	return nil
}

// GitRemoteInfo is a remote and where it is.
type GitRemoteInfo struct{ Name, URL string }

func (e *Engine) GitRemotes(ctx context.Context, dir string) ([]GitRemoteInfo, error) {
	out, ok, err := gitCommand(ctx, dir, "", "remote")
	if err != nil || !ok {
		return nil, err
	}
	var remotes []GitRemoteInfo
	for _, name := range strings.Fields(out) {
		address, _, _ := gitCommand(ctx, dir, "", "remote", "get-url", name)
		remotes = append(remotes, GitRemoteInfo{name, strings.TrimSpace(address)})
	}
	return remotes, nil
}

// GitCommitStaged is Yaak's git_commit: it commits what is staged in dir,
// leaving what is staged elsewhere in the repository for the user.
func (e *Engine) GitCommitStaged(ctx context.Context, dir, message string) error {
	root, err := gitDir(dir)
	if err != nil {
		return err
	}
	args := []string{"--literal-pathspecs", "diff", "--cached", "--name-only", "--no-renames", "-z"}
	abs, _ := filepath.Abs(dir)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if rel, err := filepath.Rel(root, abs); err == nil && rel != "." {
		args = append(args, "--", filepath.ToSlash(rel))
	}
	out, ok, err := gitCommand(ctx, root, "", args...)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("Failed to list staged files: %s", out) //nolint:staticcheck // Yaak's wording, shown as is.
	}
	staged := slices.DeleteFunc(strings.Split(out, "\x00"), func(s string) bool { return s == "" })
	if len(staged) == 0 {
		return errors.New("No staged changes to commit") //nolint:staticcheck // Yaak's wording, shown as is.
	}
	return gitRun(ctx, root, "Failed to commit", append([]string{"--literal-pathspecs", "commit", "--message", message, "--"}, staged...)...)
}
