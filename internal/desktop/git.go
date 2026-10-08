package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// syncDir is the active workspace's sync directory.
func (a *App) syncDir() string {
	for _, meta := range a.list("workspace_meta") {
		if s(meta, "workspaceId") == a.workspace {
			return s(meta, "settingSyncDir")
		}
	}
	return ""
}

// keepSyncing syncs the active workspace with its directory while it has
// one, as Yaak does: once now, then whenever either side changes.
func (a *App) keepSyncing() {
	dir, workspace := a.syncDir(), a.workspace
	if a.testMode || dir+"\x00"+workspace == a.syncing {
		return
	}
	if a.stopSync != nil {
		a.stopSync()
		a.stopSync = nil
	}
	a.syncing, a.gitInfo, a.gitState, a.gitNoRepo = dir+"\x00"+workspace, nil, nil, false
	if dir == "" {
		return
	}
	a.background(func() (func(), error) {
		if err := a.syncNow(workspace, dir); err != nil {
			return nil, err
		}
		stop, err := a.Engine.WatchSync(a.ctx, workspace, dir, func(err error) { a.Window.Update(func() { a.errorMessage = err.Error() }) })
		return func() {
			if a.syncing == dir+"\x00"+workspace {
				a.stopSync = stop
			} else if stop != nil {
				stop()
			}
			a.refreshGit()
		}, err
	})
}

// syncNow syncs the workspace with its directory, the copy updated last
// winning a conflict.
func (a *App) syncNow(workspace, dir string) error {
	plan, err := a.Engine.PlanSync(a.ctx, workspace, dir)
	if err != nil {
		return err
	}
	return a.Engine.ApplySync(a.ctx, workspace, dir, plan, engine.SyncNewest)
}

// setSyncDir is Yaak's SyncToFilesystemSetting: the workspace syncs with
// the directory chosen, or stops syncing.
func (a *App) setSyncDir(workspace, dir string) {
	var meta engine.Object
	for _, m := range a.list("workspace_meta") {
		if s(m, "workspaceId") == workspace {
			meta = deepCopy(m)
		}
	}
	if meta == nil {
		meta = engine.Object{"model": "workspace_meta", "workspaceId": workspace}
	}
	meta["settingSyncDir"] = nilIfEmpty(dir)
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, meta)
		return func() { a.applyModel(saved) }, err
	})
}

func (a *App) refreshGit() {
	dir := a.syncDir()
	if dir == "" {
		return
	}
	a.gitRefreshed = time.Now()
	a.background(func() (func(), error) {
		info, err := a.Engine.GitBranchInfo(a.ctx, dir)
		state, _ := a.Engine.GitStatus(a.ctx, dir)
		return func() {
			if dir != a.syncDir() {
				return
			}
			a.gitNoRepo = err != nil && strings.Contains(err.Error(), "not found")
			if err == nil {
				a.gitInfo = &info
			} else {
				a.gitInfo = nil
			}
			a.gitState = &state
		}, nil
	})
}

// hiddenSetup reports whether a setup message was hidden for the
// workspace, as Yaak keeps them by key.
func (a *App) hiddenSetup(key string) bool {
	for _, m := range a.list("key_value") {
		if s(m, "namespace") == "global" && s(m, "key") == key {
			var hidden map[string]bool
			_ = json.Unmarshal([]byte(s(m, "value")), &hidden)
			return hidden[a.workspace]
		}
	}
	return false
}

func (a *App) hideSetup(key string) {
	hidden := map[string]bool{}
	for _, m := range a.list("key_value") {
		if s(m, "namespace") == "global" && s(m, "key") == key {
			_ = json.Unmarshal([]byte(s(m, "value")), &hidden)
		}
	}
	hidden[a.workspace] = true
	data, _ := json.Marshal(hidden)
	model := engine.Object{"model": "key_value", "namespace": "global", "key": key, "value": string(data)}
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, model)
		return func() { a.applyModel(saved) }, err
	})
}

