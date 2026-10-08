package desktop

import (
	"cmp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// workspaceSettingsDialog is Yaak's WorkspaceSettingsDialog: Workspace,
// Settings, Headers, Auth and DNS tabs, saving each change as it is made.
func (a *App) workspaceSettingsDialog(c *ui.Context, p colors) {
	m := a.models[a.dialogID]
	if m == nil {
		return
	}
	d := a.workspaceDraft
	if d == nil || d.ID != s(m, "id") {
		d = newDraft(m)
		d.Tab = a.modalTab
		a.workspaceDraft = d
	}
	ui.Column(c).Height(560).Padding(8, 16, 0, 16).Children(func() {
		dns := len(oslice(m, "settingDnsOverrides"))
		tabBar(c, p, &d.Tab, []tabItem{
			{Label: "Workspace"},
			{Label: "Settings"},
			{Label: "Headers", Badge: badgeCount(a.headerCount(d))},
			{Label: authTabLabel(a, d.AuthType), Menu: func(menu *ui.Menu) { a.authTypeMenu(menu, d) }},
			{Label: "DNS", Badge: badgeCount(dns)},
		})
		switch d.Tab {
		case 0:
			a.workspaceGeneral(c, p, d)
		case 1:
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, 16, 16, 4).Gap(28).Children(func() {
				settingsSection(c, p, "workspace", "", func() {
					a.workspaceSyncSetting(c, p, m)
					a.workspaceEncryptionSetting(c, p, m)
				})
				a.modelSettingsEditor(c, p, d, true)
				settingsSection(c, p, "Certificate Authorities", "Certificate Authorities", func() {
					ui.Column(c).Padding(14, 0).Gap(10).Children(func() { a.caSettings(c, p, m) })
				})
			})
		case 2:
			a.headersEditor(c, p, d)
		case 3:
			a.authEditor(c, p, d)
		case 4:
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(12, 16, 12, 4).Gap(14).Children(func() { a.dnsSettings(c, p, m) })
		}
	})
	a.modalTab = d.Tab
	if d.Dirty {
		a.saveWorkspaceDraft(d)
	}
}

// saveWorkspaceDraft writes the fields the dialog edits onto the stored workspace.
func (a *App) saveWorkspaceDraft(d *Draft) {
	if a.deferUntilInputs(func() { a.saveWorkspaceDraft(d) }) {
		return
	}
	d.Dirty = false
	current := a.models[d.ID]
	if current == nil {
		return
	}
	m := deepCopy(current)
	m["name"] = cmp.Or(strings.TrimSpace(d.Name), s(current, "name"))
	m["description"] = d.Description
	m["headers"] = rowObjects(d.Headers)
	m["authenticationType"] = nilIfEmpty(d.AuthType)
	m["authentication"] = draftAuthObject(d)
	for _, setting := range []requestSetting{settingRequestTimeout, settingRequestMessageSize, settingValidateCertificates, settingFollowRedirects, settingHTTPVersion, settingSendCookies, settingStoreCookies} {
		if v, ok := d.Model[setting.key]; ok {
			m[setting.key] = v
		}
	}
	a.applyModel(m)
	a.saveModel(m)
}

func (a *App) workspaceGeneral(c *ui.Context, p colors, d *Draft) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(12).Padding(6, 0, 12, 0).Children(func() {
		if ui.TextInput(c, &d.Name).Label("Name").Placeholder("Workspace Name").FillWidth().Height(36).FontSize(15).Changed() {
			d.Dirty = true
		}
		ui.Column(c).Grow(1).MinHeight(0).Radius(6).Border(1, p.border).Padding(0, 8).Children(func() {
			a.markdownEditor(c, p, d, "Workspace description")
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Delete Workspace").FontSize(12).TextColor(p.red).Border(1, p.red.Alpha(.5)).Clicked() {
				a.prompt("delete", "Delete Workspace", s(d.Model, "name"), d.ID)
			}
			ui.Spacer(c)
			ui.Row(c).Gap(2).Padding(2, 4, 2, 8).Radius(4).Background(p.border.Alpha(.35)).Children(func() {
				ui.Text(c, d.ID).Font("monospace").FontSize(12).TextColor(p.accent).Selectable()
				if smallIconButton(c, "copy", "Copy workspace ID").Clicked() {
					c.WriteClipboard(d.ID)
				}
			})
		})
	})
}

