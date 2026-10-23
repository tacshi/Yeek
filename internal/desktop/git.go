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
	"yeek/locales"
)

func (a *App) syncDir() string {
	for _, meta := range a.list("workspace_meta") {
		if s(meta, "workspaceId") == a.workspace {
			return s(meta, "settingSyncDir")
		}
	}
	return ""
}

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

const maxSyncAttempts = 3

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

type syncReview struct {
	workspace, dir string
	plan           []engine.SyncChange
	attempt        int
	shown          bool
}

func incomingSyncChanges(plan []engine.SyncChange) []engine.SyncChange {
	var incoming []engine.SyncChange
	for _, c := range plan {
		direction := c.Direction
		if c.Conflict {
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

func (a *App) showSyncReview() {
	r := a.syncReview
	if r == nil || r.shown || a.dialogOpen && a.dialog != "commit_sync" {
		return
	}
	r.shown = true
	a.prompt("commit_sync", locales.ChangesDetected(), "", "")
}

func (a *App) hideSyncReview() {
	a.syncReview = nil
	if a.dialog == "commit_sync" {
		a.dialogOpen = false
	}
}

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
				ui.Text(c, locales.ChangesContainAWorkspaceDeletion()).FontWeight(600).TextColor(p.red)
			})
		}
		ui.Text(c, locales.FilesChangedInTheDirectory(len(incoming))).MaxLines(3)
		ui.Column(c).Gap(0).Margin(4, 0).Shrink(1).MinHeight(0).Children(func() {
			ui.Row(c).Padding(8, 0).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, locales.Type()).TextColor(p.muted).Width(130).SingleLine()
				ui.Text(c, locales.NameTitle()).TextColor(p.muted).Grow(1).SingleLine()
				ui.Text(c, locales.Operation()).TextColor(p.muted).SingleLine()
			})
			ui.Scroll(c).Shrink(1).MinHeight(0).Children(func() {
				for _, change := range incoming {
					model, label, color := change.Remote, locales.Update(), p.blue
					switch {
					case change.Local == nil:
						label, color = locales.Create(), p.green
					case change.Remote == nil:
						model, label, color = change.Local, locales.DeleteLower(), p.red
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
			if ui.Button(c, locales.Cancel()).Clicked() {
				a.syncReview, a.dialogOpen = nil, false
			}
			if ui.PrimaryButton(c, locales.ApplyChanges()).AutoFocus().Clicked() {
				workspace, dir, plan, attempt := r.workspace, r.dir, r.plan, r.attempt
				a.background(func() (func(), error) { return a.applySync(workspace, dir, plan, attempt) })
			}
		})
	})
}

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
			a.gitNoRepo = errors.Is(err, engine.ErrRepositoryNotFound) || errors.Is(err, engine.ErrGitNotFound)
			if err == nil {
				a.gitInfo = &info
			} else {
				a.gitInfo = nil
			}
			a.gitState = &state
		}, nil
	})
}

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

func gitMenuButton(c *ui.Context, p colors, label string, content func()) ui.Element {
	button := ui.ButtonBase(c).Label(label).Height(32).Shrink(0).Padding(0, 12).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(p.border)
	if button.Hovered() {
		button.Background(p.border.Alpha(.35))
	}
	button.Children(content)
	return button
}