// gitMenuButton is Yaak's GitMenuButton, the sidebar's footer.
func gitMenuButton(c *ui.Context, p colors, label string, content func()) ui.Element {
	button := ui.ButtonBase(c).Label(label).Height(32).Shrink(0).Padding(0, 12).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(p.border)
	if button.Hovered() {
		button.Background(p.border.Alpha(.35))
	}
	button.Children(content)
	return button
}

// gitButton is Yaak's GitDropdown, or its setup messages.
func (a *App) gitButton(c *ui.Context, p colors) {
	a.keepSyncing()
	dir := a.syncDir()
	if dir == "" {
		if a.hiddenSetup("setup_sync") {
			return
		}
		gitMenuButton(c, p, "Setup FS Sync or Git", func() {
			icon(c, "wrench").FontSize(13).TextColor(p.subtle)
			ui.Text(c, "Setup FS Sync or Git").FontSize(12).TextColor(p.muted).SingleLine().Grow(1)
		}).Menu(func(m *ui.Menu) {
			m.Item("When enabled, workspace data syncs to the chosen folder as text files, ideal for backup and Git collaboration.").Disabled(true)
			if m.Item("Open Workspace Settings").Chosen() {
				a.prompt("workspace_settings", "Workspace Settings", "", a.workspace)
				a.modalTab = 1
			}
			m.Separator()
			if m.Item("Hide This Message").Chosen() {
				a.confirmDialog("Hide Setup Message", "You can configure filesystem sync or Git it in the workspace settings", "Confirm", false, func() { a.hideSetup("setup_sync") })
			}
		})
		return
	}
	if time.Since(a.gitRefreshed) > 5*time.Second {
		a.refreshGit()
	}
	if a.gitNoRepo {
		if a.hiddenSetup("setup_git_repo") {
			return
		}
		gitMenuButton(c, p, "Setup Git", func() {
			icon(c, "branch").FontSize(13).TextColor(p.subtle)
			ui.Text(c, "Setup Git").FontSize(12).TextColor(p.muted).SingleLine().Grow(1)
		}).Menu(func(m *ui.Menu) {
			m.Item("Initialize local repo to start versioning with Git").Disabled(true)
			if m.Item("Initialize Git Repo").Chosen() {
				a.background(func() (func(), error) { return a.refreshGit, a.Engine.GitInit(dir) })
			}
			m.Separator()
			if m.Item("Hide This Message").Chosen() {
				a.confirmDialog("Hide Git Setup", "You can initialize a git repo outside of Yeek to bring this back", "Confirm", false, func() { a.hideSetup("setup_git_repo") })
			}
		})
		return
	}
	info := a.gitInfo
	if info == nil {
		return // still loading
	}
	gitMenuButton(c, p, "Git: "+info.Head, func() {
		ui.Row(c).Gap(4).Padding(1, 5).Radius(3).Background(p.border.Alpha(.4)).Shrink(1).MinWidth(0).Children(func() {
			icon(c, "branch").FontSize(11).TextColor(p.subtle)
			ui.Text(c, info.Head).Font("monospace").FontSize(11).TextColor(p.muted).SingleLine()
		})
		ui.Spacer(c)
		if info.Ahead > 0 {
			ui.Row(c).Gap(2).Children(func() {
				ui.Text(c, "↗").FontSize(11).TextColor(p.accent)
				ui.Text(c, fmt.Sprint(info.Ahead)).FontSize(11).TextColor(p.muted)
			})
		}
		if info.Behind > 0 {
			ui.Row(c).Gap(2).Children(func() {
				ui.Text(c, "↙").FontSize(11).TextColor(p.blue)
				ui.Text(c, fmt.Sprint(info.Behind)).FontSize(11).TextColor(p.muted)
			})
		}
	}).Menu(func(m *ui.Menu) { a.gitMenu(m, dir, info) })
}

