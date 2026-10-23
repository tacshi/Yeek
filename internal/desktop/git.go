package desktop

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/yeekui"
	"github.com/egoist/mygo/yeekui/editor"
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
	a.reviewSync(workspace, dir, 1)
	a.background(func() (func(), error) {
		changed := func() { a.Window.Update(func() { a.reviewSync(workspace, dir, 1) }) }
		stop, err := a.Engine.WatchSync(a.ctx, workspace, dir, changed, func(err error) { a.Window.Update(func() { a.errorMessage = err.Error() }) })
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

// maxSyncAttempts is Yaak's MAX_SYNC_ATTEMPTS: files can keep changing
// under a plan, so stop planning again after a few stale applies.
const maxSyncAttempts = 3

// syncNow syncs the workspace with its directory without asking, the copy
// updated last winning a conflict, as Yaak's sync({ force: true }) does
// after Git changed the files.
func (a *App) syncNow(workspace, dir string) error {
	for attempt := 1; ; attempt++ {
		plan, err := a.Engine.PlanSync(a.ctx, workspace, dir)
		if err != nil {
			return err
		}
		err = a.Engine.ApplySync(a.ctx, workspace, dir, plan, engine.SyncNewest)
		if !errors.Is(err, engine.ErrSyncStale) || attempt == maxSyncAttempts {
			return err
		}
	}
}

// syncReview is the plan the Changes Detected dialog shows.
type syncReview struct {
	workspace, dir string
	plan           []engine.SyncChange
	attempt        int
	// shown is false while another dialog is open; the review waits for it.
	shown bool
}

// incomingSyncChanges are the changes of a plan that change the workspace
// from its directory, Yaak's db ops.
func incomingSyncChanges(plan []engine.SyncChange) []engine.SyncChange {
	var incoming []engine.SyncChange
	for _, c := range plan {
		direction := c.Direction
		if c.Conflict {
			// As engine.SyncNewest resolves it: the copy updated last wins.
			direction = "push"
			if s(c.Remote, "updatedAt") > s(c.Local, "updatedAt") {
				direction = "pull"
			}
		}
		if direction == "pull" {
			incoming = append(incoming, c)
		}
	}
	return incoming
}

// reviewSync is Yaak's syncWorkspace: changes going out to the directory
// apply at once, and changes coming in from it are shown in the Changes
// Detected dialog first.
func (a *App) reviewSync(workspace, dir string, attempt int) {
	a.background(func() (func(), error) {
		plan, err := a.Engine.PlanSync(a.ctx, workspace, dir)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(plan, func(c engine.SyncChange) bool { return c.Direction != "none" || c.Conflict }) {
			return a.hideSyncReview, nil
		}
		if len(incomingSyncChanges(plan)) == 0 {
			return a.applySync(workspace, dir, plan, attempt)
		}
		return func() {
			if attempt > 1 {
				a.showStaleSyncToast()
			}
			a.syncReview = &syncReview{workspace: workspace, dir: dir, plan: plan, attempt: attempt}
			a.showSyncReview()
		}, nil
	})
}

// applySync applies a plan, planning (and asking) again when it went
// stale, up to maxSyncAttempts times.
func (a *App) applySync(workspace, dir string, plan []engine.SyncChange, attempt int) (func(), error) {
	err := a.Engine.ApplySync(a.ctx, workspace, dir, plan, engine.SyncNewest)
	switch {
	case errors.Is(err, engine.ErrSyncStale) && attempt < maxSyncAttempts:
		return func() { a.reviewSync(workspace, dir, attempt+1) }, nil
	case errors.Is(err, engine.ErrSyncStale):
		return func() {
			a.hideSyncReview()
			a.showStaleSyncToast()
		}, nil
	case err != nil:
		return nil, err
	}
	return a.hideSyncReview, nil
}

func (a *App) showStaleSyncToast() {
	a.showToast("sync-stale", engine.ErrSyncStale.Error(), "warning", 5*time.Second)
}

// showSyncReview opens the Changes Detected dialog for the review waiting,
// unless another dialog is open.
func (a *App) showSyncReview() {
	r := a.syncReview
	if r == nil || r.shown || a.dialogOpen && a.dialog != "commit_sync" {
		return
	}
	r.shown = true
	a.prompt("commit_sync", "Changes Detected", "", "")
}

// hideSyncReview closes the Changes Detected dialog, as Yaak's
// hideDialog("commit-sync") does.
func (a *App) hideSyncReview() {
	a.syncReview = nil
	if a.dialog == "commit_sync" {
		a.dialogOpen = false
	}
}

// syncReviewDialog is the Changes Detected dialog of Yaak's syncWorkspace.
func (a *App) syncReviewDialog(c *ui.Context, p colors) {
	r := a.syncReview
	if r == nil {
		return
	}
	incoming := incomingSyncChanges(r.plan)
	deletingWorkspace := slices.ContainsFunc(incoming, func(c engine.SyncChange) bool {
		return c.Remote == nil && s(c.Local, "model") == "workspace"
	})
	ui.Column(c).Padding(20).Gap(12).MaxHeight(560).Children(func() {
		if deletingWorkspace {
			ui.Row(c).Padding(10, 12).Radius(6).Border(1, p.red.Alpha(.35)).Background(p.red.Alpha(.08)).Children(func() {
				ui.Text(c, "🚨 Changes contain a workspace deletion!").FontWeight(600).TextColor(p.red)
			})
		}
		verb := "have"
		if len(incoming) == 1 {
			verb = "has"
		}
		ui.Text(c, pluralizeCount("file", len(incoming))+" in the directory "+verb+" changed. Do you want to update your workspace?").MaxLines(3)
		ui.Column(c).Gap(0).Margin(4, 0).Shrink(1).MinHeight(0).Children(func() {
			ui.Row(c).Padding(8, 0).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, "Type").TextColor(p.muted).Width(130).SingleLine()
				ui.Text(c, "Name").TextColor(p.muted).Grow(1).SingleLine()
				ui.Text(c, "Operation").TextColor(p.muted).SingleLine()
			})
			ui.Scroll(c).Shrink(1).MinHeight(0).Children(func() {
				for _, change := range incoming {
					model, label, color := change.Remote, "update", p.blue
					switch {
					case change.Local == nil:
						label, color = "create", p.green
					case change.Remote == nil:
						model, label, color = change.Local, "delete", p.red
					}
					ui.Row(c).Key(change.ID).Padding(6, 0).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border.Alpha(.5)).Children(func() {
						ui.Text(c, modelTypeLabel(model)).TextColor(p.muted).Width(130).SingleLine()
						name := a.syncModelName(model)
						ui.Text(c, name).Grow(1).Shrink(1).MinWidth(0).SingleLine().Tooltip(name)
						ui.Text(c, label).Font("monospace").FontSize(12).Padding(1, 4).Radius(3).Background(p.border.Alpha(.4)).TextColor(color)
					})
				}
			})
		})
		ui.Row(c).Justify(ui.End).Gap(8).Padding(4, 0, 0, 0).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.syncReview, a.dialogOpen = nil, false
			}
			if ui.PrimaryButton(c, "Apply Changes").AutoFocus().Clicked() {
				workspace, dir, plan, attempt := r.workspace, r.dir, r.plan, r.attempt
				a.background(func() (func(), error) { return a.applySync(workspace, dir, plan, attempt) })
			}
		})
	})
}