func (a *App) gitButton(c *ui.Context, p colors) {
	a.keepSyncing()
	dir := a.syncDir()
	if dir == "" {
		if a.hiddenSetup("setup_sync") {
			return
		}
		gitMenuButton(c, p, locales.SetupFSSyncOrGit(), func() {
			icon(c, "wrench").FontSize(13).TextColor(p.subtle)
			ui.Text(c, locales.SetupFSSyncOrGit()).FontSize(12).TextColor(p.muted).SingleLine().Grow(1)
		}).Menu(func(m *ui.Menu) {
			m.Item(locales.WhenEnabledWorkspaceDataSyncsTo()).Disabled(true)
			if m.Item(locales.OpenWorkspaceSettings()).Chosen() {
				a.prompt("workspace_settings", locales.WorkspaceSettings(), "", a.workspace)
				a.modalTab = 1
			}
			m.Separator()
			if m.Item(locales.HideThisMessage()).Chosen() {
				a.confirmDialog(locales.HideSetupMessage(), locales.YouCanConfigureFilesystemSyncOr(), locales.Confirm(), false, func() { a.hideSetup("setup_sync") })
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
		gitMenuButton(c, p, locales.SetupGit(), func() {
			icon(c, "branch").FontSize(13).TextColor(p.subtle)
			ui.Text(c, locales.SetupGit()).FontSize(12).TextColor(p.muted).SingleLine().Grow(1)
		}).Menu(func(m *ui.Menu) {
			m.Item(locales.InitializeLocalRepoToStartVersioning()).Disabled(true)
			if m.Item(locales.InitializeGitRepo()).Chosen() {
				a.background(func() (func(), error) { return a.refreshGit, a.Engine.GitInit(dir) })
			}
			m.Separator()
			if m.Item(locales.HideThisMessage()).Chosen() {
				a.confirmDialog(locales.HideGitSetup(), locales.YouCanInitializeAGitRepo(), locales.Confirm(), false, func() { a.hideSetup("setup_git_repo") })
			}
		})
		return
	}
	info := a.gitInfo
	if info == nil {
		return
	}
	gitMenuButton(c, p, locales.GitBranch(info.Head), func() {
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

func (a *App) gitMenu(m *ui.Menu, dir string, info *engine.GitBranchInfo) {
	current := info.Head
	if m.Item(locales.ViewHistoryEllipsis()).Chosen() {
		a.prompt("git_history", locales.CommitHistory(), "", "")
	}
	if m.Item(locales.ManageRemotesEllipsis()).Chosen() {
		a.openGitRemotes()
	}
	m.Separator()
	if m.Item(locales.NewBranchEllipsis()).Chosen() {
		a.ask(locales.CreateBranch(), "", locales.CreateTitle(), false, []askInput{{label: locales.BranchName()}}, func(v []string) { a.newBranch(v[0], "") })
	}
	m.Separator()
	if m.Item(locales.Push()).Chosen() {
		a.gitPush()
	}
	if m.Item(locales.Pull()).Chosen() {
		a.gitPull()
	}
	if m.Item(locales.CommitEllipsis()).Chosen() {
		a.gitCommitMessage, a.commitCollapsed = "", nil
		a.prompt("git_commit", locales.CommitChanges(), "", "")
	}
	if a.gitState != nil && len(a.gitState.Files) > 0 && m.Item(locales.ResetChanges()).Chosen() {
		a.confirmDialog(locales.ResetChanges(), locales.ThisWillDiscardAllUncommittedChanges(), locales.Reset(), true, func() {
			a.gitOp(locales.ErrorResettingChanges(), func() (string, error) { return locales.ChangesHaveBeenReset(), a.Engine.GitResetChanges(a.ctx, dir) })
		})
	}
	if len(info.LocalBranches) > 0 {
		m.Separator()
		m.Item(locales.Branches()).Disabled(true)
	}
	for _, branch := range info.LocalBranches {
		here := branch == current
		label := branch
		if here {
			label = "✓ " + branch
		}
		m.Submenu(label, func(sub *ui.Menu) {
			if !here && sub.Item(locales.Checkout()).Chosen() {
				a.checkout(branch, false)
			}
			if !here && sub.Item(locales.MergeIntoBranch(current)).Chosen() {
				a.gitOp(locales.ErrorMergingBranch(), func() (string, error) {
					return locales.MergedBranchIntoTarget(branch, current), a.Engine.GitMergeBranch(a.ctx, dir, branch)
				})
			}
			if sub.Item(locales.NewBranchEllipsis()).Chosen() {
				a.ask(locales.NewBranch(), locales.CreateANewBranchFromBranch(branch), locales.CreateTitle(), false, []askInput{{label: locales.BranchName()}}, func(v []string) { a.newBranch(v[0], branch) })
			}
			if sub.Item(locales.RenameEllipsis()).Chosen() {
				a.ask(locales.RenameBranch(), "", locales.Rename(), false, []askInput{{label: locales.NewBranchName(), value: branch}}, func(v []string) {
					if name := strings.TrimSpace(v[0]); name != "" && name != branch {
						a.gitOp(locales.ErrorRenamingBranch(), func() (string, error) {
							return locales.RenamedBranchToName(branch, name), a.Engine.GitRenameBranch(a.ctx, dir, branch, name)
						})
					}
				})
			}
			if !here {
				sub.Separator()
				if sub.Item(locales.Delete()).Chosen() {
					a.confirmDialog(locales.DeleteBranch(), locales.PermanentlyDeleteBranch(branch), locales.Delete(), true, func() { a.deleteBranch(branch, false) })
				}
			}
		})
	}
	for _, branch := range info.RemoteBranches {
		if slices.Contains(info.LocalBranches, strings.TrimPrefix(branch, "origin/")) {
			continue
		}
		m.Submenu(branch, func(sub *ui.Menu) {
			if sub.Item(locales.Checkout()).Chosen() {
				a.checkout(branch, false)
			}
			if sub.Item(locales.Delete()).Chosen() {
				a.confirmDialog(locales.DeleteRemoteBranch(), locales.PermanentlyDeleteBranchFromTheRemote(branch), locales.Delete(), true, func() {
					a.gitOp(locales.ErrorDeletingRemoteBranch(), func() (string, error) {
						return locales.DeletedRemoteBranchBranch(branch), a.Engine.GitDeleteRemoteBranch(a.ctx, dir, branch)
					})
				})
			}
		})
	}
}

func (a *App) gitOp(failure string, op func() (string, error)) {
	workspace, dir := a.workspace, a.syncDir()
	a.background(func() (func(), error) {
		message, err := op()
		if err == nil {
			err = a.syncNow(workspace, dir)
		}
		return func() {
			if err != nil {
				a.showToast("", locales.FailureError(failure, err.Error()), "danger", 0)
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
				a.confirmDialog(locales.ConflictsDetected(), locales.YourBranchHasConflictsEitherMake(), locales.ForceCheckout(), false, func() { a.checkout(branch, true) })
			case err != nil:
				a.showToast("", locales.ErrorCheckingOutBranchError(err.Error()), "danger", 0)
			default:
				a.showToast("git-checkout-success", locales.SwitchedBranchBranch(name), "success", 5*time.Second)
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
				a.showToast("", locales.ErrorCreatingBranchError(err.Error()), "danger", 0)
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
				a.confirmDialog(locales.BranchNotFullyMerged(), locales.BranchBranchIsNotFullyMerged(branch), locales.Confirm(), false, func() { a.deleteBranch(branch, true) })
			case err != nil && force:
				a.showToast("", locales.ErrorForceDeletingBranchError(err.Error()), "danger", 0)
			case err != nil:
				a.showToast("", locales.ErrorDeletingBranchError(err.Error()), "danger", 0)
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
				a.showToast("", locales.ErrorPushingChangesError(err.Error()), "danger", 0)
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
				a.showToast("", locales.ErrorPullingChangesError(err.Error()), "danger", 0)
			default:
				a.gitResult(result, a.gitPull)
			}
			a.refreshGit()
		}, nil
	})
}

func (a *App) gitResult(r engine.GitResult, retry func()) {
	dir := a.syncDir()
	switch r.Kind {
	case "success":
		a.showToast("", r.Message, "success", 5*time.Second)
	case "up_to_date":
		a.showToast("", locales.AlreadyUpToDate(), "info", 5*time.Second)
	case "needs_credentials":
		a.askCredentials(r.URL, r.Error, func(user, password string) {
			a.background(func() (func(), error) {
				return retry, a.Engine.GitAddCredential(a.ctx, r.URL, user, password)
			})
		})
	case "diverged":
		a.diverged = &r
		a.divergedChoice = ""
		a.prompt("git_diverged", locales.BranchesDiverged(), "", "")
	case "uncommitted_changes":
		a.confirmDialog(locales.UncommittedChanges(), locales.YouHaveUncommittedChangesCommitOr(), locales.ResetAndPull(), true, func() {
			a.background(func() (func(), error) { return retry, a.Engine.GitResetChanges(a.ctx, dir) })
		})
	}
}

var githubRemote = regexp.MustCompile(`(?i)github\.com`)

func (a *App) askCredentials(remote, failure string, done func(user, password string)) {
	user, pass := askInput{label: locales.Username()}, askInput{label: locales.PasswordToken(), description: locales.EnterYourPasswordOrAccessToken(), password: true}
	if githubRemote.MatchString(remote) {
		user = askInput{label: locales.GitHubUsername(), description: locales.UseYourGitHubUsernameNotYour()}
		pass = askInput{label: locales.GitHubPersonalAccessToken(), description: locales.GitHubRequiresAPersonalAccessToken(), password: true}
	}
	description := locales.EnterCredentialsForRemote(remote)
	if failure != "" {
		description = failure
	}
	a.ask(locales.CredentialsRequired(), description, locales.Submit(), false, []askInput{user, pass}, func(v []string) { done(v[0], v[1]) })
}

func (a *App) addGitRemote(retry func()) {
	dir := a.syncDir()
	a.ask(locales.AddRemote(), "", locales.Add(), false, []askInput{{label: locales.NameTitle(), value: "origin"}, {label: "URL"}}, func(v []string) {
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
	a.prompt("git_remotes", locales.ManageRemotes(), "", "")
}

func (a *App) loadGitRemotes() {
	dir := a.syncDir()
	a.background(func() (func(), error) {
		remotes, err := a.Engine.GitRemotes(a.ctx, dir)
		return func() { a.gitRemotes = remotes }, err
	})
}

func (a *App) gitRemotesDialog(c *ui.Context, p colors) {
	dir := a.syncDir()
	ui.Column(c).Padding(16, 20).Gap(10).Children(func() {
		for _, remote := range a.gitRemotes {
			ui.Row(c).Key(remote.Name).Gap(10).Padding(6, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, remote.Name).FontWeight(600).FontSize(13).Width(90)
				ui.Text(c, remote.URL).Font("monospace").FontSize(12).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
				if smallIconButton(c, "trash", locales.RemoveRemote(remote.Name)).Clicked() {
					name := remote.Name
					a.background(func() (func(), error) { return a.loadGitRemotes, a.Engine.GitRemote(dir, name, "", true) })
				}
			})
		}
		if len(a.gitRemotes) == 0 {
			ui.Text(c, locales.NoRemotes()).FontSize(13).TextColor(p.muted)
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.AddRemote()).Clicked() {
				a.addGitRemote(func() { a.openGitRemotes() })
			}
		})
	})
}

func (a *App) gitHistoryDialog(c *ui.Context, p colors) {
	ui.Column(c).Height(420).Padding(8, 20, 12, 20).Children(func() {
		ui.Row(c).Height(30).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, locales.Message()).FontSize(12).FontWeight(600).Grow(1)
			ui.Text(c, locales.Author()).FontSize(12).FontWeight(600).Width(140)
			ui.Text(c, locales.When()).FontSize(12).FontWeight(600).Width(110)
		})
		ui.Scroll(c).Grow(1).Padding(0, 12, 0, 0).Children(func() {
			if a.gitState == nil {
				return
			}
			for _, commit := range a.gitState.Commits {
				ui.Row(c).Key(commit.Hash).Height(30).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(p.border.Alpha(.5)).Children(func() {
					if commit.Message == "" {
						ui.Text(c, locales.NoMessage()).Italic().FontSize(12).TextColor(p.muted).Grow(1)
					} else {
						ui.Text(c, commit.Message).FontSize(12).SingleLine().Grow(1).MinWidth(0)
					}
					author := commit.Author
					if author == "" {
						author = locales.Unknown()
					}
					ui.Text(c, author).FontSize(12).SingleLine().Width(140).Tooltip(locales.EmailEmail(commit.Email))
					ui.Text(c, timeAgo(commit.When)).FontSize(12).TextColor(p.muted).Width(110).Tooltip(commit.When.Format(time.RFC1123))
				})
			}
		})
	})
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return locales.CountSecondsAgo(int(d.Seconds()))
	case d < time.Hour:
		return locales.CountMinutesAgo(int(d.Minutes()))
	case d < 24*time.Hour:
		return locales.CountHoursAgo(int(d.Hours()))
	case d < 30*24*time.Hour:
		return locales.CountDaysAgo(int(d.Hours() / 24))
	case d < 365*24*time.Hour:
		return locales.CountMonthsAgo(int(d.Hours() / 24 / 30))
	}
	return locales.CountYearsAgo(int(d.Hours() / 24 / 365))
}

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
		ui.Column(c).Grow(1).Padding(12, 20, 16, 20).Children(func() { emptyState(c, p, locales.NoChangesSinceLastCommit()) })
		return
	}
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
				ui.Row(c).Gap(8).Padding(12, 0, 4, 0).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Height(1).Grow(1).Background(p.border)
					ui.Text(c, locales.OtherFiles()).FontSize(12).TextColor(p.subtle)
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
			ui.TextArea(c, &a.gitCommitMessage).Label(locales.CommitMessage()).Placeholder(locales.CommitMessageEllipsis()).Grow(1).MinHeight(0).FillWidth()
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
								a.showToast("", locales.ErrorCommittingError(err.Error()), "danger", 0)
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
				if ui.Button(c, locales.Commit()).Disabled(!ready).Clicked() {
					commit(false)
				}
				if ui.PrimaryButton(c, locales.CommitAndPush()).Disabled(!ready).Clicked() {
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

type gitEntry struct {
	path   string
	status string
	staged bool
	model  engine.Object
}

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

type commitNode struct {
	entry    gitEntry
	children []*commitNode
}

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
	for id, m := range a.models {
		if kind := s(m, "model"); !changed[id] && (kind == "workspace" && id == a.workspace || kind == "folder" && s(m, "workspaceId") == a.workspace) {
			entries = append(entries, gitEntry{status: "current", model: m})
		}
	}
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

func (n *commitNode) relevant() bool {
	return n.entry.status != "current" || slices.ContainsFunc(n.children, (*commitNode).relevant)
}

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
				glyph, label := "down", locales.CollapseName(name)
				if collapsed {
					glyph, label = "chevron", locales.ExpandName(name)
				}
				toggle := ui.ButtonBase(c).Label(label).Size(16, 16).Shrink(0).Center()
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
			title := locales.StageChangeName(name)
			if checked || mixed {
				title = locales.UnstageChangeName(name)
			}
			on := checked
			if checkbox(c, p, &on, mixed, title).Margin(0, 2, 0, 0).Changed() {
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
	ui.Row(c).Key(key).FillWidth().Children(func() {
		ui.Box(c).Width(8).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 16).BorderWidth(0, 0, 0, 1).BorderStyle(ui.BorderDashed).BorderColor(p.border).Children(body)
	})
}

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
		title := locales.StageChangeName(display)
		if staged {
			title = locales.UnstageChangeName(display)
		}
		if checkbox(c, p, &staged, false, title).Changed() {
			path := entry.path
			a.background(func() (func(), error) { return a.refreshGit, a.Engine.GitStage(dir, []string{path}, staged) })
		}
		icon(c, "file_code").TextColor(p.muted).Shrink(0)
		ui.Text(c, display).FontSize(13).SingleLine().Grow(1).MinWidth(0)
		statusChip(c, p, entry.status)
	})
}