// gitMenu is GitDropdown's items.
func (a *App) gitMenu(m *ui.Menu, dir string, info *engine.GitBranchInfo) {
	current := info.Head
	if m.Item("View History...").Chosen() {
		a.prompt("git_history", "Commit History", "", "")
	}
	if m.Item("Manage Remotes...").Chosen() {
		a.openGitRemotes()
	}
	m.Separator()
	if m.Item("New Branch...").Chosen() {
		a.ask("Create Branch", "", "Create", false, []askInput{{label: "Branch Name"}}, func(v []string) { a.newBranch(v[0], "") })
	}
	m.Separator()
	if m.Item("Push").Chosen() {
		a.gitPush()
	}
	if m.Item("Pull").Chosen() {
		a.gitPull()
	}
	if m.Item("Commit...").Chosen() {
		a.gitCommitMessage = ""
		a.prompt("git_commit", "Commit Changes", "", "")
	}
	if a.gitState != nil && len(a.gitState.Files) > 0 && m.Item("Reset Changes").Chosen() {
		a.confirmDialog("Reset Changes", "This will discard all uncommitted changes. This cannot be undone.", "Reset", true, func() {
			a.gitOp("Error resetting changes", func() (string, error) { return "Changes have been reset", a.Engine.GitResetChanges(a.ctx, dir) })
		})
	}
	if len(info.LocalBranches) > 0 {
		m.Separator()
		m.Item("Branches").Disabled(true)
	}
	for _, branch := range info.LocalBranches {
		here := branch == current
		label := branch
		if here {
			label = "✓ " + branch // a native submenu cannot show a check mark
		}
		m.Submenu(label, func(sub *ui.Menu) {
			if !here && sub.Item("Checkout").Chosen() {
				a.checkout(branch, false)
			}
			if !here && sub.Item("Merge into "+current).Chosen() {
				a.gitOp("Error merging branch", func() (string, error) {
					return "Merged " + branch + " into " + current, a.Engine.GitMergeBranch(a.ctx, dir, branch)
				})
			}
			if sub.Item("New Branch...").Chosen() {
				a.ask("New Branch", "Create a new branch from "+branch, "Create", false, []askInput{{label: "Branch Name"}}, func(v []string) { a.newBranch(v[0], branch) })
			}
			if sub.Item("Rename...").Chosen() {
				a.ask("Rename Branch", "", "Rename", false, []askInput{{label: "New Branch Name", value: branch}}, func(v []string) {
					if name := strings.TrimSpace(v[0]); name != "" && name != branch {
						a.gitOp("Error renaming branch", func() (string, error) {
							return "Renamed " + branch + " to " + name, a.Engine.GitRenameBranch(a.ctx, dir, branch, name)
						})
					}
				})
			}
			if !here {
				sub.Separator()
				if sub.Item("Delete").Chosen() {
					a.confirmDialog("Delete Branch", "Permanently delete "+branch+"?", "Delete", true, func() { a.deleteBranch(branch, false) })
				}
			}
		})
	}
	for _, branch := range info.RemoteBranches {
		if slices.Contains(info.LocalBranches, strings.TrimPrefix(branch, "origin/")) {
			continue
		}
		m.Submenu(branch, func(sub *ui.Menu) {
			if sub.Item("Checkout").Chosen() {
				a.checkout(branch, false)
			}
			if sub.Item("Delete").Chosen() {
				a.confirmDialog("Delete Remote Branch", "Permanently delete "+branch+" from the remote?", "Delete", true, func() {
					a.gitOp("Error deleting remote branch", func() (string, error) {
						return "Deleted remote branch " + branch, a.Engine.GitDeleteRemoteBranch(a.ctx, dir, branch)
					})
				})
			}
		})
	}
}