// syncModelName is Yaak's resolvedModelNameWithFolders: the model's name
// after those of the folders it is in.
func (a *App) syncModelName(m engine.Object) string {
	names := []string{requestName(m)}
	seen := map[string]bool{}
	for id := s(m, "folderId"); id != "" && !seen[id]; {
		seen[id] = true
		folder := a.models[id]
		if folder == nil {
			break
		}
		names = append([]string{requestName(folder)}, names...)
		id = s(folder, "folderId")
	}
	return strings.Join(names, " / ")
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
		a.gitCommitMessage, a.commitCollapsed = "", nil
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

// gitCommitDialog is Yaak's GitCommitDialog: the changes to stage over
// the message, and Commit or Commit and Push, beside the diff of the
// change selected.
func (a *App) gitCommitDialog(c *ui.Context, p colors) {
	dir, workspace := a.syncDir(), a.workspace
	if a.gitState == nil {
		return
	}
	tree, external := a.commitTree()
	if tree == nil {
		return
	}
	if len(a.gitState.Files) == 0 {
		ui.Column(c).Grow(1).Padding(12, 20, 16, 20).Children(func() { emptyState(c, p, "No changes since last commit") })
		return
	}
	// Yaak's split layouts: the diff takes 60% of the width, the message
	// 35% of the height beside it.
	w, h := c.Size()
	if a.gitCommitSplit == 0 {
		a.gitCommitSplit = w * .9 * .4
	}
	if a.gitCommitMessageSplit == 0 {
		a.gitCommitMessageSplit = h * .88 * .65
	}
	changes := func() {
		ui.Scroll(c).Fill().Padding(0, 2, 12, 0).Children(func() {
			a.commitTreeView(c, p, dir, tree, 0)
			if len(external) > 0 {
				// Yaak's Separator with a label.
				ui.Row(c).Gap(8).Padding(12, 0, 4, 0).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Height(1).Grow(1).Background(p.border)
					ui.Text(c, "Other files").FontSize(12).TextColor(p.subtle)
					ui.Box(c).Height(1).Grow(1).Background(p.border)
				})
				for _, entry := range external {
					a.externalCommitRow(c, p, dir, entry)
				}
			}
		})
	}
	message := func() {
		ui.Column(c).Fill().Padding(12, 0, 8, 0).Gap(12).Children(func() {
			ui.TextArea(c, &a.gitCommitMessage).Label("Commit message").Placeholder("Commit message...").Grow(1).MinHeight(0).FillWidth()
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
	ui.Column(c).Grow(1).MinHeight(0).Padding(12, 8, 16, 8).Children(func() {
		splitQuiet(c, &a.gitCommitSplit, false, func() {
			ui.Column(c).Fill().Padding(0, 16).Children(func() {
				splitQuiet(c, &a.gitCommitMessageSplit, true, changes, message).Fill()
			})
		}, func() {
			ui.Column(c).Fill().Padding(0, 16).BorderWidth(0, 0, 0, 1).BorderColor(p.border).Children(func() {
				a.gitDiffPanel(c, p, dir, workspace)
			})
		}).Fill()
	})
}

// gitEntry is Yaak's GitStatusEntry: a file, its status, whether it is
// staged, and the model it holds, or held.
type gitEntry struct {
	path   string
	status string // untracked, conflict, current, modified, removed, renamed or type_change
	staged bool
	model  engine.Object
}

// gitEntryOf is Yaak's git_status_from_raw: the index's status when the
// change is staged, else the worktree's.
func gitEntryOf(f engine.GitFile) gitEntry {
	if f.Staging == "U" || f.Worktree == "U" {
		return gitEntry{path: f.Path, status: "conflict", staged: true, model: f.Model}
	}
	code := func(c string) string {
		switch c {
		case "?", "A":
			return "untracked"
		case "M", "C":
			return "modified"
		case "D":
			return "removed"
		case "R":
			return "renamed"
		}
		return "current"
	}
	index, worktree := code(f.Staging), code(f.Worktree)
	if f.Staging == "?" {
		index = "current"
	}
	if index != "current" {
		return gitEntry{path: f.Path, status: index, staged: true, model: f.Model}
	}
	return gitEntry{path: f.Path, status: worktree, model: f.Model}
}

// commitNode is Yaak's CommitTreeNode: a model's entry and those of the
// models in it.
type commitNode struct {
	entry    gitEntry
	children []*commitNode
}

// commitTree is the tree of Yaak's commit dialog, from the workspace
// down, and the changed files holding no model: the changes, among the
// models they are in, which are current where unchanged.
func (a *App) commitTree() (*commitNode, []gitEntry) {
	var entries, external []gitEntry
	changed := map[string]bool{}
	for _, f := range a.gitState.Files {
		entry := gitEntryOf(f)
		if entry.model == nil {
			external = append(external, entry)
			continue
		}
		changed[s(entry.model, "id")] = true
		entries = append(entries, entry)
	}
	// The unchanged models changes can be in.
	for id, m := range a.models {
		if kind := s(m, "model"); !changed[id] && (kind == "workspace" && id == a.workspace || kind == "folder" && s(m, "workspaceId") == a.workspace) {
			entries = append(entries, gitEntry{status: "current", model: m})
		}
	}
	// In the order of their files, as git's status lists them.
	slices.SortFunc(entries, func(x, y gitEntry) int { return strings.Compare(s(x.model, "id"), s(y.model, "id")) })
	var next func(m engine.Object, depth int) *commitNode
	next = func(m engine.Object, depth int) *commitNode {
		i := slices.IndexFunc(entries, func(e gitEntry) bool { return s(e.model, "id") == s(m, "id") })
		if i < 0 || depth > 64 {
			return nil
		}
		node := &commitNode{entry: entries[i]}
		for _, e := range entries {
			child := e.model
			parent := s(child, "folderId")
			if parent == "" {
				parent = s(child, "workspaceId")
			}
			if parent == s(m, "id") {
				if n := next(child, depth+1); n != nil {
					node.children = append(node.children, n)
				}
			}
		}
		return node
	}
	ws := a.models[a.workspace]
	if ws == nil {
		return nil, external
	}
	return next(ws, 0), external
}

// checkedStatus is Yaak's nodeCheckedStatus: checked when every change
// below is staged, mixed when some are.
func (n *commitNode) checkedStatus() (checked, mixed bool) {
	var visited, staged, current int
	var visit func(n *commitNode)
	visit = func(n *commitNode) {
		visited++
		switch {
		case n.entry.status == "current":
			current++
		case n.entry.staged:
			staged++
		}
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(n)
	if visited == staged+current {
		return true, false
	}
	return false, staged > 0
}

// relevant reports whether the node or one below it is changed: Yaak's
// tree shows only those.
func (n *commitNode) relevant() bool {
	return n.entry.status != "current" || slices.ContainsFunc(n.children, (*commitNode).relevant)
}

// commitTreeView is Yaak's CheckboxTree of the commit dialog.
func (a *App) commitTreeView(c *ui.Context, p colors, dir string, n *commitNode, depth int) {
	if !n.relevant() {
		return
	}
	key := s(n.entry.model, "id")
	collapsed := a.commitCollapsed[key]
	visibleChildren := slices.ContainsFunc(n.children, (*commitNode).relevant)
	selectable := n.entry.status != "current"
	selected := selectable && a.gitDiff != nil && a.gitDiff.path == n.entry.path
	name := requestName(n.entry.model)
	body := func() {
		row := ui.Row(c).Height(28).Gap(4).AlignItems(ui.Center).FillWidth().Radius(3)
		if selected {
			row.Background(p.border.Alpha(.45))
		}
		row.Children(func() {
			if visibleChildren {
				glyph, label := "down", "Collapse"
				if collapsed {
					glyph, label = "chevron", "Expand"
				}
				toggle := ui.ButtonBase(c).Label(label+" "+name).Size(16, 16).Shrink(0).Center()
				toggle.Children(func() {
					color := p.subtle
					if toggle.Hovered() {
						color = p.text
					}
					icon(c, glyph).FontSize(12).TextColor(color)
				})
				if toggle.Clicked() {
					if a.commitCollapsed == nil {
						a.commitCollapsed = map[string]bool{}
					}
					a.commitCollapsed[key] = !collapsed
				}
			} else {
				ui.Box(c).Width(16).Shrink(0)
			}
			checked, mixed := n.checkedStatus()
			title := "Stage change"
			if checked || mixed {
				title = "Unstage change"
			}
			on := checked
			if checkbox(c, p, &on, mixed, title+" "+name).Margin(0, 2, 0, 0).Changed() {
				a.stageCommitNode(dir, n, mixed || !checked)
			}
			content := func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(4).Padding(2, 4).AlignItems(ui.Center).Children(func() {
					textColor := p.muted
					if selected {
						textColor = p.text
					}
					switch s(n.entry.model, "model") {
					case "folder":
						icon(c, "folder").TextColor(p.muted).Shrink(0)
					case "environment":
						icon(c, "variable").TextColor(p.muted).Shrink(0)
					case "workspace":
						icon(c, "house").TextColor(p.muted).Shrink(0)
					default:
						ui.Box(c).Width(16).Shrink(0)
					}
					ui.Text(c, name).FontSize(13).TextColor(textColor).SingleLine().Grow(1).MinWidth(0)
					if n.entry.status != "current" {
						statusChip(c, p, n.entry.status)
					}
				})
			}
			if selectable {
				button := ui.ButtonBase(c).Label(name).Grow(1).MinWidth(0)
				button.Children(content)
				if button.Clicked() {
					if selected {
						a.gitDiff = nil
					} else {
						a.gitDiff = &gitDiffPanel{path: n.entry.path}
					}
				}
			} else {
				content()
			}
		})
		if !collapsed {
			for _, child := range n.children {
				a.commitTreeView(c, p, dir, child, depth+1)
			}
		}
	}
	if depth == 0 {
		ui.Column(c).Key(key).FillWidth().Children(body)
		return
	}
	// Yaak's pl-4 ml-2 and dashed left border.
	ui.Row(c).Key(key).FillWidth().Children(func() {
		ui.Box(c).Width(8).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 16).BorderWidth(0, 0, 0, 1).BorderStyle(ui.BorderDashed).BorderColor(p.border).Children(body)
	})
}

// stageCommitNode is Yaak's setCheckedAndChildren: it stages, or
// unstages, the changes of the node and those below it.
func (a *App) stageCommitNode(dir string, n *commitNode, staged bool) {
	var paths []string
	var visit func(n *commitNode)
	visit = func(n *commitNode) {
		for _, c := range n.children {
			visit(c)
		}
		if n.entry.status != "current" && n.entry.staged != staged {
			paths = append(paths, n.entry.path)
		}
	}
	visit(n)
	if len(paths) > 0 {
		a.background(func() (func(), error) { return a.refreshGit, a.Engine.GitStage(dir, paths, staged) })
	}
}

// externalCommitRow is Yaak's ExternalTreeNode: a changed file holding
// no model, by its path in the sync directory.
func (a *App) externalCommitRow(c *ui.Context, p colors, dir string, entry gitEntry) {
	display := entry.path
	if rel := a.gitState.RelaDir; rel != "" {
		display = strings.TrimPrefix(display, rel+"/")
	}
	row := ui.Row(c).Key(entry.path).Height(28).Gap(8).Padding(0, 4).Radius(3).AlignItems(ui.Center).FillWidth()
	if row.Hovered() {
		row.Background(p.border.Alpha(.3))
	}
	row.Children(func() {
		staged := entry.staged
		title := "Stage change"
		if staged {
			title = "Unstage change"
		}
		if checkbox(c, p, &staged, false, title+" "+display).Changed() {
			path := entry.path
			a.background(func() (func(), error) { return a.refreshGit, a.Engine.GitStage(dir, []string{path}, staged) })
		}
		icon(c, "file_code").TextColor(p.muted).Shrink(0)
		ui.Text(c, display).FontSize(13).SingleLine().Grow(1).MinWidth(0)
		statusChip(c, p, entry.status)
	})
}

// statusChip is Yaak's Chip of a change's status, in its statusColor.
func statusChip(c *ui.Context, p colors, status string) ui.Element {
	color := p.muted
	switch status {
	case "modified":
		color = p.blue
	case "untracked":
		color = p.green
	case "removed":
		color = p.red
	case "conflict":
		color = p.orange
	}
	chip := ui.Row(c).Padding(2, 8).Radius(6).Background(color.Alpha(.1)).Border(1, color.Alpha(.1)).Shrink(0)
	chip.Children(func() { ui.Text(c, status).FontSize(11).FontWeight(500).TextColor(color).SingleLine() })
	return chip
}

// gitDiffPanel is the change the commit dialog's diff panel shows: its
// model at HEAD and now, as YAML, loaded for the status the dialog shows.
type gitDiffPanel struct {
	path string
	// loaded is the status the texts were loaded for; another reloads them.
	loaded *engine.GitStatus
	diff   *editor.DiffState
}

// gitDiffPanel is Yaak's DiffPanel: the change selected, Discard Changes,
// and its DiffViewer.
func (a *App) gitDiffPanel(c *ui.Context, p colors, dir, workspace string) {
	d := a.gitDiff
	var file *gitEntry
	if d != nil {
		if i := slices.IndexFunc(a.gitState.Files, func(f engine.GitFile) bool { return f.Path == d.path && f.Model != nil }); i >= 0 {
			entry := gitEntryOf(a.gitState.Files[i])
			file = &entry
		}
	}
	if file == nil {
		a.gitDiff = nil
		emptyState(c, p, "Select a change to view diff")
		return
	}
	if d.loaded != a.gitState {
		d.loaded = a.gitState
		path := d.path
		a.background(func() (func(), error) {
			prev, next, err := a.Engine.GitFileVersions(dir, path)
			return func() {
				if a.gitDiff != d {
					return
				}
				d.diff = setYAMLDiff(d.diff, prev, next)
			}, err
		})
	}
	ui.Column(c).Fill().Gap(8).Children(func() {
		ui.Row(c).Gap(8).Padding(0, 4).Children(func() {
			ui.Text(c, requestName(file.model)+" ("+file.status+")").FontSize(12).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
			if warningButton(c, p, "Discard Changes").Clicked() {
				a.discardGitChange(dir, workspace, file.path)
			}
		})
		if d.diff == nil {
			ui.Box(c).Grow(1)
			return
		}
		a.gitDiffView(c, p, d.diff)
	})
}

// setYAMLDiff shows prev and next in d, or in a new diff of YAML when d
// is nil, keeping where it is scrolled to when only the texts change.
func setYAMLDiff(d *editor.DiffState, prev, next string) *editor.DiffState {
	if d == nil {
		d = &editor.DiffState{Old: editor.NewBuffer(""), New: editor.NewBuffer("")}
		lang := editor.LanguageNamed("YAML")
		d.Old.SetLanguage(lang)
		d.New.SetLanguage(lang)
	}
	for _, side := range []struct {
		buf  *editor.Buffer
		text string
	}{{d.Old, prev}, {d.New, next}} {
		if side.buf.String() != side.text {
			side.buf.Reset(side.text)
		}
	}
	return d
}

// gitDiffView is Yaak's DiffViewer, CodeMirror's MergeView of the two
// texts side by side: YAML, wrapped, read only, line numbers, unchanged
// lines collapsed but for 2 around changes, and changed lines tinted with
// no highlight of the words changed in them.
func (a *App) gitDiffView(c *ui.Context, p colors, d *editor.DiffState) {
	font := cmp.Or(s(a.settings, "editorFont"), "monospace")
	size := float32(n(a.settings, "editorFontSize"))
	if size <= 0 {
		size = 12
	}
	d.Font = ui.Font{Family: font, Size: size}
	d.Wrap, d.ContextLines, d.Inline = true, 2, editor.DiffInlineNone
	d.Theme = diffTheme(p)
	d.RenderSeparator = func(c *ui.Context, sep ui.Element, s *editor.DiffSeparator) {
		// Yaak's .cm-collapsedLines: a bordered bar on the surface, which a
		// click expands.
		sep.Padding(4, 2)
		sep.Children(func() {
			bar := ui.ButtonBase(c).Grow(1).AlignSelf(ui.Stretch).Center().Radius(3).Background(p.sidebar).Border(1, p.border).
				Label(fmt.Sprintf("Expand %d unchanged lines", s.Lines)).Opacity(.8)
			if bar.Hovered() {
				bar.Opacity(1)
			}
			bar.Children(func() {
				label := fmt.Sprintf("%d unchanged lines", s.Lines)
				if s.Lines == 1 {
					label = "1 unchanged line"
				}
				ui.Text(c, "⦚ "+label+" ⦚").FontSize(size - 1).TextColor(p.text)
			})
			if bar.Clicked() {
				s.ExpandAll()
			}
		})
	}
	editor.DiffView(c, d).Grow(1).MinHeight(0).FillWidth()
}

// diffTheme colors a diff as Yaak's editors: its syntaxHighlightStyle,
// and changes in the theme's success and danger colors. Plain YAML values,
// which the tokenizer makes variables, keep the text's color, as Yaak's
// YAML content does.
func diffTheme(p colors) *editor.Theme {
	t := &editor.Theme{Background: p.background, Text: p.text, LineNumber: p.subtle, ActiveLineNumber: p.muted, Added: p.green, Deleted: p.red, Modified: p.blue}
	tok := func(color ui.Color, kinds ...editor.TokenKind) {
		for _, k := range kinds {
			t.Tokens[k].Color = color
		}
	}
	tok(p.subtle, editor.TokenComment, editor.TokenMeta)
	tok(p.muted, editor.TokenPunctuation)
	tok(p.blue, editor.TokenLink, editor.TokenTag, editor.TokenNumber)
	tok(p.accent, editor.TokenAttribute, editor.TokenProperty)
	tok(p.notice, editor.TokenString, editor.TokenEscape)
	tok(p.red, editor.TokenConstant, editor.TokenOperator, editor.TokenKeyword)
	return t
}

// discardGitChange is Yaak's handleDiscardChanges: it asks, discards the
// change, syncs the workspace from its files, and comes back to the
// commit dialog.
func (a *App) discardGitChange(dir, workspace, path string) {
	back := func() {
		a.refreshGit()
		a.prompt("git_commit", "Commit Changes", "", "")
	}
	a.confirmDialog("Discard Changes", "Do you really want to discard uncommitted changes for the selected item?", "Discard", true, func() {
		a.gitDiff = nil
		a.background(func() (func(), error) {
			err := a.Engine.GitRestore(a.ctx, dir, path)
			if err == nil {
				err = a.syncNow(workspace, dir)
			}
			return back, err
		})
	})
	a.asking.cancel = back
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

// warningButton is Yaak's warning-colored border button, 2xs.
func warningButton(c *ui.Context, p colors, label string) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(22).Padding(0, 8).Radius(4).Border(1, p.orange).Center().Shrink(0)
	if b.Hovered() {
		b.Background(p.orange.Alpha(.1))
	}
	b.Children(func() { ui.Text(c, label).FontSize(11).TextColor(p.orange) })
	return b
}

// gitModelPaths returns the paths in the repository of the changed files
// of models, by their IDs.
func (a *App) gitModelPaths() map[string]string {
	paths := map[string]string{}
	if a.gitState == nil {
		return paths
	}
	for _, f := range a.gitState.Files {
		if f.Model != nil {
			paths[s(f.Model, "id")] = f.Path
		}
	}
	return paths
}

// gitTreeMenu adds the git items of Yaak's sidebar context menu: View
// History of one item, and Restore Changes of those changed, after a
// separator when separate is set.
func (a *App) gitTreeMenu(menu *ui.Menu, items []engine.Object, separate bool) {
	dir, workspace := a.syncDir(), a.workspace
	if dir == "" || a.gitNoRepo {
		return
	}
	changed := a.gitModelPaths()
	var restore []string
	for _, m := range items {
		if path, ok := changed[s(m, "id")]; ok {
			restore = append(restore, path)
		}
	}
	if len(items) != 1 && len(restore) == 0 {
		return
	}
	if separate {
		menu.Separator()
	}
	if len(items) == 1 && menu.Item("View History").Chosen() {
		id := s(items[0], "id")
		path, ok := changed[id]
		a.background(func() (func(), error) {
			var err error
			if !ok {
				// Yaak's syncPathForModel.
				path, err = a.Engine.GitRepoPath(dir, "yaak."+id+".yaml")
			}
			return func() { a.openFileHistory(dir, path) }, err
		})
	}
	if len(restore) > 0 && menu.Item("Restore Changes").Chosen() {
		description := "This will discard uncommitted changes for the selected item."
		if len(restore) > 1 {
			description = fmt.Sprintf("This will discard uncommitted changes for %d selected items.", len(restore))
		}
		a.confirmDialog("Restore Changes", description, "Restore", true, func() {
			a.background(func() (func(), error) {
				for _, path := range restore {
					if err := a.Engine.GitRestore(a.ctx, dir, path); err != nil {
						return a.refreshGit, err
					}
				}
				return a.refreshGit, a.syncNow(workspace, dir)
			})
		})
	}
}

// fileHistory is Yaak's FileHistoryDialog's state: the commits changing a
// file, the one selected, and its diff.
type fileHistory struct {
	dir, path string
	loading   bool
	commits   []engine.GitCommit
	selected  string
	// diffOf is the commit whose diff was asked for.
	diffOf string
	diff   *editor.DiffState
	split  float32
}

func (a *App) openFileHistory(dir, path string) {
	h := &fileHistory{dir: dir, path: path, loading: true}
	a.fileHistory = h
	a.prompt("git_file_history", "File History", "", "")
	a.background(func() (func(), error) {
		commits, err := a.Engine.GitFileLog(a.ctx, dir, path)
		return func() {
			h.loading, h.commits = false, commits
			if len(commits) > 0 {
				h.selected = commits[0].Hash
			}
		}, err
	})
}

// fileHistoryDialog is Yaak's FileHistoryDialog: the commits changing a
// file beside the diff of the one selected, which Restore File restores.
func (a *App) fileHistoryDialog(c *ui.Context, p colors) {
	h := a.fileHistory
	if h == nil {
		return
	}
	if len(h.commits) == 0 {
		ui.Column(c).Grow(1).MinHeight(160).Padding(12, 20, 16, 20).Children(func() {
			if !h.loading {
				emptyState(c, p, "No history for this file")
			}
		})
		return
	}
	i := slices.IndexFunc(h.commits, func(cm engine.GitCommit) bool { return cm.Hash == h.selected })
	if i >= 0 && h.diffOf != h.selected {
		hash := h.selected
		h.diffOf = hash
		a.background(func() (func(), error) {
			original, modified, err := a.Engine.GitFileDiffForCommit(h.dir, hash, h.path)
			return func() {
				if h.selected == hash {
					h.diff = setYAMLDiff(h.diff, original, modified)
				}
			}, err
		})
	}
	// Yaak's split layout: the diff takes 60% of the width.
	if h.split == 0 {
		w, _ := c.Size()
		h.split = min(1120, w*.9) * .4
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(4, 8, 16, 8).Children(func() {
		splitQuiet(c, &h.split, false, func() {
			ui.Scroll(c).Fill().Padding(6, 16, 8, 16).Gap(0).Children(func() {
				for _, commit := range h.commits {
					a.fileHistoryCommit(c, p, h, commit)
				}
			})
		}, func() {
			ui.Column(c).Fill().MinWidth(0).Padding(0, 16).BorderWidth(0, 0, 0, 1).BorderColor(p.border).Gap(8).Children(func() {
				if i < 0 {
					emptyState(c, p, "Select a commit to view diff")
					return
				}
				commit := h.commits[i]
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, cmp.Or(commit.Message, "No message")).FontSize(12).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
					if warningButton(c, p, "Restore File").Clicked() {
						a.restoreFileFromCommit(h, commit.Hash)
					}
				})
				if h.diff == nil {
					ui.Box(c).Grow(1)
					return
				}
				a.gitDiffView(c, p, h.diff)
			})
		}).Fill()
	})
}

