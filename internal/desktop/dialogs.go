package desktop

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func (a *App) dialogs(c *ui.Context, p colors) {
	ui.DialogBase(c, &a.dialogOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .48))
		panel.Width(660).MaxWidthPercent(90).MaxHeightPercent(88).Padding(0).Radius(10).Background(p.background).Border(1, p.border).Gap(0)
		// A dialog without a title draws its own heading and close button.
		if a.dialogTitle != "" {
			ui.Row(c).Padding(16, 20).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
				ui.Text(c, a.dialogTitle).FontSize(16).FontWeight(600).Grow(1)
				if iconButton(c, "close", "Close dialog").Clicked() {
					a.dialogOpen = false
				}
			})
		}
		switch a.dialog {
		case "custom_method":
			a.customMethodDialog(c, p)
		case "multipart_part":
			a.multipartPartDialog(c, p)
		case "workspace", "folder", "rename", "cookie_jar":
			ui.Column(c).Padding(20).Gap(16).Children(func() {
				entry := ui.TextInput(c, &a.dialogValue).Label("Name").Placeholder("Name").AutoFocus().FillWidth()
				submit := entry.Submitted()
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, "Cancel").Clicked() {
						a.dialogOpen = false
					}
					if ui.PrimaryButton(c, "Save").Disabled(strings.TrimSpace(a.dialogValue) == "").Clicked() || submit {
						a.confirmName()
					}
				})
			})
		case "delete":
			ui.Column(c).Padding(20).Gap(18).Children(func() {
				ui.Text(c, "Delete “"+a.dialogValue+"” and its contents?")
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, "Cancel").Clicked() {
						a.dialogOpen = false
					}
					if ui.PrimaryButton(c, "Delete").Background(p.red).Clicked() {
						deletedEnvironment := s(a.models[a.dialogID], "model") == "environment"
						if a.dialogID == a.environment {
							a.environment = ""
						}
						a.deleteModel(a.dialogID)
						a.dialogOpen = false
						if deletedEnvironment {
							a.openEnvironments()
						}
					}
				})
			})
		case "delete_body":
			a.deleteBodyDialog(c, p)
		case "delete_items":
			a.deleteItemsDialog(c, p)
		case "description", "curl":
			ui.Column(c).Padding(20).Gap(14).Children(func() {
				ui.TextArea(c, &a.dialogValue).Label(a.dialogTitle).Height(280).Font("monospace").FontSize(12).AutoFocus()
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, "Cancel").Clicked() {
						a.dialogOpen = false
					}
					label := "Save"
					if a.dialog == "curl" {
						label = "Import"
					}
					if ui.PrimaryButton(c, label).Clicked() {
						if a.dialog == "curl" {
							a.importCurl()
						} else {
							if d := a.drafts[a.dialogID]; d != nil {
								d.Description = a.dialogValue
								d.Dirty = true
								a.save(d)
							}
							a.dialogOpen = false
						}
					}
				})
			})
		case "response_preview":
			panel.Width(760)
			a.responsePreview(c, p)
		case "settings":
			panel.Width(800)
			a.settingsDialog(c, p)
		case "workspace_settings":
			panel.Width(850)
			a.workspaceSettingsDialog(c, p)
		case "scope":
			panel.Width(760)
			a.scopeDialog(c, p)
		case "new_environment":
			panel.Width(480)
			a.newEnvironmentDialog(c, p)
		case "environment_color":
			panel.Width(480)
			a.environmentColorDialog(c, p)
		case "environments":
			panel.Width(850)
			a.environmentDialog(c, p)
		case "cookies":
			panel.Width(1200).Height(700)
			a.cookiesDialog(c, p)
		case "delete_cookie_jar":
			ui.Column(c).Padding(20).Gap(16).Children(func() {
				ui.Text(c, "Delete “"+a.dialogValue+"” and its cookies?")
				ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
					if ui.Button(c, "Cancel").Clicked() {
						a.dialogOpen = false
					}
					if ui.PrimaryButton(c, "Delete Jar").Background(p.red).Clicked() {
						a.deleteCookieJar(a.dialogID)
						a.dialogOpen = false
					}
				})
			})
		case "switch_workspace":
			panel.Width(420)
			a.switchWorkspaceDialog(c, p)
		case "move_workspace":
			panel.Width(420)
			a.moveToWorkspaceDialog(c, p)
		case "grpc_schema":
			panel.Width(560)
			a.grpcSchemaDialog(c, p)
		case "ask":
			panel.Width(480)
			a.askDialog(c, p)
		case "git_commit":
			panel.Width(760)
			a.gitCommitDialog(c, p)
		case "git_history":
			panel.Width(760)
			a.gitHistoryDialog(c, p)
		case "git_remotes":
			panel.Width(560)
			a.gitRemotesDialog(c, p)
		case "git_diverged":
			panel.Width(480)
			a.gitDivergedDialog(c, p)
		case "shortcuts":
			ui.Scroll(c).MaxHeight(520).Padding(14, 22).Gap(10).Children(func() {
				for _, h := range hotkeys {
					ui.Row(c).Key(h.action).Gap(60).Children(func() {
						ui.Text(c, h.label).Grow(1)
						ui.Text(c, a.hotkeyText(h.action)).Font("monospace").TextColor(p.muted)
					})
				}
			})
		case "exports":
			a.exportDialog(c, p)
		case "imports":
			panel.Width(1080).Height(720)
			a.importDialog(c, p)
		case "encryption_key":
			ui.Column(c).Padding(22).Gap(15).Children(func() {
				ui.Text(c, a.dialogValue).Font("monospace").FontSize(13).Selectable()
				if ui.Button(c, "Copy Key").Clicked() {
					c.WriteClipboard(a.dialogValue)
				}
			})
		case "set_encryption_key":
			ui.Column(c).Padding(22).Gap(15).Children(func() {
				ui.TextInput(c, &a.dialogValue).Label("Workspace encryption key").Placeholder("YK…").Password().FillWidth()
				if ui.PrimaryButton(c, "Set Key").Clicked() {
					id, value := a.dialogID, a.dialogValue
					a.run(func() (func(), error) {
						return func() { a.dialogValue = ""; a.dialogOpen = false }, a.Engine.SetWorkspaceKey(a.ctx, id, value)
					})
				}
			})
		}
	})
}
func (a *App) confirmName() {
	kind, title, id, workspace := a.dialog, strings.TrimSpace(a.dialogValue), a.dialogID, a.workspace
	if title == "" {
		return
	}
	a.dialogOpen = false
	a.run(func() (func(), error) {
		var m engine.Object
		var err error
		switch kind {
		case "workspace":
			m, err = a.Engine.Save(a.ctx, engine.Object{"model": "workspace", "name": title})
			if err == nil {
				err = a.Engine.Store.EnsureWorkspace(a.ctx, s(m, "id"))
			}
		case "folder":
			m, err = a.Engine.Save(a.ctx, engine.Object{"model": "folder", "name": title, "workspaceId": workspace, "folderId": nilIfEmpty(id), "sortPriority": float64(-time.Now().UnixMilli())})
		case "cookie_jar":
			m, err = a.Engine.Save(a.ctx, engine.Object{"model": "cookie_jar", "workspaceId": workspace, "name": title})
		case "rename":
			m, err = a.Engine.Store.Get(a.ctx, id)
			if err == nil {
				m, err = a.Engine.Save(a.ctx, engine.Object{"model": s(m, "model"), "id": id, "createdAt": m["createdAt"], "name": title})
			}
		}
		return func() {
			if m == nil {
				return
			}
			a.applyModel(m)
			if kind == "cookie_jar" {
				a.selectCookieJar(s(m, "id"))
			}
			if kind == "workspace" {
				a.switchWorkspace(s(m, "id"))
			}
			if kind == "rename" && s(m, "model") == "environment" {
				// Renaming happens from the environment editor, which comes back.
				a.openEnvironmentsAt(s(m, "id"))
			}
			if d := a.drafts[s(m, "id")]; d != nil {
				d.Name = title
			}
			if kind == "folder" && id != "" {
				a.expanded[id] = true
			}
		}, err
	})
}