// gitOp runs a Git operation, then shows how it went, and syncs the
// workspace with what it changed on disk.
func (a *App) gitOp(failure string, op func() (string, error)) {
	workspace, dir := a.workspace, a.syncDir()
	a.background(func() (func(), error) {
		message, err := op()
		if err == nil {
			err = a.syncNow(workspace, dir)
		}
		return func() {
			if err != nil {
				a.showToast("", failure+": "+err.Error(), "danger", 0)
			} else if message != "" {
				a.showToast("", message, "success", 5*time.Second)
			}
			a.refreshGit()
		}, nil
	})
}

func (a *App) checkout(branch string, force bool) {
	dir := a.syncDir()
	workspace := a.workspace
	a.background(func() (func(), error) {
		name, err := a.Engine.GitCheckoutBranch(a.ctx, dir, branch, force)
		if err == nil {
			err = a.syncNow(workspace, dir)
		}
		return func() {
			switch {
			case err != nil && !force:
				a.confirmDialog("Conflicts Detected", "Your branch has conflicts. Either make a commit or force checkout to discard changes.", "Force Checkout", false, func() { a.checkout(branch, true) })
			case err != nil:
				a.showToast("", "Error checking out branch: "+err.Error(), "danger", 0)
			default:
				a.showToast("git-checkout-success", "Switched branch "+name, "success", 5*time.Second)
			}
			a.refreshGit()
		}, nil
	})
}

func (a *App) newBranch(name, base string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	dir := a.syncDir()
	a.background(func() (func(), error) {
		err := a.Engine.GitCreateBranch(a.ctx, dir, name, base)
		return func() {
			if err != nil {
				a.showToast("", "Error creating branch: "+err.Error(), "danger", 0)
				return
			}
			a.checkout(name, false)
		}, nil
	})
}

func (a *App) deleteBranch(branch string, force bool) {
	dir := a.syncDir()
	a.background(func() (func(), error) {
		err := a.Engine.GitDeleteLocalBranch(a.ctx, dir, branch, force)
		return func() {
			switch {
			case errors.Is(err, engine.ErrNotFullyMerged):
				a.confirmDialog("Branch not fully merged", "Branch "+branch+" is not fully merged.\n\nDo you want to delete it anyway?", "Confirm", false, func() { a.deleteBranch(branch, true) })
			case err != nil && force:
				a.showToast("", "Error force deleting branch: "+err.Error(), "danger", 0)
			case err != nil:
				a.showToast("", "Error deleting branch: "+err.Error(), "danger", 0)
			}
			a.refreshGit()
		}, nil
	})
}

func (a *App) gitPush() {
	dir := a.syncDir()
	a.background(func() (func(), error) {
		result, err := a.Engine.GitPush(a.ctx, dir)
		return func() {
			switch {
			case errors.Is(err, engine.ErrNoRemote):
				a.addGitRemote(a.gitPush)
			case err != nil:
				a.showToast("", "Error pushing changes: "+err.Error(), "danger", 0)
			default:
				a.gitResult(result, a.gitPush)
			}
			a.refreshGit()
		}, nil
	})
}

func (a *App) gitPull() {
	dir, workspace := a.syncDir(), a.workspace
	a.background(func() (func(), error) {
		result, err := a.Engine.GitPull(a.ctx, dir)
		if err == nil && result.Kind == "success" {
			err = a.syncNow(workspace, dir)
		}
		return func() {
			switch {
			case errors.Is(err, engine.ErrNoRemote):
				a.addGitRemote(a.gitPull)
			case err != nil:
				a.showToast("", "Error pulling changes: "+err.Error(), "danger", 0)
			default:
				a.gitResult(result, a.gitPull)
			}
			a.refreshGit()
		}, nil
	})
}

