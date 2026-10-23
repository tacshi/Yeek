package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"

	"yeek/locales"
)

type GitFile struct {
	Path, Staging, Worktree string
	Model                   Object
}
type GitCommit struct {
	Hash, Message, Author, Email string
	When                         time.Time
}
type GitStatus struct {
	RelaDir           string
	Branch            string
	Files             []GitFile
	Branches, Remotes []string
	Commits           []GitCommit
}

func (e *Engine) GitStatus(ctx context.Context, dir string) (GitStatus, error) {
	result := GitStatus{Files: []GitFile{}, Branches: []string{}, Remotes: []string{}, Commits: []GitCommit{}}
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

func (e *Engine) GitInit(dir string) error {
	_, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")}})
	return err
}
func (e *Engine) GitStage(dir string, files []string, staged bool) error {
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
			return errors.New(locales.GitPathsMustStayInsideThe())
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

func syncModelYAML(data []byte) string {
	var m Object
	if yaml.Unmarshal(data, &m) != nil || !syncKind(str(m, "model")) || str(m, "id") == "" {
		return ""
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return ""
	}
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
	spec := ":(top,literal)" + path
	if s := status.File(path); s.Worktree == git.Untracked || s.Staging == git.Added {
		if err := gitRun(ctx, root, locales.FailedToRestore(), "rm", "--cached", "--quiet", "--ignore-unmatch", "--", spec); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			return err
		}
		return nil
	}
	return gitRun(ctx, root, locales.FailedToRestore(), "checkout", "HEAD", "--", spec)
}

func checkGitPath(path string) error {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "/") || clean != path || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New(locales.InvalidGitPathPath(strconv.Quote(path)))
	}
	return nil
}

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
	return gitRun(ctx, root, locales.FailedToRestore(), "checkout", commit.Hash.String(), "--", ":(top,literal)"+path)
}

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
