package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

type createWorkspaceForm struct {
	name, dir, notEmpty string
	initGit, encryption bool
}

func (a *App) newWorkspace() {
	if a.deferUntilInputs(a.newWorkspace) {
		return
	}
	a.createWorkspace = &createWorkspaceForm{}
	a.prompt("create_workspace", locales.CreateWorkspace(), "", "")
}

var encryptionHelp = locales.EncryptPasswordsTokensAndOtherSensitive

func (a *App) createWorkspaceDialog(c *ui.Context, p colors) {
	f := a.createWorkspace
	if f == nil {
		return
	}
	submit := false
	ui.Column(c).Padding(8, 24, 16, 24).Gap(12).Children(func() {
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, locales.NameTitle()).FontSize(12).TextColor(p.muted)
			if ui.TextInput(c, &f.name).Label(locales.NameTitle()).AutoFocus().FillWidth().Submitted() {
				submit = true
			}
		})
		ui.Column(c).FillWidth().Gap(12).Margin(8, 0).Children(func() {
			if f.notEmpty != "" {
				ui.Column(c).FillWidth().Padding(10, 12).Gap(6).Radius(6).Border(1, p.notice.Alpha(.5)).Background(p.notice.Alpha(.08)).Children(func() {
					ui.Text(c, locales.DirectoryIsNotEmptyDoYou()).FontSize(13)
					if ui.Button(c, locales.OpenWorkspace()).FontSize(11).Padding(2, 8).TextColor(p.notice).Border(1, p.notice.Alpha(.6)).AlignSelf(ui.Start).Clicked() {
						a.openSyncDirWorkspace(f.notEmpty)
						a.dialogOpen = false
					}
				})
			}
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Row(c).Gap(4).Children(func() {
					ui.Text(c, locales.LocalDirectorySync()).FontSize(12).TextColor(p.muted)
					icon(c, "info").FontSize(11).TextColor(p.subtle).Tooltip(locales.SyncDataToAFolderFor())
				})
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					label := locales.SelectDirectory()
					if f.dir != "" {
						label = locales.ChangeDirectoryTitle()
					}
					if ui.Button(c, label).FontSize(11).Padding(2, 8).Clicked() {
						a.chooseNewWorkspaceDir(f)
					}
					if f.dir != "" {
						if smallIconButton(c, "close", locales.UnsetDirectory()).Clicked() {
							f.dir = ""
						}
						ui.Text(c, f.dir).Font("monospace").FontSize(11).SingleLine().MinWidth(0)
					} else {
						ui.Text(c, locales.NoDirectorySelected()).FontSize(13).MinWidth(0)
					}
				})
			})
			if f.dir != "" {
				ui.Checkbox(c, &f.initGit, locales.InitializeGitRepo()).FontSize(13)
			}
		})
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Gap(4).Children(func() {
				ui.Text(c, locales.WorkspaceEncryption()).FontSize(12).TextColor(p.muted)
				icon(c, "info").FontSize(11).TextColor(p.subtle).Tooltip(encryptionHelp())
			})
			ui.Checkbox(c, &f.encryption, locales.EnableEncryption()).FontSize(13)
		})
	})
	dialogFooter(c, p, nil, func() {
		if ui.PrimaryButton(c, locales.CreateWorkspace()).Disabled(strings.TrimSpace(f.name) == "").Clicked() {
			submit = true
		}
	})
	if submit && strings.TrimSpace(f.name) != "" {
		a.submitNewWorkspace(f)
	}
}

func (a *App) chooseNewWorkspaceDir(f *createWorkspaceForm) {
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: locales.SelectFolder(), Directory: true})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() { a.setNewWorkspaceDir(f, paths[0]) }, nil
	})
}

func (a *App) setNewWorkspaceDir(f *createWorkspaceForm, path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if notEmpty, err := dirNotEmpty(path); err == nil && notEmpty {
		f.notEmpty = path
		return
	}
	f.notEmpty, f.dir = "", path
}

