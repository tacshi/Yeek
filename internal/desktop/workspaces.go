package desktop

import (
	"slices"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

func (a *App) openWorkspace(id string) {
	if id == a.workspace {
		a.switchWorkspaceIn(id, true)
		return
	}
	if inNewWindow, ok := a.settings["openWorkspaceNewWindow"].(bool); ok {
		a.switchWorkspaceIn(id, inNewWindow)
		return
	}
	a.switchTarget, a.rememberWindow = id, false
	a.prompt("switch_workspace", locales.SwitchWorkspace(), "", id)
}

func (a *App) switchWorkspaceIn(id string, inNewWindow bool) {
	if inNewWindow && a.OpenWorkspace != nil {
		a.OpenWorkspace(id)
		return
	}
	a.switchWorkspace(id)
}

func (a *App) switchWorkspaceDialog(c *ui.Context, p colors) {
	id := a.switchTarget
	open := func(inNewWindow bool) {
		a.dialogOpen = false
		a.switchWorkspaceIn(id, inNewWindow)
		if a.rememberWindow {
			a.saveSetting("openWorkspaceNewWindow", inNewWindow)
		}
	}
	ui.Column(c).Padding(20).Gap(14).Children(func() {
		ui.Row(c).Gap(4).Wrap().Children(func() {
			ui.Text(c, locales.WhereToOpenBefore()).FontSize(13)
			ui.Text(c, requestName(a.models[id])).Font("monospace").FontSize(12).Padding(1, 4).Radius(3).Background(p.border.Alpha(.4))
			ui.Text(c, locales.WhereToOpenAfter()).FontSize(13)
		})
		ui.Checkbox(c, &a.rememberWindow, locales.RememberMyChoice())
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			newWindow := ui.ButtonBase(c).Label(locales.NewWindow()).Height(30).Padding(0, 12).Gap(6).Radius(5).Border(1, p.border)
			newWindow.Children(func() {
				ui.Text(c, locales.NewWindow()).FontSize(13)
				icon(c, "external").FontSize(12).TextColor(p.muted)
			})
			if newWindow.Clicked() {
				open(true)
			}
			if ui.PrimaryButton(c, locales.ThisWindow()).AutoFocus().Clicked() {
				open(false)
			}
		})
	})
}

func (a *App) moveToWorkspace(requests []engine.Object) {
	if len(requests) == 0 {
		return
	}
	title := locales.MoveRequest()
	if len(requests) > 1 {
		title = locales.MoveCountRequests(len(requests))
	}
	a.moving, a.moveTarget = treeIDs(requests), a.workspace
	a.prompt("move_workspace", title, "", "")
}

func (a *App) moveToWorkspaceDialog(c *ui.Context, p colors) {
	workspaces := a.list("workspace")
	labels, ids := []string{}, []string{}
	for _, w := range workspaces {
		label := s(w, "name")
		if s(w, "id") == a.workspace {
			label = locales.NameCurrent(label)
		}
		labels, ids = append(labels, label), append(ids, s(w, "id"))
	}
	chosen := labels[max(0, slices.Index(ids, a.moveTarget))]
	ui.Column(c).Padding(20).Gap(16).Children(func() {
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, locales.TargetWorkspace()).FontSize(12).TextColor(p.muted)
			if ui.Select(c, &chosen, labels).Label(locales.TargetWorkspace()).FillWidth().Changed() {
				a.moveTarget = ids[slices.Index(labels, chosen)]
			}
		})
		label := locales.Move()
		if len(a.moving) > 1 {
			label = locales.MoveCountRequests(len(a.moving))
		}
		if ui.PrimaryButton(c, label).Label(locales.ConfirmAction(label)).Disabled(a.moveTarget == a.workspace).FillWidth().Clicked() {
			target := a.moveTarget
			name := requestName(a.models[target])
			message := locales.CountRequestsMovedToWorkspace(len(a.moving), name)
			if len(a.moving) == 1 {
				message = locales.NameMovedToWorkspace(requestName(a.models[a.moving[0]]), name)
			}
			for _, id := range a.moving {
				a.patchModel(id, engine.Object{"workspaceId": target, "folderId": nil})
				a.closeTab(id)
			}
			a.tree.selected, a.moving, a.dialogOpen = nil, nil, false
			a.showToastAction("workspace-moved", message, "success", 5*time.Second, locales.SwitchToWorkspace(), func() { a.switchWorkspace(target) })
		}
	})
}