func gitStatusLabel(status string) string {
	switch status {
	case "untracked":
		return locales.Untracked()
	case "conflict":
		return locales.Conflict()
	case "current":
		return locales.Current()
	case "modified":
		return locales.Modified()
	case "removed":
		return locales.Removed()
	case "renamed":
		return locales.Renamed()
	case "type_change":
		return locales.TypeChange()
	}
	return status
}

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
	chip.Children(func() { ui.Text(c, gitStatusLabel(status)).FontSize(11).FontWeight(500).TextColor(color).SingleLine() })
	return chip
}

type gitDiffPanel struct {
	path   string
	loaded *engine.GitStatus
	diff   *editor.DiffState
}

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
		emptyState(c, p, locales.SelectAChangeToViewDiff())
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
			ui.Text(c, locales.NameStatus(requestName(file.model), gitStatusLabel(file.status))).FontSize(12).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
			if warningButton(c, p, locales.DiscardChanges()).Clicked() {
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
		sep.Padding(4, 2)
		sep.Children(func() {
			bar := ui.ButtonBase(c).Grow(1).AlignSelf(ui.Stretch).Center().Radius(3).Background(p.sidebar).Border(1, p.border).
				Label(locales.ExpandCountUnchangedLines(s.Lines)).Opacity(.8)
			if bar.Hovered() {
				bar.Opacity(1)
			}
			bar.Children(func() {
				label := locales.CountUnchangedLines(s.Lines)
				ui.Text(c, "⦚ "+label+" ⦚").FontSize(size - 1).TextColor(p.text)
			})
			if bar.Clicked() {
				s.ExpandAll()
			}
		})
	}
	editor.DiffView(c, d).Grow(1).MinHeight(0).FillWidth()
}

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

