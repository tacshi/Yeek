package desktop

import (
	"cmp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// Folder settings tabs, in Yaak's order.
const (
	folderTabGeneral = iota
	folderTabSettings
	folderTabHeaders
	folderTabAuth
	folderTabVariables
)

func (a *App) openScope(m engine.Object) { a.openFolderSettings(m, folderTabGeneral) }

func (a *App) openFolderSettings(m engine.Object, tab int) {
	a.scopeDraft = newDraft(m)
	a.scopeDraft.Tab = tab
	a.prompt("scope", "", "", s(m, "id"))
}

// folderEnvironment is the environment that overrides variables for a folder's requests.
func (a *App) folderEnvironment(folder string) engine.Object {
	for _, env := range a.list("environment") {
		if s(env, "parentModel") == "folder" && s(env, "parentId") == folder {
			return env
		}
	}
	return nil
}

// scopeDialog is Yaak's FolderSettingsDialog: a breadcrumb title over tabs
// down the left, saving each change as it is made.
func (a *App) scopeDialog(c *ui.Context, p colors) {
	d := a.scopeDraft
	if d == nil || a.models[d.ID] == nil {
		return
	}
	ui.Column(c).Height(540).Padding(16, 16, 12, 16).Gap(8).Children(func() {
		ui.Row(c).Gap(8).Padding(0, 0, 4, 0).Children(func() {
			icon(c, "folder").FontSize(18).TextColor(p.muted)
			path := a.requestPath(d.Model)
			for i, name := range path {
				if i > 0 {
					icon(c, "chevron").FontSize(14).TextColor(p.subtle)
				}
				text := ui.Text(c, name).FontSize(16).FontWeight(600).SingleLine().MinWidth(0)
				if i < len(path)-1 {
					text.TextColor(p.muted)
				}
			}
			ui.Spacer(c)
			if iconButton(c, "close", "Close dialog").Clicked() {
				a.dialogOpen = false
			}
		})
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Gap(12).Children(func() {
			env := a.folderEnvironment(d.ID)
			ui.Column(c).Width(150).Shrink(0).Gap(2).Children(func() {
				for i, item := range []tabItem{
					{Label: "General"},
					{Label: "Settings", Badge: badgeCount(overriddenSettings(d))},
					{Label: "Headers", Badge: badgeCount(a.headerCount(d))},
					{Label: authTabLabel(a, d.AuthType), Menu: func(m *ui.Menu) { a.authTypeMenu(m, d) }},
					{Label: "Variables", Badge: badgeCount(len(oslice(env, "variables")))},
				} {
					tab := navItem(c, p, item.Label, d.Tab == i).Key(i)
					if d.Tab == i && item.Menu != nil {
						tab.Menu(item.Menu)
					}
					if tab.Clicked() && d.Tab != i {
						d.Tab = i
					}
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				switch d.Tab {
				case folderTabGeneral:
					a.folderGeneral(c, p, d)
				case folderTabSettings:
					a.requestSettings(c, p, d)
				case folderTabHeaders:
					a.headersEditor(c, p, d)
				case folderTabAuth:
					a.authEditor(c, p, d)
				case folderTabVariables:
					a.folderVariables(c, p, d, env)
				}
			})
		})
	})
	if d.Dirty {
		a.saveFolderDraft(d)
	}
}

func (a *App) saveFolderDraft(d *Draft) {
	if a.deferUntilInputs(func() { a.saveFolderDraft(d) }) {
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

func (a *App) folderGeneral(c *ui.Context, p colors, d *Draft) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(12).Children(func() {
		labeled(c, p, "Folder Name", func() {
			if ui.TextInput(c, &d.Name).Label("Folder Name").FillWidth().Changed() {
				d.Dirty = true
			}
		})
		ui.Column(c).Grow(1).MinHeight(0).Radius(6).Border(1, p.border).Padding(0, 8).Children(func() {
			a.markdownEditor(c, p, d, "Folder description")
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "Delete Folder").FontSize(12).TextColor(p.red).Border(1, p.red.Alpha(.5)).Clicked() {
				a.prompt("delete", "Delete "+s(d.Model, "name"), s(d.Model, "name"), d.ID)
			}
			ui.Spacer(c)
			ui.Row(c).Gap(2).Padding(2, 4, 2, 8).Radius(4).Background(p.border.Alpha(.35)).Children(func() {
				ui.Text(c, d.ID).Font("monospace").FontSize(12).TextColor(p.accent).Selectable()
				if smallIconButton(c, "copy", "Copy folder ID").Clicked() {
					c.WriteClipboard(d.ID)
				}
			})
		})
	})
}

// folderVariables is the Variables tab: the folder's environment, or a way to create one.
func (a *App) folderVariables(c *ui.Context, p colors, d *Draft, env engine.Object) {
	if env == nil {
		ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
			ui.Text(c, "Override Variables for requests within this folder.").FontSize(13).TextColor(p.muted)
			if ui.Button(c, "Create Folder Environment").FontSize(12).Clicked() {
				folder, workspace := d.ID, s(d.Model, "workspaceId")
				a.run(func() (func(), error) {
					saved, err := a.Engine.Save(a.ctx, engine.Object{"model": "environment", "workspaceId": workspace, "parentModel": "folder", "parentId": folder, "name": "Folder Environment"})
					return func() { a.applyModel(saved) }, err
				})
			}
		})
		return
	}
	id := s(env, "id")
	if a.folderEnvID != id {
		a.folderEnvID, a.folderEnvRows = id, kvRows(env, "variables")
	}
	a.environmentEditorFor(c, p, env, &a.folderEnvRows, func() { a.saveEnvironmentRows(id, a.folderEnvRows) }, true)
}
