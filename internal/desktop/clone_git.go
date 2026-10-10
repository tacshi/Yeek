package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
)

// cloneGitForm is Yaak's CloneGitRepositoryDialog.
type cloneGitForm struct {
	url, base, override, subdirectory, error string
	hasOverride, hasSubdirectory, cloning    bool
}

// defaultProjectDir is Yaak's appInfo.defaultProjectDir, ~/YaakProjects, in
// Yeek's name.
func defaultProjectDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "YeekProjects"
	}
	return filepath.Join(home, "YeekProjects")
}

func (a *App) openCloneGit() {
	if a.deferUntilInputs(a.openCloneGit) {
		return
	}
	a.cloneGit = &cloneGitForm{base: defaultProjectDir()}
	a.prompt("clone_git", "Clone Git Repository", "", "")
}

var (
	repoName    = regexp.MustCompile(`/([^/]+?)(\.git)?$`)
	sshRepoName = regexp.MustCompile(`:([^/]+?)(\.git)?$`)
)

// extractRepoName is Yaak's: the repository's name from its URL, HTTPS or SSH.
func extractRepoName(url string) string {
	if m := repoName.FindStringSubmatch(url); m != nil && m[1] != "" {
		return m[1]
	}
	if m := sshRepoName.FindStringSubmatch(url); m != nil && m[1] != "" {
		return m[1]
	}
	return ""
}

// pathSeparator is Yaak's getPathSeparator: \ when the path has one.
func pathSeparator(path string) string {
	if strings.Contains(path, `\`) {
		return `\`
	}
	return "/"
}

// directory is where the repository clones: the base directory and the
// repository's name, unless typed.
func (f *cloneGitForm) directory() string {
	if f.hasOverride {
		return f.override
	}
	if name := extractRepoName(f.url); name != "" {
		return f.base + pathSeparator(f.base) + name
	}
	return f.base
}

// workspaceDirectory is the directory the workspace's files are in.
func (f *cloneGitForm) workspaceDirectory() string {
	dir := f.directory()
	if f.hasSubdirectory && f.subdirectory != "" {
		return dir + pathSeparator(f.base) + f.subdirectory
	}
	return dir
}

func (a *App) cloneGitDialog(c *ui.Context, p colors) {
	f := a.cloneGit
	if f == nil {
		return
	}
	submit := false
	ui.Column(c).Padding(8, 24, 16, 24).Gap(12).Children(func() {
		if f.error != "" {
			ui.Text(c, f.error).FontSize(13).Padding(10, 12).Radius(6).Border(1, p.red.Alpha(.45)).Background(p.red.Alpha(.08)).MaxLines(8).Selectable().FillWidth()
		}
		field := func(label string, value *string, placeholder string, right func()) ui.Element {
			var input ui.Element
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Text(c, label).FontSize(12).TextColor(p.muted)
				ui.Row(c).FillWidth().Gap(2).Children(func() {
					input = ui.TextInput(c, value).Label(label).Placeholder(placeholder).Grow(1)
					if right != nil {
						right()
					}
				})
			})
			if input.Submitted() {
				submit = true
			}
			return input
		}
		field("Repository URL", &f.url, "https://github.com/user/repo.git", nil).AutoFocus()
		directory := f.directory()
		if field("Directory", &directory, defaultProjectDir(), func() {
			if smallIconButton(c, "folder", "Browse").Clicked() {
				a.background(func() (func(), error) {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Select Directory", Directory: true})
					if err != nil || len(paths) == 0 {
						return nil, err
					}
					return func() { f.base, f.hasOverride, f.override = paths[0], false, "" }, nil
				})
			}
		}).Changed() {
			f.override, f.hasOverride = directory, true
		}
		ui.Row(c).Gap(6).Children(func() {
			ui.Checkbox(c, &f.hasSubdirectory, "Workspace is in a subdirectory").FontSize(13)
			icon(c, "info").FontSize(12).TextColor(p.subtle).Tooltip("Enable if the Yaak workspace files are not at the root of the repository")
		})
		if f.hasSubdirectory {
			field("Subdirectory", &f.subdirectory, "path/to/workspace", nil)
		}
	})
	dialogFooter(c, p, nil, func() {
		label := "Clone Repository"
		if f.cloning {
			label = "Cloning..."
		}
		if ui.PrimaryButton(c, label).Disabled(f.url == "" || f.directory() == "" || f.cloning).Clicked() {
			submit = true
		}
	})
	if submit && f.url != "" && f.directory() != "" && !f.cloning {
		a.cloneRepository(f, false)
	}
}

// cloneRepository is Yaak's handleClone with gitClone: credentials asked
// for and stored when the remote wants them, once, then the workspace in
// the clone opened.
func (a *App) cloneRepository(f *cloneGitForm, retried bool) {
	url, dir, workspaceDir := f.url, f.directory(), f.workspaceDirectory()
	f.cloning, f.error = true, ""
	a.background(func() (func(), error) {
		result, err := a.Engine.GitClone(a.ctx, url, dir)
		return func() {
			f.cloning = false
			switch {
			case err != nil:
				f.error = err.Error()
				a.showToast("git-clone-error", "Clone Failed: "+err.Error(), "danger", 0)
			case result.Kind == "needs_credentials" && retried:
				f.error = result.Error
				if f.error == "" {
					f.error = "Authentication failed. Please check your credentials and try again."
				}
			case result.Kind == "needs_credentials":
				a.askCredentials(result.URL, result.Error, func(user, password string) {
					a.background(func() (func(), error) {
						err := a.Engine.GitAddCredential(a.ctx, result.URL, user, password)
						return func() {
							a.cloneGit = f
							a.prompt("clone_git", "Clone Git Repository", "", "")
							a.cloneRepository(f, true)
						}, err
					})
				})
			default:
				a.dialogOpen = false
				a.openSyncDirWorkspace(workspaceDir)
			}
		}, nil
	})
}

// openWorkspaceFolder is Yaak's New Workspace > Open Folder: the workspace
// synced in a folder the user picks.
func (a *App) openWorkspaceFolder() {
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Select Workspace Directory", Directory: true})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() { a.openSyncDirWorkspace(paths[0]) }, nil
	})
}