// workspaceSyncSetting is Yaak's SyncToFilesystemSetting: the directory
// the workspace syncs with, which a non-empty directory can open instead.
func (a *App) workspaceSyncSetting(c *ui.Context, p colors, workspace engine.Object) {
	id := s(workspace, "id")
	dir := ""
	for _, meta := range a.list("workspace_meta") {
		if s(meta, "workspaceId") == id {
			dir = s(meta, "settingSyncDir")
		}
	}
	if a.notEmptySyncDir != "" {
		ui.Column(c).Margin(0, 0, 12, 0).Padding(10, 12).Gap(8).Radius(6).Border(1, p.notice.Alpha(.5)).Background(p.notice.Alpha(.08)).Children(func() {
			ui.Text(c, "Directory is not empty. Do you want to open it instead?").FontSize(13)
			if ui.Button(c, "Open Workspace").FontSize(12).Padding(3, 10).TextColor(p.notice).AlignSelf(ui.Start).Clicked() {
				a.openSyncDirWorkspace(a.notEmptySyncDir)
				a.notEmptySyncDir, a.dialogOpen = "", false
			}
		})
	}
	settingRow(c, p, "Local directory sync", "Sync data to a folder for backup and Git integration.", nil, func() {
		if dir == "" {
			if ui.Button(c, "Select Directory").FontSize(12).Clicked() {
				a.chooseSyncDir(id)
			}
			return
		}
		path := ui.ButtonBase(c).Label("Change directory").Padding(3, 8).Radius(4).Border(1, p.border).MaxWidth(320)
		path.Children(func() { ui.Text(c, dir).Font("monospace").FontSize(12).TextColor(p.muted).SingleLine() })
		if path.Clicked() {
			a.chooseSyncDir(id)
		}
		if smallIconButton(c, "close", "Clear directory").Clicked() {
			a.setSyncDir(id, "")
		}
	})
}

// openSyncDirWorkspace is Yaak's openWorkspaceFromSyncDir.
func (a *App) openSyncDirWorkspace(dir string) {
	a.background(func() (func(), error) {
		id, err := a.Engine.SyncDirWorkspace(dir)
		if err != nil {
			return nil, err
		}
		if err = a.syncNow(id, dir); err != nil {
			return nil, err
		}
		models, err := a.Engine.Store.List(a.ctx, "", "")
		return func() {
			for _, m := range models {
				a.applyModel(m)
			}
			a.switchWorkspace(id)
		}, err
	})
}

// workspaceEncryptionSetting is Yaak's "Workspace encryption" row.
func (a *App) workspaceEncryptionSetting(c *ui.Context, p colors, workspace engine.Object) {
	id := s(workspace, "id")
	settingRow(c, p, "Workspace encryption", "Encrypt secrets stored with the secure() template function.", nil, func() {
		if workspace["encryptionKeyChallenge"] == nil {
			if ui.Button(c, "Enable Encryption").FontSize(12).Clicked() {
				a.run(func() (func(), error) { return nil, a.Engine.EnableEncryption(a.ctx, id) })
			}
		} else if ui.Button(c, "Reveal Key").FontSize(12).Clicked() {
			a.run(func() (func(), error) {
				key, err := a.Engine.RevealWorkspaceKey(a.ctx, id)
				return func() { a.prompt("encryption_key", "Workspace Encryption Key", key, id) }, err
			})
		}
		if ui.Button(c, "Enter Key…").FontSize(12).Clicked() {
			a.prompt("set_encryption_key", "Set Workspace Encryption Key", "", id)
		}
	})
}
