package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

type GitFile struct {
	Path, Staging, Worktree string
	// Model is the model the file holds, or held at HEAD when deleted, as
	// Yaak's status entries' next ?? prev; nil for another file.
	Model Object
}
type GitCommit struct {
	Hash, Message, Author, Email string
	When                         time.Time
}
type GitStatus struct {
	// RelaDir is the directory's path in the repository, "" at its root.
	RelaDir           string
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
	result.RelaDir = strings.TrimSuffix(scope, "/")
	var headCommit *object.Commit
	if head, err := repo.Head(); err == nil {
		headCommit, _ = repo.CommitObject(head.Hash())
	}
	root := tree.Filesystem.Root()
	for path, file := range status {
		if strings.HasPrefix(path, scope) {
			result.Files = append(result.Files, GitFile{Path: path, Staging: string(file.Staging), Worktree: string(file.Worktree), Model: gitFileModel(root, headCommit, path)})
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

// GitInit is Yaak's git_init: a repository whose branch is main, as Git
// and GitHub default to, rather than go-git's master.
func (e *Engine) GitInit(dir string) error {
	_, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")}})
	return err
}
func (e *Engine) GitStage(dir string, files []string, staged bool) error {
	// Files are named by their paths in the repository, which can be
	// around the sync directory, as GitStatus lists them.
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
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

// GitFileVersions returns a file of the repository around dir, by its path
// in the repository, as the model it holds at HEAD and in the worktree, in
// YAML, as Yaak's commit dialog diffs them: empty where the file is absent
// or holds no model.
func (e *Engine) GitFileVersions(dir, path string) (prev, next string, err error) {
	if err = checkGitPath(path); err != nil {
		return "", "", err
	}
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", "", err
	}
	if head, err := repo.Head(); err == nil {
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			if file, err := commit.File(path); err == nil {
				if content, err := file.Contents(); err == nil {
					prev = syncModelYAML([]byte(content))
				}
			}
		}
	}
	tree, err := repo.Worktree()
	if err != nil {
		return "", "", err
	}
	if data, err := os.ReadFile(filepath.Join(tree.Filesystem.Root(), filepath.FromSlash(path))); err == nil {
		next = syncModelYAML(data)
	}
	return prev, next, nil
}

// syncModelYAML is a sync file's model in YAML with an indent of 2, its
// fields in the file's order, or "" when the file holds no model.
func syncModelYAML(data []byte) string {
	var m Object
	if yaml.Unmarshal(data, &m) != nil || !syncKind(str(m, "model")) || str(m, "id") == "" {
		return ""
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return ""
	}
	// Styles and comments go, as Yaak's stringify writes the model anew: a
	// JSON file's flow style and quotes, and a YAML file's own. Tags stay,
	// so the encoder quotes what would read as another type.
	var block func(n *yaml.Node)
	block = func(n *yaml.Node) {
		n.Style, n.HeadComment, n.LineComment, n.FootComment = 0, "", "", ""
		for _, c := range n.Content {
			block(c)
		}
	}
	block(&doc)
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if enc.Encode(&doc) != nil || enc.Close() != nil {
		return ""
	}
	return out.String()
}

// GitRestore is Yaak's git_restore: it discards the uncommitted changes of
// a file of the repository around dir, by its path in the repository,
// staged or not. A file new since HEAD is removed.
func (e *Engine) GitRestore(ctx context.Context, dir, path string) error {
	if err := checkGitPath(path); err != nil {
		return err
	}
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	root := tree.Filesystem.Root()
	status, err := tree.Status()
	if err != nil {
		return err
	}
	// A pathspec from the repository's root, matched as it is written.
	spec := ":(top,literal)" + path
	if s := status.File(path); s.Worktree == git.Untracked || s.Staging == git.Added {
		if err := gitRun(ctx, root, "Failed to restore", "rm", "--cached", "--quiet", "--ignore-unmatch", "--", spec); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return err
		}
		return nil
	}
	return gitRun(ctx, root, "Failed to restore", "checkout", "HEAD", "--", spec)
}

// checkGitPath rejects a path that is not a file's within the repository.
func checkGitPath(path string) error {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || clean != path || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid git path %q", path)
	}
	return nil
}