func (a *App) discardGitChange(dir, workspace, path string) {
	back := func() {
		a.refreshGit()
		a.prompt("git_commit", locales.CommitChanges(), "", "")
	}
	a.confirmDialog(locales.DiscardChanges(), locales.DoYouReallyWantToDiscard(), locales.Discard(), true, func() {
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

func (a *App) gitDivergedDialog(c *ui.Context, p colors) {
	r := a.diverged
	if r == nil {
		return
	}
	dir, workspace := a.syncDir(), a.workspace
	ui.Column(c).Padding(20).Gap(14).Children(func() {
		ui.Text(c, locales.YourLocalBranchHasDivergedFrom(r.Remote, r.Branch)).FontSize(13).MaxLines(4)
		for _, option := range []struct{ value, label, description string }{
			{"merge", locales.MergeCommit(), locales.CombiningLocalAndRemoteChangesInto()},
			{"force_reset", locales.ForcePull(), locales.DiscardLocalCommitsAndResetTo()},
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
			if ui.Button(c, locales.Cancel()).Clicked() {
				a.dialogOpen, a.diverged = false, nil
			}
			label := map[string]string{"": locales.SelectAnOption(), "merge": locales.Merge(), "force_reset": locales.ForcePull()}[a.divergedChoice]
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
							a.showToast("", locales.ErrorPullingChangesError(err.Error()), "danger", 0)
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

func warningButton(c *ui.Context, p colors, label string) ui.Element {
	b := ui.ButtonBase(c).Label(label).Height(22).Padding(0, 8).Radius(4).Border(1, p.orange).Center().Shrink(0)
	if b.Hovered() {
		b.Background(p.orange.Alpha(.1))
	}
	b.Children(func() { ui.Text(c, label).FontSize(11).TextColor(p.orange) })
	return b
}

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
	if len(items) == 1 && menu.Item(locales.ViewHistory()).Chosen() {
		id := s(items[0], "id")
		path, ok := changed[id]
		a.background(func() (func(), error) {
			var err error
			if !ok {
				path, err = a.Engine.GitRepoPath(dir, engine.SyncFileName(dir, id))
			}
			return func() { a.openFileHistory(dir, path) }, err
		})
	}
	if len(restore) > 0 && menu.Item(locales.RestoreChanges()).Chosen() {
		description := locales.ThisWillDiscardUncommittedChangesFor()
		if len(restore) > 1 {
			description = locales.ThisWillDiscardUncommittedChangesForCount(len(restore))
		}
		a.confirmDialog(locales.RestoreChanges(), description, locales.Restore(), true, func() {
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

type fileHistory struct {
	dir, path string
	loading   bool
	commits   []engine.GitCommit
	selected  string
	diffOf    string
	diff      *editor.DiffState
	split     float32
}

func (a *App) openFileHistory(dir, path string) {
	h := &fileHistory{dir: dir, path: path, loading: true}
	a.fileHistory = h
	a.prompt("git_file_history", locales.FileHistory(), "", "")
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

func (a *App) fileHistoryDialog(c *ui.Context, p colors) {
	h := a.fileHistory
	if h == nil {
		return
	}
	if len(h.commits) == 0 {
		ui.Column(c).Grow(1).MinHeight(160).Padding(12, 20, 16, 20).Children(func() {
			if !h.loading {
				emptyState(c, p, locales.NoHistoryForThisFile())
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
					emptyState(c, p, locales.SelectACommitToViewDiff())
					return
				}
				commit := h.commits[i]
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, cmp.Or(commit.Message, locales.NoMessage())).FontSize(12).TextColor(p.muted).SingleLine().Grow(1).MinWidth(0)
					if warningButton(c, p, locales.RestoreFile()).Clicked() {
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

func (a *App) fileHistoryCommit(c *ui.Context, p colors, h *fileHistory, commit engine.GitCommit) {
	item := ui.ButtonBase(c).Key(commit.Hash).Label(cmp.Or(commit.Message, locales.NoMessage())).FillWidth().Padding(6, 8).Radius(3).Column().Gap(1)
	switch {
	case commit.Hash == h.selected:
		item.Background(p.border.Alpha(.55))
	case item.Hovered():
		item.Background(p.border.Alpha(.3))
	}
	item.Children(func() {
		ui.Text(c, cmp.Or(commit.Message, locales.NoMessage())).FontSize(13).SingleLine().FillWidth()
		ui.Row(c).Gap(4).FillWidth().Children(func() {
			ui.Text(c, locales.AuthorWhen(cmp.Or(commit.Author, locales.Unknown()), timeAgo(commit.When))).FontSize(12).TextColor(p.muted).SingleLine().Shrink(1).MinWidth(0)
			ui.Text(c, commit.Hash[:min(7, len(commit.Hash))]).Font("monospace").FontSize(10).TextColor(p.muted).Shrink(0)
		})
	})
	if item.Clicked() {
		h.selected = commit.Hash
	}
}

func (a *App) restoreFileFromCommit(h *fileHistory, hash string) {
	workspace := a.workspace
	back := func() {
		a.refreshGit()
		a.fileHistory = h
		a.prompt("git_file_history", locales.FileHistory(), "", "")
	}
	a.confirmDialog(locales.RestoreFile(), locales.ThisWillRestoreTheFileTo(), locales.Restore(), false, func() {
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
