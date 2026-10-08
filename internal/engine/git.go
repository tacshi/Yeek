package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/zalando/go-keyring"
)

type GitFile struct{ Path, Staging, Worktree string }
type GitCommit struct {
	Hash, Message, Author string
	When                  time.Time
}
type GitStatus struct {
	Branch            string
	Files             []GitFile
	Branches, Remotes []string
	Commits           []GitCommit
}

func (e *Engine) GitStatus(ctx context.Context, dir string) (GitStatus, error) {
	result := GitStatus{Files: []GitFile{}, Branches: []string{}, Remotes: []string{}, Commits: []GitCommit{}}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return result, err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return result, err
	}
	status, err := tree.Status()
	if err != nil {
		return result, err
	}
	for path, file := range status {
		result.Files = append(result.Files, GitFile{Path: path, Staging: string(file.Staging), Worktree: string(file.Worktree)})
	}
	slices.SortFunc(result.Files, func(a, b GitFile) int { return strings.Compare(a.Path, b.Path) })
	if head, err := repo.Head(); err == nil {
		result.Branch = head.Name().Short()
		if !head.Name().IsBranch() {
			result.Branch = head.Hash().String()[:8]
		}
	}
	branches, err := repo.Branches()
	if err != nil {
		return result, err
	}
	err = branches.ForEach(func(ref *plumbing.Reference) error {
		result.Branches = append(result.Branches, ref.Name().Short())
		return nil
	})
	if err != nil {
		return result, err
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return result, err
	}
	for _, remote := range remotes {
		result.Remotes = append(result.Remotes, remote.Config().Name)
	}
	log, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err == nil {
		defer log.Close()
		for len(result.Commits) < 50 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			commit, err := log.Next()
			if err != nil {
				break
			}
			result.Commits = append(result.Commits, GitCommit{Hash: commit.Hash.String(), Message: strings.TrimSpace(commit.Message), Author: commit.Author.Name, When: commit.Author.When})
		}
	}
	return result, nil
}
func (e *Engine) GitInit(dir string) error { _, err := git.PlainInit(dir, false); return err }
func (e *Engine) GitClone(ctx context.Context, remote, dir string) error {
	auth, err := gitAuth(remote)
	if err != nil {
		return err
	}
	_, err = git.PlainCloneContext(ctx, dir, false, &git.CloneOptions{URL: remote, Auth: auth})
	return err
}
func (e *Engine) GitStage(dir string, files []string, staged bool) error {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	for _, path := range files {
		if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(filepath.Clean(path), ".."+string(filepath.Separator)) {
			return errors.New("git paths must stay inside the workspace")
		}
	}
	if staged {
		for _, path := range files {
			if _, err = tree.Add(path); err != nil {
				return err
			}
		}
		return nil
	}
	return tree.Restore(&git.RestoreOptions{Staged: true, Files: files})
}
func (e *Engine) GitCommit(dir, message, name, email string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("enter a commit message")
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	options := &git.CommitOptions{}
	if name != "" && email != "" {
		options.Author = &object.Signature{Name: name, Email: email, When: time.Now()}
	}
	hash, err := tree.Commit(message, options)
	return hash.String(), err
}
func (e *Engine) GitCheckout(dir, branch string, create bool) error {
	ref := plumbing.NewBranchReferenceName(branch)
	if err := ref.Validate(); err != nil {
		return err
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	return tree.Checkout(&git.CheckoutOptions{Branch: ref, Create: create})
}
func (e *Engine) GitDeleteBranch(dir, branch string) error {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}
	head, err := repo.Head()
	if err != nil {
		return err
	}
	ref := plumbing.NewBranchReferenceName(branch)
	if head.Name() == ref {
		return errors.New("switch to another branch before deleting this one")
	}
	return repo.Storer.RemoveReference(ref)
}
func (e *Engine) GitRemote(dir, name, remote string, remove bool) error {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}
	if remove {
		return repo.DeleteRemote(name)
	}
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: name, URLs: []string{remote}})
	return err
}
func (e *Engine) GitNetwork(ctx context.Context, dir, operation, remote string) error {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}
	if remote == "" {
		remote = "origin"
	}
	r, err := repo.Remote(remote)
	if err != nil {
		return err
	}
	auth, err := gitAuth(r.Config().URLs[0])
	if err != nil {
		return err
	}
	switch operation {
	case "fetch":
		err = repo.FetchContext(ctx, &git.FetchOptions{RemoteName: remote, Auth: auth})
	case "push":
		err = repo.PushContext(ctx, &git.PushOptions{RemoteName: remote, Auth: auth})
	case "pull":
		tree, e := repo.Worktree()
		if e != nil {
			return e
		}
		err = tree.PullContext(ctx, &git.PullOptions{RemoteName: remote, Auth: auth})
	default:
		return fmt.Errorf("unknown Git operation %q", operation)
	}
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil
	}
	return err
}
func (e *Engine) GitDiff(dir, path string) (string, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", err
	}
	file, err := commit.File(path)
	if err != nil {
		return "", err
	}
	return file.Contents()
}
func (e *Engine) SetGitCredential(remote, user, password string) error {
	return keyring.Set("app.yeek.git", remote, user+"\n"+password)
}
func gitAuth(remote string) (transport.AuthMethod, error) {
	if !strings.HasPrefix(remote, "http://") && !strings.HasPrefix(remote, "https://") {
		return nil, nil
	}
	value, err := keyring.Get("app.yeek.git", remote)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Git credentials: %w", err)
	}
	user, password, _ := strings.Cut(value, "\n")
	return &githttp.BasicAuth{Username: user, Password: password}, nil
}
