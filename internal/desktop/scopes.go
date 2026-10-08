package desktop

import (
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func (a *App) openScope(m engine.Object) {
	a.scopeDraft = newDraft(m)
	a.modalTab = 0
	a.prompt("scope", s(m, "name"), "", s(m, "id"))
}
func (a *App) scopeDialog(c *ui.Context, p colors) {
	d := a.scopeDraft
	if d == nil {
		return
	}
	ui.Column(c).Height(470).Padding(8, 16, 0, 16).Children(func() {
		tabBar(c, p, &a.modalTab, []tabItem{
			{Label: "Headers", Badge: badgeCount(a.headerCount(d))},
			{Label: authTabLabel(a, d.AuthType), Menu: func(m *ui.Menu) { a.authTypeMenu(m, d) }},
			{Label: "Settings", Badge: badgeCount(overriddenSettings(d))},
			{Label: "Info", Badge: badgeDot(strings.TrimSpace(d.Description) != "")},
		})
		switch a.modalTab {
		case 0:
			a.headersEditor(c, p, d)
		case 1:
			a.authEditor(c, p, d)
		case 2:
			a.requestSettings(c, p, d)
		case 3:
			a.descriptionEditor(c, p, d, "Folder description")
		}
		ui.Row(c).Padding(12, 0).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "Save").Clicked() {
				m := deepCopy(d.Model)
				m["headers"] = rowObjects(d.Headers)
				m["authenticationType"] = nilIfEmpty(d.AuthType)
				m["authentication"] = draftAuthObject(d)
				m["description"] = d.Description
				if strings.TrimSpace(d.Name) != "" {
					m["name"] = d.Name
				}
				a.saveModel(m)
				a.dialogOpen = false
			}
		})
	})
}
func (a *App) openFolderVariables(folder engine.Object) {
	id, wid := s(folder, "id"), a.workspace
	a.run(func() (func(), error) {
		envs, err := a.Engine.Store.Find(a.ctx, "environment", "parentId", id)
		if err != nil {
			return nil, err
		}
		var env engine.Object
		if len(envs) > 0 {
			env = envs[0]
		} else {
			env, err = a.Engine.Save(a.ctx, engine.Object{"model": "environment", "workspaceId": wid, "parentModel": "folder", "parentId": id, "name": s(folder, "name")})
		}
		return func() {
			a.applyModel(env)
			a.envDraft = kvRows(env, "variables")
			a.prompt("folder_variables", "Folder Variables", "", s(env, "id"))
		}, err
	})
}