// gitResult is Yaak's handling of a push or pull: credentials are asked
// for, diverged branches resolved and uncommitted changes reset, then it
// tries again.
func (a *App) gitResult(r engine.GitResult, retry func()) {
	dir := a.syncDir()
	switch r.Kind {
	case "success":
		a.showToast("", r.Message, "success", 5*time.Second)
	case "up_to_date":
		a.showToast("", "Already up-to-date", "info", 5*time.Second)
	case "needs_credentials":
		a.askCredentials(r.URL, r.Error, func(user, password string) {
			a.background(func() (func(), error) {
				return retry, a.Engine.GitAddCredential(a.ctx, r.URL, user, password)
			})
		})
	case "diverged":
		a.diverged = &r
		a.divergedChoice = ""
		a.prompt("git_diverged", "Branches Diverged", "", "")
	case "uncommitted_changes":
		a.confirmDialog("Uncommitted Changes", "You have uncommitted changes. Commit or reset your changes before pulling.", "Reset and Pull", true, func() {
			a.background(func() (func(), error) { return retry, a.Engine.GitResetChanges(a.ctx, dir) })
		})
	}
}

var githubRemote = regexp.MustCompile(`(?i)github\.com`)

// askCredentials is Yaak's promptCredentials.
func (a *App) askCredentials(remote, failure string, done func(user, password string)) {
	user, pass := askInput{label: "Username"}, askInput{label: "Password / Token", description: "Enter your password or access token for this Git server.", password: true}
	if githubRemote.MatchString(remote) {
		user = askInput{label: "GitHub Username", description: "Use your GitHub username (not your email)."}
		pass = askInput{label: "GitHub Personal Access Token", description: "GitHub requires a Personal Access Token (PAT) for write operations over HTTPS. Passwords are not supported.", password: true}
	}
	description := "Enter credentials for " + remote
	if failure != "" {
		description = failure
	}
	a.ask("Credentials Required", description, "Submit", false, []askInput{user, pass}, func(v []string) { done(v[0], v[1]) })
}

// addGitRemote asks for the origin to push to or pull from, then retries.
func (a *App) addGitRemote(retry func()) {
	dir := a.syncDir()
	a.ask("Add Remote", "", "Add", false, []askInput{{label: "Name", value: "origin"}, {label: "URL"}}, func(v []string) {
		a.background(func() (func(), error) {
			err := a.Engine.GitRemote(dir, strings.TrimSpace(v[0]), strings.TrimSpace(v[1]), false)
			return func() {
				if retry != nil {
					retry()
				}
				a.loadGitRemotes()
			}, err
		})
	})
}

func (a *App) openGitRemotes() {
	a.gitRemotes = nil
	a.loadGitRemotes()
	a.prompt("git_remotes", "Manage Remotes", "", "")
}

func (a *App) loadGitRemotes() {
	dir := a.syncDir()
	a.background(func() (func(), error) {
		remotes, err := a.Engine.GitRemotes(a.ctx, dir)
		return func() { a.gitRemotes = remotes }, err
	})
}

// gitRemotesDialog is Yaak's GitRemotesDialog.
func (a *App) gitRemotesDialog(c *ui.Context, p colors) {
	dir := a.syncDir()
	ui.Column(c).Padding(16, 20).Gap(10).Children(func() {
		for _, remote := range a.gitRemotes {
			ui.Row(c).Key(remote.Name).Gap(10).Padding(6, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, remote.Name).FontWeight(600).FontSize(13).Width(90)
				ui.Text(c, remote.URL).Font("monospace").FontSize(12).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
				if smallIconButton(c, "trash", "Remove "+remote.Name).Clicked() {
					name := remote.Name
					a.background(func() (func(), error) { return a.loadGitRemotes, a.Engine.GitRemote(dir, name, "", true) })
				}
			})
		}
		if len(a.gitRemotes) == 0 {
			ui.Text(c, "No remotes").FontSize(13).TextColor(p.muted)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c, "Add Remote").Clicked() {
				a.addGitRemote(func() { a.openGitRemotes() })
			}
		})
	})
}