// navItem is a left-aligned list entry for dialog sidebars.
func navItem(c *ui.Context, p colors, label string, active bool) ui.Element {
	item := ui.ButtonBase(c).Label(label).Height(28).Padding(0, 10).Radius(4).Justify(ui.Start)
	switch {
	case active:
		item.Background(p.border.Alpha(.55))
	case item.Hovered():
		item.Background(p.border.Alpha(.3))
	}
	item.Children(func() {
		text := ui.Text(c, label).FontSize(13).SingleLine()
		if !active {
			text.TextColor(p.muted)
		}
	})
	return item
}

// labeled shows a small caption above a form control.
func labeled(c *ui.Context, p colors, label string, control func()) {
	ui.Column(c).Key(label).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.muted)
		control()
	})
}

// deleteItemsDialog is Yaak's confirmation for deleting sidebar items.
func (a *App) deleteItemsDialog(c *ui.Context, p colors) {
	var names []string
	for _, id := range a.deleteIDs {
		if m := a.models[id]; m != nil {
			names = append(names, requestName(m))
		}
	}
	ui.Column(c).Padding(20).Gap(18).Children(func() {
		switch {
		case len(names) == 1:
			ui.Row(c).Gap(4).Children(func() {
				ui.Text(c, "Permanently delete")
				ui.Text(c, names[0]).Font("monospace").FontSize(12).Padding(1, 4).Radius(3).Background(p.border.Alpha(.4)).SingleLine().Shrink(1).MinWidth(0)
				ui.Text(c, "?")
			})
		case len(names) < 10:
			ui.Text(c, "Permanently delete the following?")
			ui.Column(c).Gap(4).Children(func() {
				for i, name := range names {
					ui.Text(c, name).Key(i).Font("monospace").FontSize(12).Padding(1, 4).Radius(3).Background(p.border.Alpha(.4)).SingleLine()
				}
			})
		default:
			ui.Text(c, "Permanently delete all "+strings.ToLower(pluralizeCount("Item", len(names)))+"?")
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen = false
			}
			if ui.PrimaryButton(c, "Delete").Background(p.red).Clicked() {
				for _, id := range a.deleteIDs {
					// A folder's contents go with it.
					if !slices.ContainsFunc(a.deleteIDs, func(other string) bool { return other != id && a.hasAncestor(id, other) }) {
						a.deleteModel(id)
					}
				}
				a.tree.selected, a.deleteIDs = nil, nil
				a.dialogOpen = false
			}
		})
	})
}