// fileHistoryCommit is Yaak's CommitListItem.
func (a *App) fileHistoryCommit(c *ui.Context, p colors, h *fileHistory, commit engine.GitCommit) {
	item := ui.ButtonBase(c).Key(commit.Hash).Label(cmp.Or(commit.Message, "No message")).FillWidth().Padding(6, 8).Radius(3).Column().Gap(1)
	switch {
	case commit.Hash == h.selected:
		item.Background(p.border.Alpha(.55))
	case item.Hovered():
		item.Background(p.border.Alpha(.3))
	}
	item.Children(func() {
		ui.Text(c, cmp.Or(commit.Message, "No message")).FontSize(13).SingleLine().FillWidth()
		ui.Row(c).Gap(4).FillWidth().Children(func() {
			ui.Text(c, cmp.Or(commit.Author, "Unknown")+" - "+timeAgo(commit.When)+" ago - ").FontSize(12).TextColor(p.muted).SingleLine().Shrink(1).MinWidth(0)
			ui.Text(c, commit.Hash[:min(7, len(commit.Hash))]).Font("monospace").FontSize(10).TextColor(p.muted).Shrink(0)
		})
	})
	if item.Clicked() {
		h.selected = commit.Hash
	}
}

// restoreFileFromCommit is Yaak's handleRestoreCommit: it asks, restores
// the file from the commit, syncs the workspace from its files, and comes
// back to the file's history.
func (a *App) restoreFileFromCommit(h *fileHistory, hash string) {
	workspace := a.workspace
	back := func() {
		a.refreshGit()
		a.fileHistory = h
		a.prompt("git_file_history", "File History", "", "")
	}
	a.confirmDialog("Restore File", "This will restore the file to the selected commit.", "Restore", false, func() {
		a.background(func() (func(), error) {
			err := a.Engine.GitRestoreFromCommit(a.ctx, h.dir, hash, h.path)
			if err == nil {
				err = a.syncNow(workspace, h.dir)
			}
			return back, err
		})
	})
	a.asking.cancel = back
}