// gitHistoryDialog is Yaak's HistoryDialog.
func (a *App) gitHistoryDialog(c *ui.Context, p colors) {
	ui.Column(c).Height(420).Padding(8, 20, 12, 20).Children(func() {
		ui.Row(c).Height(30).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, "Message").FontSize(12).FontWeight(600).Grow(1)
			ui.Text(c, "Author").FontSize(12).FontWeight(600).Width(140)
			ui.Text(c, "When").FontSize(12).FontWeight(600).Width(110)
		})
		ui.Scroll(c).Grow(1).Padding(0, 12, 0, 0).Children(func() {
			if a.gitState == nil {
				return
			}
			for _, commit := range a.gitState.Commits {
				ui.Row(c).Key(commit.Hash).Height(30).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(p.border.Alpha(.5)).Children(func() {
					if commit.Message == "" {
						ui.Text(c, "No message").Italic().FontSize(12).TextColor(p.muted).Grow(1)
					} else {
						ui.Text(c, commit.Message).FontSize(12).SingleLine().Grow(1).MinWidth(0)
					}
					author := commit.Author
					if author == "" {
						author = "Unknown"
					}
					ui.Text(c, author).FontSize(12).SingleLine().Width(140).Tooltip("Email: " + commit.Email)
					ui.Text(c, timeAgo(commit.When)+" ago").FontSize(12).TextColor(p.muted).Width(110).Tooltip(commit.When.Format(time.RFC1123))
				})
			}
		})
	})
}