var osFiles = map[string]bool{".DS_Store": true, ".localized": true, "Thumbs.db": true, "desktop.ini": true}

func dirNotEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !osFiles[entry.Name()] {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) submitNewWorkspace(f *createWorkspaceForm) {
	name, dir, initGit, encryption := strings.TrimSpace(f.name), f.dir, f.initGit, f.encryption
	a.dialogOpen = false
	a.saveActive()
	a.run(func() (func(), error) {
		workspace, err := a.Engine.Save(a.ctx, engine.Object{"model": "workspace", "name": name})
		if err != nil {
			return nil, err
		}
		id := s(workspace, "id")
		if err = a.Engine.Store.EnsureWorkspace(a.ctx, id); err != nil {
			return nil, err
		}
		metas, err := a.Engine.Store.List(a.ctx, "workspace_meta", id)
		if err != nil {
			return nil, err
		}
		meta := engine.Object{"model": "workspace_meta", "workspaceId": id}
		if len(metas) > 0 {
			meta = metas[0]
		}
		meta["settingSyncDir"] = nilIfEmpty(dir)
		if meta, err = a.Engine.Save(a.ctx, meta); err != nil {
			return nil, err
		}
		var gitErr error
		if initGit && dir != "" {
			gitErr = a.Engine.GitInit(dir)
		}
		return func() {
			a.applyModel(workspace)
			a.applyModel(meta)
			a.switchWorkspace(id)
			if gitErr != nil {
				a.showToast("git-init-error", locales.ErrorInitializingGitError(gitErr.Error()), "danger", 0)
			}
			if encryption {
				a.prompt("workspace_encryption", locales.WorkspaceEncryptionTitle(), "", id)
			}
		}, nil
	})
}

func (a *App) workspaceEncryptionDialog(c *ui.Context, p colors) {
	workspace := a.models[a.dialogID]
	if workspace == nil {
		workspace = a.models[a.workspace]
	}
	if workspace == nil {
		return
	}
	ui.Column(c).Padding(8, 24, 16, 24).Gap(12).Children(func() {
		a.workspaceEncryptionSetting(c, p, workspace)
		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, locales.Done()).Clicked() {
				a.dialogOpen = false
				a.finishEncryptionGate()
			}
		})
	})
}

type encryptionGate struct {
	workspace                string
	then                     func()
	dialog, title, value, id string
	dialogOpen               bool
}

var encryptionDialogs = []string{"workspace_encryption", "set_encryption_key", "encryption_key"}

func (a *App) withEncryptionEnabled(workspace string, then func()) {
	if a.workspaceKeySet(workspace) {
		then()
		return
	}
	a.encryptionGate = &encryptionGate{workspace: workspace, then: then, dialog: a.dialog, title: a.dialogTitle, value: a.dialogValue, id: a.dialogID, dialogOpen: a.dialogOpen}
	a.prompt("workspace_encryption", locales.WorkspaceEncryptionTitle(), "", workspace)
}

func (a *App) workspaceKeySet(workspace string) bool {
	for _, meta := range a.list("workspace_meta") {
		if s(meta, "workspaceId") == workspace && meta["encryptionKey"] != nil {
			return true
		}
	}
	return false
}

func (a *App) finishEncryptionGate() {
	g := a.encryptionGate
	if g == nil {
		return
	}
	a.encryptionGate = nil
	if g.dialogOpen {
		a.prompt(g.dialog, g.title, g.value, g.id)
	} else {
		a.dialogOpen = false
	}
	if a.workspaceKeySet(g.workspace) {
		g.then()
	}
}

func (a *App) checkEncryptionGate() {
	g := a.encryptionGate
	if g == nil || a.dialogOpen && slices.Contains(encryptionDialogs, a.dialog) {
		return
	}
	a.encryptionGate = nil
	if !a.dialogOpen && g.dialogOpen {
		a.prompt(g.dialog, g.title, g.value, g.id)
	}
}
