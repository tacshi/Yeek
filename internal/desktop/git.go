package desktop

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func (a *App) refreshGit() {
	dir := a.gitDirectory
	if dir == "" {
		return
	}
	workspace := a.workspace
	a.background(func() (func(), error) {
		state, gitErr := a.Engine.GitStatus(a.ctx, dir)
		plan, err := a.Engine.PlanSync(a.ctx, workspace, dir)
		return func() {
			if gitErr == nil {
				a.gitState = &state
			} else {
				a.gitState = nil
			}
			a.syncPlan = plan
		}, err
	})
}
func (a *App) syncFiles(resolve string) {
	dir, workspace, plan := a.gitDirectory, a.workspace, append([]engine.SyncChange{}, a.syncPlan...)
	a.saveActive()
	a.background(func() (func(), error) {
		err := a.Engine.ApplySync(a.ctx, workspace, dir, plan, resolve)
		return func() { a.refreshGit() }, err
	})
}
func (a *App) gitAction(operation string) {
	dir := a.gitDirectory
	a.background(func() (func(), error) {
		err := a.Engine.GitNetwork(a.ctx, dir, operation, "origin")
		return func() { a.refreshGit() }, err
	})
}
func (a *App) gitDialog(c *ui.Context, p colors) {
	ui.Column(c).Height(570).Padding(18).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if ui.TextInput(c, &a.gitDirectory).Placeholder("Workspace directory").Label("Sync directory").Grow(1).Submitted() {
				a.refreshGit()
			}
			if ui.Button(c, "Choose…").Clicked() {
				a.chooseSyncDir()
			}
			if ui.Button(c, "Refresh").Disabled(a.gitDirectory == "").Clicked() {
				a.refreshGit()
			}
		})
		if a.gitDirectory == "" {
			ui.Text(c, "Choose a directory to sync this workspace or connect it to Git.").TextColor(p.muted).FontSize(12)
			return
		}
		tabs(c, p, &a.gitTab, "Sync", "Changes", "History", "Branches", "Remotes")
		switch a.gitTab {
		case 0:
			ui.Scroll(c).Grow(1).Gap(5).Children(func() {
				count := 0
				for i := range a.syncPlan {
					change := &a.syncPlan[i]
					if change.Direction == "none" {
						continue
					}
					count++
					ui.Row(c).Key(change.ID).Gap(8).Padding(5).Children(func() {
						label := change.Direction
						if change.Conflict {
							label = "Conflict"
						}
						ui.Text(c, label).Width(60).FontSize(11).TextColor(p.accent)
						ui.Text(c, filepath.Base(change.Path)).Grow(1).SingleLine().FontSize(12)
						if change.Conflict {
							if ui.Button(c, "Keep Local").FontSize(11).Clicked() {
								change.Direction = "push"
								change.Conflict = false
							}
							if ui.Button(c, "Use Disk").FontSize(11).Clicked() {
								change.Direction = "pull"
								change.Conflict = false
							}
						}
					})
				}
				if count == 0 {
					ui.Text(c, "Workspace files are in sync").TextColor(p.muted).Padding(10)
				}
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Sync Automatically").Disabled(a.stopSync != nil).Clicked() {
					dir, wid := a.gitDirectory, a.workspace
					a.syncFiles("")
					a.background(func() (func(), error) {
						stop, err := a.Engine.WatchSync(a.ctx, wid, dir, func(err error) { a.Window.Update(func() { a.errorMessage = err.Error() }) })
						return func() { a.stopSync = stop }, err
					})
				}
				if a.stopSync != nil {
					if ui.Button(c, "Stop Auto Sync").Clicked() {
						a.stopSync()
						a.stopSync = nil
					}
				}
				if ui.PrimaryButton(c, "Synchronize").Clicked() {
					a.syncFiles("")
				}
			})
		case 1:
			if a.gitState == nil {
				ui.Text(c, "This directory does not contain a Git repository.").TextColor(p.muted)
				if ui.PrimaryButton(c, "Initialize Repository").Clicked() {
					dir := a.gitDirectory
					a.background(func() (func(), error) { return func() { a.refreshGit() }, a.Engine.GitInit(dir) })
				}
				return
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, a.gitState.Branch).FontWeight(600).Grow(1)
				for _, op := range []string{"fetch", "pull", "push"} {
					if ui.Button(c, fieldLabel(op)).Clicked() {
						a.gitAction(op)
					}
				}
			})
			ui.Scroll(c).Grow(1).Gap(4).Children(func() {
				for _, file := range a.gitState.Files {
					ui.Row(c).Key(file.Path).Gap(8).Padding(4).Children(func() {
						staged := file.Staging != " " && file.Staging != "?"
						if ui.Checkbox(c, &staged, "").Label("Stage " + file.Path).Changed() {
							dir, path := a.gitDirectory, file.Path
							a.background(func() (func(), error) {
								return func() { a.refreshGit() }, a.Engine.GitStage(dir, []string{path}, staged)
							})
						}
						ui.Text(c, file.Staging+file.Worktree).Font("monospace").FontSize(11).TextColor(p.accent)
						ui.Text(c, file.Path).Grow(1).SingleLine().FontSize(12)
					})
				}
			})
			ui.TextArea(c, &a.gitMessage).Height(70).Placeholder("Commit message").Label("Commit message")
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &a.gitAuthorName).Placeholder("Author (or Git config)").Label("Git author").Grow(1)
				ui.TextInput(c, &a.gitAuthorEmail).Placeholder("Email (or Git config)").Label("Git email").Grow(1)
				if ui.PrimaryButton(c, "Commit Staged").Disabled(a.gitMessage == "").Clicked() {
					dir, message, name, email := a.gitDirectory, a.gitMessage, a.gitAuthorName, a.gitAuthorEmail
					a.background(func() (func(), error) {
						_, err := a.Engine.GitCommit(dir, message, name, email)
						return func() { a.gitMessage = ""; a.refreshGit() }, err
					})
				}
			})
		case 2:
			ui.Scroll(c).Grow(1).Gap(10).Children(func() {
				if a.gitState != nil {
					for _, commit := range a.gitState.Commits {
						ui.Column(c).Key(commit.Hash).Gap(4).Padding(8).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
							ui.Text(c, commit.Message).FontSize(13).Selectable()
							ui.Text(c, fmt.Sprintf("%s · %s · %s", commit.Hash[:8], commit.Author, commit.When.Format("2 Jan 2006 15:04"))).FontSize(11).TextColor(p.muted)
						})
					}
				}
			})
		case 3:
			if a.gitState == nil {
				return
			}
			ui.Scroll(c).Grow(1).Gap(5).Children(func() {
				for _, branch := range a.gitState.Branches {
					ui.Row(c).Key(branch).Gap(8).Padding(6).Children(func() {
						ui.Text(c, branch).Grow(1)
						if branch == a.gitState.Branch {
							ui.Text(c, "Current").TextColor(p.muted).FontSize(11)
						} else if ui.Button(c, "Checkout").Clicked() {
							dir := a.gitDirectory
							a.background(func() (func(), error) { return func() { a.refreshGit() }, a.Engine.GitCheckout(dir, branch, false) })
						}
					})
				}
			})
			if ui.Button(c, "New Branch…").Clicked() {
				a.prompt("gitbranch", "New Branch", "", "")
			}
		case 4:
			if a.gitState != nil {
				for _, remote := range a.gitState.Remotes {
					ui.Text(c, remote).FontSize(12)
				}
			}
			ui.TextInput(c, &a.gitRemoteName).Placeholder("Remote name (origin)").Label("Remote name")
			ui.TextInput(c, &a.gitRemoteURL).Placeholder("Repository URL").Label("Repository URL")
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Add Remote").Disabled(a.gitRemoteURL == "").Clicked() {
					dir, name, url := a.gitDirectory, a.gitRemoteName, a.gitRemoteURL
					if name == "" {
						name = "origin"
					}
					a.background(func() (func(), error) { return func() { a.refreshGit() }, a.Engine.GitRemote(dir, name, url, false) })
				}
				if a.gitState == nil && ui.PrimaryButton(c, "Clone Here").Disabled(a.gitRemoteURL == "").Clicked() {
					dir, url := a.gitDirectory, a.gitRemoteURL
					a.background(func() (func(), error) { return func() { a.refreshGit() }, a.Engine.GitClone(a.ctx, url, dir) })
				}
			})
		}
	})
}