// timeAgo is date-fns' formatDistanceToNowStrict.
func timeAgo(t time.Time) string {
	d := time.Since(t)
	unit := func(n int, word string) string {
		if n == 1 {
			return "1 " + word
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	switch {
	case d < time.Minute:
		return unit(int(d.Seconds()), "second")
	case d < time.Hour:
		return unit(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return unit(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return unit(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return unit(int(d.Hours()/24/30), "month")
	}
	return unit(int(d.Hours()/24/365), "year")
}

// gitCommitDialog is Yaak's GitCommitDialog: the changes to stage, the
// message, and Commit or Commit and Push.
func (a *App) gitCommitDialog(c *ui.Context, p colors) {
	dir, workspace := a.syncDir(), a.workspace
	ui.Column(c).Height(520).Padding(12, 20, 16, 20).Gap(12).Children(func() {
		if a.gitState == nil || len(a.gitState.Files) == 0 {
			emptyState(c, p, "No changes since last commit")
			return
		}
		ui.Scroll(c).Grow(1).Padding(0, 12, 0, 0).Gap(2).Children(func() {
			for _, file := range a.gitState.Files {
				ui.Row(c).Key(file.Path).Height(26).Gap(8).Children(func() {
					staged := file.Staging != " " && file.Staging != "?"
					title := "Stage change"
					if staged {
						title = "Unstage change"
					}
					if ui.Checkbox(c, &staged, "").Label(title + " " + file.Path).Changed() {
						path := file.Path
						a.background(func() (func(), error) { return a.refreshGit, a.Engine.GitStage(dir, []string{path}, staged) })
					}
					status, color := gitFileStatus(file, p)
					ui.Text(c, a.gitFileName(file.Path)).FontSize(12).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, status).FontSize(11).TextColor(color)
				})
			}
		})
		ui.TextArea(c, &a.gitCommitMessage).Label("Commit message").Placeholder("Commit message...").Height(90).FillWidth()
		ui.Row(c).Gap(8).Children(func() {
			if a.gitInfo != nil {
				ui.Text(c, a.gitInfo.Head).Font("monospace").FontSize(11).Padding(1, 5).Radius(3).Background(p.border.Alpha(.4))
			}
			ui.Spacer(c)
			staged := slices.ContainsFunc(a.gitState.Files, func(f engine.GitFile) bool { return f.Staging != " " && f.Staging != "?" })
			ready := staged && strings.TrimSpace(a.gitCommitMessage) != ""
			commit := func(push bool) {
				message := a.gitCommitMessage
				a.background(func() (func(), error) {
					err := a.Engine.GitCommitStaged(a.ctx, dir, message)
					if err == nil {
						err = a.syncNow(workspace, dir)
					}
					return func() {
						if err != nil {
							a.showToast("", "Error committing: "+err.Error(), "danger", 0)
							return
						}
						a.dialogOpen = false
						a.refreshGit()
						if push {
							a.gitPush()
						}
					}, nil
				})
			}
			if ui.Button(c, "Commit").Disabled(!ready).Clicked() {
				commit(false)
			}
			if ui.PrimaryButton(c, "Commit and Push").Disabled(!ready).Clicked() {
				commit(true)
			}
		})
	})
}

// gitFileName names a synced file by its model, as Yaak's commit tree does.
func (a *App) gitFileName(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	if id := strings.TrimSuffix(strings.TrimPrefix(base, "yaak."), ".yaml"); id != base {
		if m := a.models[id]; m != nil {
			return requestName(m)
		}
	}
	return path
}

func gitFileStatus(file engine.GitFile, p colors) (string, ui.Color) {
	code := file.Worktree
	if code == " " {
		code = file.Staging
	}
	switch code {
	case "?", "A":
		return "Added", p.green
	case "D":
		return "Removed", p.red
	case "R":
		return "Renamed", p.blue
	}
	return "Modified", p.notice
}

// gitDivergedDialog is Yaak's DivergedDialog.
func (a *App) gitDivergedDialog(c *ui.Context, p colors) {
	r := a.diverged
	if r == nil {
		return
	}
	dir, workspace := a.syncDir(), a.workspace
	ui.Column(c).Padding(20).Gap(14).Children(func() {
		ui.Text(c, "Your local branch has diverged from "+r.Remote+"/"+r.Branch+". How would you like to resolve this?").FontSize(13).MaxLines(4)
		for _, option := range []struct{ value, label, description string }{
			{"merge", "Merge Commit", "Combining local and remote changes into a single merge commit"},
			{"force_reset", "Force Pull", "Discard local commits and reset to match the remote branch"},
		} {
			card := ui.ButtonBase(c).Key(option.value).Label(option.label).Padding(10, 12).Gap(4).Radius(6).Border(1, p.border).FillWidth().Column()
			if a.divergedChoice == option.value {
				card.Border(1, p.accent).Background(p.accent.Alpha(.08))
			}
			card.Children(func() {
				ui.Text(c, option.label).FontSize(13).FontWeight(600)
				ui.Text(c, option.description).FontSize(12).TextColor(p.muted)
			})
			if card.Clicked() {
				a.divergedChoice = option.value
			}
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen, a.diverged = false, nil
			}
			label := map[string]string{"": "Select an option", "merge": "Merge", "force_reset": "Force Pull"}[a.divergedChoice]
			resolve := ui.PrimaryButton(c, label).Disabled(a.divergedChoice == "")
			if a.divergedChoice == "force_reset" {
				resolve.Background(p.red)
			}
			if resolve.Clicked() {
				choice, remote, branch := a.divergedChoice, r.Remote, r.Branch
				a.dialogOpen, a.diverged = false, nil
				a.background(func() (func(), error) {
					var result engine.GitResult
					var err error
					if choice == "force_reset" {
						result, err = a.Engine.GitPullForceReset(a.ctx, dir, remote, branch)
					} else {
						result, err = a.Engine.GitPullMerge(a.ctx, dir, remote, branch)
					}
					if err == nil {
						err = a.syncNow(workspace, dir)
					}
					return func() {
						if err != nil {
							a.showToast("", "Error pulling changes: "+err.Error(), "danger", 0)
						} else {
							a.gitResult(result, nil)
						}
						a.refreshGit()
					}, nil
				})
			}
		})
	})
}