// GitRepoPath returns the path in the repository around dir of a file of
// dir, named by its path in dir.
func (e *Engine) GitRepoPath(dir, name string) (string, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	real := func(path string) string {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return path
	}
	rel, err := filepath.Rel(real(tree.Filesystem.Root()), filepath.Join(real(dir), name))
	if err != nil {
		return "", err
	}
	path := filepath.ToSlash(rel)
	return path, checkGitPath(path)
}

// GitFileLog is Yaak's git_log_for_file: the commits from HEAD, newest
// first, that change a file of the repository around dir, by its path in
// the repository, from their first parents.
func (e *Engine) GitFileLog(ctx context.Context, dir, path string) ([]GitCommit, error) {
	commits := []GitCommit{}
	if err := checkGitPath(path); err != nil {
		return commits, err
	}
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return commits, err
	}
	head, err := repo.Head()
	if err != nil {
		// A repository without commits has no history.
		return commits, nil
	}
	log, err := repo.Log(&git.LogOptions{From: head.Hash(), Order: git.LogOrderCommitterTime})
	if err != nil {
		return commits, err
	}
	defer log.Close()
	for {
		if err := ctx.Err(); err != nil {
			return commits, err
		}
		commit, err := log.Next()
		if err != nil {
			break
		}
		var before plumbing.Hash
		if parent, err := commit.Parent(0); err == nil {
			before = blobHash(parent, path)
		}
		if blobHash(commit, path) != before {
			commits = append(commits, GitCommit{Hash: commit.Hash.String(), Message: strings.TrimSpace(commit.Message), Author: commit.Author.Name, Email: commit.Author.Email, When: commit.Author.When})
		}
	}
	return commits, nil
}

// blobHash is the hash of a file of a commit's tree, zero without it.
func blobHash(commit *object.Commit, path string) plumbing.Hash {
	tree, err := commit.Tree()
	if err != nil {
		return plumbing.ZeroHash
	}
	entry, err := tree.FindEntry(path)
	if err != nil {
		return plumbing.ZeroHash
	}
	return entry.Hash
}

// GitFileDiffForCommit is Yaak's git_file_diff_for_commit: a file of the
// repository around dir, by its path in the repository, as the commit's
// first parent has it and as the commit does, empty where absent.
func (e *Engine) GitFileDiffForCommit(dir, hash, path string) (original, modified string, err error) {
	if err = checkGitPath(path); err != nil {
		return "", "", err
	}
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", "", err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return "", "", err
	}
	text := func(c *object.Commit) (string, error) {
		file, err := c.File(path)
		if errors.Is(err, object.ErrFileNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return file.Contents()
	}
	if parent, err := commit.Parent(0); err == nil {
		if original, err = text(parent); err != nil {
			return "", "", err
		}
	}
	if modified, err = text(commit); err != nil {
		return "", "", err
	}
	return original, modified, nil
}

// GitRestoreFromCommit is Yaak's git_restore_file_from_commit: a file of
// the repository around dir, by its path in the repository, becomes the
// commit's, staged, or goes where the commit has none.
func (e *Engine) GitRestoreFromCommit(ctx context.Context, dir, hash, path string) error {
	if err := checkGitPath(path); err != nil {
		return err
	}
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return err
	}
	commit, err := repo.CommitObject(plumbing.NewHash(hash))
	if err != nil {
		return err
	}
	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	root := tree.Filesystem.Root()
	if blobHash(commit, path).IsZero() {
		return os.RemoveAll(filepath.Join(root, filepath.FromSlash(path)))
	}
	return gitRun(ctx, root, "Failed to restore", "checkout", commit.Hash.String(), "--", ":(top,literal)"+path)
}

// gitFileModel is the model a file of the repository at root holds, else
// the one it holds at HEAD, else nil.
func gitFileModel(root string, head *object.Commit, path string) Object {
	model := func(data []byte) Object {
		var m Object
		if yaml.Unmarshal(data, &m) != nil || !syncKind(str(m, "model")) || str(m, "id") == "" {
			return nil
		}
		return m
	}
	if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))); err == nil { // #nosec G304 -- a file git reports in the repository.
		if m := model(data); m != nil {
			return m
		}
	}
	if head == nil {
		return nil
	}
	file, err := head.File(path)
	if err != nil {
		return nil
	}
	content, err := file.Contents()
	if err != nil {
		return nil
	}
	return model([]byte(content))
}
