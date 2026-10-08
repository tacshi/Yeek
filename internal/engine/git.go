package engine

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

type GitFile struct{ Path, Staging, Worktree string }
type GitCommit struct {
	Hash, Message, Author, Email string
	When                         time.Time
}
type GitStatus struct {
	Branch            string
	Files             []GitFile
	Branches, Remotes []string
	Commits           []GitCommit
}

func (e *Engine) GitStatus(ctx context.Context, dir string) (GitStatus, error) {
	result := GitStatus{Files: []GitFile{}, Branches: []string{}, Remotes: []string{}, Commits: []GitCommit{}}
	// The sync directory can be inside a larger repository, whose other
	// files are not the workspace's.
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
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
	scope := ""
	real := func(path string) string {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return path
	}
	if rel, err := filepath.Rel(real(tree.Filesystem.Root()), real(dir)); err == nil && rel != "." {
		scope = filepath.ToSlash(rel) + "/"
	}
	for path, file := range status {
		if strings.HasPrefix(path, scope) {
			result.Files = append(result.Files, GitFile{Path: path, Staging: string(file.Staging), Worktree: string(file.Worktree)})
		}
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
			result.Commits = append(result.Commits, GitCommit{Hash: commit.Hash.String(), Message: strings.TrimSpace(commit.Message), Author: commit.Author.Name, Email: commit.Author.Email, When: commit.Author.When})
		}
	}
	return result, nil
}
func (e *Engine) GitInit(dir string) error { _, err := git.PlainInit(dir, false); return err }
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
