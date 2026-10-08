package desktop

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func (a *App) dialogs(c *ui.Context, p colors) {
	ui.DialogBase(c, &a.dialogOpen, func(backdrop, panel *ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .48))
		panel.Width(660).MaxWidthPercent(90).MaxHeightPercent(88).Padding(0).Radius(10).Background(p.background).Border(1, p.border).Gap(0)
		ui.Row(c).Padding(16, 20).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, a.dialogTitle).FontSize(16).FontWeight(600).Grow(1)
			if iconButton(c, "close", "Close dialog").Clicked() {
				a.dialogOpen = false
			}
		})
		switch a.dialog {
		case "custom_method":
			a.customMethodDialog(c, p)
		case "multipart_part":
			a.multipartPartDialog(c, p)
		case "workspace", "folder", "rename", "gitbranch", "cookie_jar":
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
						a.deleteModel(a.dialogID)
						a.dialogOpen = false
					}
				})
			})
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
		case "settings":
			panel.Width(800)
			a.settingsDialog(c, p)
		case "workspace_settings":
			panel.Width(850)
			a.workspaceSettingsDialog(c, p)
		case "graphql_docs":
			panel.Width(900)
			a.graphqlDocs(c, p)
		case "scope":
			panel.Width(760)
			a.scopeDialog(c, p)
		case "folder_variables":
			panel.Width(760)
			ui.Column(c).Height(420).Padding(4, 16, 0, 16).Children(func() {
				dirty := false
				a.kvEditor(c, p, &a.envDraft, "Variable", "Value", &dirty)
				if dirty {
					a.saveEnvironment()
				}
			})
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
		case "history":
			a.historyDialog(c, p)
		case "palette":
			a.palette(c, p)
		case "git":
			panel.Width(880)
			a.gitDialog(c, p)
		case "shortcuts":
			ui.Column(c).Padding(22).Gap(14).Children(func() {
				for _, row := range []struct{ name, key string }{{"Send request", "⌘ ↩"}, {"New request", "⌘ N"}, {"Save request", "⌘ S"}, {"Close request tab", "⌘ W"}, {"Duplicate request", "⌘ D"}, {"Search requests", "⌘ P"}, {"Settings", "⌘ ,"}} {
					ui.Row(c).Gap(60).Children(func() { ui.Text(c, row.name).Grow(1); ui.Text(c, row.key).Font("monospace").TextColor(p.muted) })
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
		case "about":
			ui.Column(c).Padding(24).Gap(14).Children(func() {
				ui.Text(c, "Yeek 0.1.0").FontSize(22).FontWeight(600)
				ui.Text(c, "Native API client built in Go with MyGo.").TextColor(p.muted)
				ui.Text(c, "Yaak-compatible workspaces and request workflows.").TextColor(p.muted)
				ui.Text(c, a.Engine.DataDir).FontSize(12).Font("monospace").Selectable()
				if ui.Button(c, "Open Data Folder").Clicked() {
					a.run(func() (func(), error) { return nil, mygo.Shell.OpenPath(a.Engine.DataDir) })
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
			m, err = a.Engine.Save(a.ctx, engine.Object{"model": "folder", "name": title, "workspaceId": workspace, "folderId": nilIfEmpty(id)})
		case "cookie_jar":
			m, err = a.Engine.Save(a.ctx, engine.Object{"model": "cookie_jar", "workspaceId": workspace, "name": title})
		case "gitbranch":
			err = a.Engine.GitCheckout(a.gitDirectory, title, true)
		case "rename":
			m, err = a.Engine.Store.Get(a.ctx, id)
			if err == nil {
				m, err = a.Engine.Save(a.ctx, engine.Object{"model": s(m, "model"), "id": id, "createdAt": m["createdAt"], "name": title})
			}
		}
		return func() {
			if kind == "gitbranch" {
				a.refreshGit()
				return
			}
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
			if d := a.drafts[s(m, "id")]; d != nil {
				d.Name = title
			}
			if kind == "folder" && id != "" {
				a.expanded[id] = true
			}
		}, err
	})
}
func (a *App) settingsDialog(c *ui.Context, p colors) {
	selectedTab := a.modalTab
	ui.Row(c).Height(500).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(165).Padding(10).Gap(2).Background(p.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
			for i, label := range []string{"General", "Appearance", "Editor", "Proxy", "Certificates", "Plugins"} {
				if navItem(c, p, label, a.modalTab == i).Clicked() {
					selectedTab = i
				}
			}
		})
		ui.Scroll(c).Grow(1).Padding(20, 24).Gap(16).Children(func() {
			switch a.modalTab {
			case 0:
				ui.Text(c, "General").FontSize(18).FontWeight(600)
				labeled(c, p, "Data directory", func() {
					ui.Text(c, a.Engine.DataDir).Font("monospace").FontSize(12).Selectable()
					ui.Row(c).Children(func() {
						if ui.Button(c, "Open Data Folder").Clicked() {
							a.run(func() (func(), error) { return nil, mygo.Shell.OpenPath(a.Engine.DataDir) })
						}
					})
				})
				a.settingToggle(c, "coloredMethods", "Color request methods")
				a.settingToggle(c, "useNativeTitlebar", "Use native title bar after restarting")
			case 1:
				ui.Text(c, "Appearance").FontSize(18).FontWeight(600)
				appearances := []string{"System", "Light", "Dark"}
				appearance := "System"
				if i := slices.Index([]string{"system", "light", "dark"}, s(a.settings, "appearance")); i >= 0 {
					appearance = appearances[i]
				}
				labeled(c, p, "Appearance", func() {
					if ui.Select(c, &appearance, appearances).Label("Appearance").FillWidth().Changed() {
						a.saveSetting("appearance", strings.ToLower(appearance))
					}
				})
				for _, dark := range []bool{false, true} {
					key, label := "themeLight", "Light theme"
					if dark {
						key, label = "themeDark", "Dark theme"
					}
					names, ids := []string{"Yaak"}, []string{"yaak-light"}
					if dark {
						ids[0] = "yaak-dark"
					}
					for _, theme := range builtinThemes {
						if theme.Dark == dark {
							names = append(names, theme.Name)
							ids = append(ids, theme.ID)
						}
					}
					for _, theme := range a.Engine.PluginThemes() {
						if theme.Dark == dark {
							names = append(names, theme.Name)
							ids = append(ids, "plugin:"+theme.Name)
						}
					}
					chosen := "Yaak"
					if i := slices.Index(ids, s(a.settings, key)); i >= 0 {
						chosen = names[i]
					}
					labeled(c, p, label, func() {
						if ui.Select(c, &chosen, names).Label(label).FillWidth().Changed() {
							a.saveSetting(key, ids[slices.Index(names, chosen)])
						}
					})
				}
				size := n(a.settings, "interfaceFontSize")
				if size == 0 {
					size = 13
				}
				labeled(c, p, "Interface font size", func() {
					if ui.NumberInput(c, &size, 9, 24, 1).Label("Interface font size").Width(settingControlWidth).Changed() {
						a.saveSetting("interfaceFontSize", size)
					}
				})
				font := s(a.settings, "interfaceFont")
				labeled(c, p, "Interface font", func() {
					if ui.TextInput(c, &font).Placeholder("System default").Label("Interface font").FillWidth().Changed() {
						a.saveSetting("interfaceFont", font)
					}
				})
			case 2:
				ui.Text(c, "Editor").FontSize(18).FontWeight(600)
				font := s(a.settings, "editorFont")
				labeled(c, p, "Editor font", func() {
					if ui.TextInput(c, &font).Placeholder("System monospace").Label("Editor font").FillWidth().Changed() {
						a.saveSetting("editorFont", font)
					}
				})
				size := n(a.settings, "editorFontSize")
				if size == 0 {
					size = 12
				}
				labeled(c, p, "Editor font size", func() {
					if ui.NumberInput(c, &size, 8, 30, 1).Label("Editor font size").Width(settingControlWidth).Changed() {
						a.saveSetting("editorFontSize", size)
					}
				})
				a.settingToggle(c, "editorSoftWrap", "Wrap long lines")
			case 3:
				a.proxySettingsView(c, p)
			case 5:
				a.pluginsSettings(c, p)
			case 4:
				a.certificateSettings(c, p)
			}
		})
	})
	a.modalTab = selectedTab
}

// navItem is a left-aligned list entry for dialog sidebars.
func navItem(c *ui.Context, p colors, label string, active bool) *ui.Element {
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
func (a *App) settingToggle(c *ui.Context, key, label string) {
	value := b(a.settings, key)
	if ui.Checkbox(c, &value, label).Changed() {
		a.saveSetting(key, value)
	}
}
func (a *App) workspaceSettingsDialog(c *ui.Context, p colors) {
	m := a.models[a.workspace]
	if m == nil {
		return
	}
	next := a.modalTab
	ui.Box(c).Padding(8, 16, 0, 16).Children(func() { tabs(c, p, &next, "Requests", "DNS", "CA Certificates") })
	switch a.modalTab {
	case 0:
		a.workspaceRequestSettings(c, p)
	case 1:
		ui.Scroll(c).Height(460).Padding(20).Gap(14).Children(func() { a.dnsSettings(c, p, m) })
	case 2:
		ui.Scroll(c).Height(460).Padding(20).Gap(14).Children(func() { a.caSettings(c, p, m) })
	}
	a.modalTab = next
}
func (a *App) workspaceRequestSettings(c *ui.Context, p colors) {
	m := a.models[a.workspace]
	if m == nil {
		return
	}
	ui.Scroll(c).Height(460).Padding(16, 20).Gap(10).Children(func() {
		for _, field := range []struct{ key, label string }{{"settingValidateCertificates", "Validate TLS certificates"}, {"settingFollowRedirects", "Follow redirects"}, {"settingSendCookies", "Send cookies"}, {"settingStoreCookies", "Store cookies"}} {
			value := b(m, field.key)
			if ui.Checkbox(c, &value, field.label).Changed() {
				m = deepCopy(m)
				m[field.key] = value
				a.saveModel(engine.Object{"model": "workspace", "id": s(m, "id"), "createdAt": m["createdAt"], field.key: value})
			}
		}
		timeout := n(m, "settingRequestTimeout")
		settingRow(c, p, "Request timeout (ms, 0 for none)", func() {
			if ui.NumberInput(c, &timeout, 0, 3600000, 1000).Label("Request timeout").Width(settingControlWidth).Changed() {
				m = deepCopy(m)
				m["settingRequestTimeout"] = timeout
				a.saveModel(engine.Object{"model": "workspace", "id": s(m, "id"), "createdAt": m["createdAt"], "settingRequestTimeout": timeout})
			}
		})
		limit := n(m, "settingRequestMessageSize") / (1 << 20)
		settingRow(c, p, "WebSocket and gRPC message limit (MiB)", func() {
			if ui.NumberInput(c, &limit, 1.0/(1<<20), 2047.999999, 1).Label("Workspace message limit").Width(settingControlWidth).Changed() {
				m = deepCopy(m)
				m["settingRequestMessageSize"] = int64(limit * (1 << 20))
				a.saveModel(engine.Object{"model": "workspace", "id": s(m, "id"), "createdAt": m["createdAt"], "settingRequestMessageSize": m["settingRequestMessageSize"]})
			}
		})
		versions, values := []string{"Automatic", "HTTP/1.1", "HTTP/2"}, []string{"auto", "http1", "http2"}
		version := versions[max(0, slices.Index(values, s(m, "settingHttpVersion")))]
		settingRow(c, p, "HTTP version", func() {
			if ui.Select(c, &version, versions).Label("HTTP version").Width(settingControlWidth).Changed() {
				value := values[slices.Index(versions, version)]
				m = deepCopy(m)
				m["settingHttpVersion"] = value
				a.saveModel(engine.Object{"model": "workspace", "id": s(m, "id"), "createdAt": m["createdAt"], "settingHttpVersion": value})
			}
		})
		settingRow(c, p, "Workspace encryption", func() {
			if m["encryptionKeyChallenge"] == nil {
				if ui.Button(c, "Enable Encryption").Clicked() {
					id := a.workspace
					a.run(func() (func(), error) { return nil, a.Engine.EnableEncryption(a.ctx, id) })
				}
			} else {
				if ui.Button(c, "Reveal Workspace Key").Clicked() {
					id := a.workspace
					a.run(func() (func(), error) {
						key, err := a.Engine.RevealWorkspaceKey(a.ctx, id)
						return func() { a.prompt("encryption_key", "Workspace Encryption Key", key, id) }, err
					})
				}
			}
			if ui.Button(c, "Enter Key…").Clicked() {
				a.prompt("set_encryption_key", "Set Workspace Encryption Key", "", a.workspace)
			}
		})
	})
}
func (a *App) openEnvironments() {
	id := a.environment
	for _, env := range a.list("environment") {
		if id == "" && s(env, "parentModel") == "workspace" {
			id = s(env, "id")
			break
		}
	}
	a.prompt("environments", "Environments", "", id)
	a.envDraft = kvRows(a.models[id], "variables")
}
func (a *App) environmentDialog(c *ui.Context, p colors) {
	ui.Row(c).Height(430).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(185).Padding(10).Gap(2).Background(p.sidebar).BorderWidth(0, 1, 0, 0).BorderColor(p.border).Children(func() {
			for _, env := range a.list("environment") {
				if s(env, "parentModel") == "folder" {
					continue
				}
				if navItem(c, p, s(env, "name"), s(env, "id") == a.dialogID).Key(s(env, "id")).Clicked() {
					a.saveEnvironment()
					a.dialogID = s(env, "id")
					a.envDraft = kvRows(env, "variables")
				}
			}
			ui.Spacer(c)
			if ui.Button(c, "New Environment").Clicked() {
				workspace := a.workspace
				parent := a.dialogID
				a.run(func() (func(), error) {
					env, err := a.Engine.Save(a.ctx, engine.Object{"model": "environment", "workspaceId": workspace, "parentModel": "environment", "parentId": parent, "name": "New Environment"})
					return func() { a.applyModel(env); a.dialogID = s(env, "id"); a.envDraft = nil }, err
				})
			}
		})
		ui.Column(c).Grow(1).Padding(4, 16, 0, 16).Children(func() {
			dirty := false
			a.kvEditor(c, p, &a.envDraft, "Variable", "Value", &dirty)
			if dirty {
				a.saveEnvironment()
			}
			ui.Row(c).Padding(12, 0).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Rename").Clicked() {
					a.saveEnvironment()
					a.prompt("rename", "Rename Environment", s(a.models[a.dialogID], "name"), a.dialogID)
				}
				if ui.PrimaryButton(c, "Save Variables").Clicked() {
					a.saveEnvironment()
					a.dialogOpen = false
				}
			})
		})
	})
}
func (a *App) saveEnvironment() {
	if m := a.models[a.dialogID]; m != nil {
		copy := deepCopy(m)
		copy["variables"] = rowObjects(a.envDraft)
		a.saveModel(copy)
	}
}
func (a *App) historyDialog(c *ui.Context, p colors) {
	responses := a.list("http_response")
	slices.SortFunc(responses, func(x, y engine.Object) int { return strings.Compare(s(y, "createdAt"), s(x, "createdAt")) })
	ui.Scroll(c).Height(420).Padding(10).Children(func() {
		for _, response := range responses {
			ui.Row(c).Key(s(response, "id")).Padding(9, 10).Gap(12).Children(func() {
				ui.Text(c, statusLabel(response, true)).Width(35).TextColor(statusColor(response, p)).Font("monospace").FontSize(12)
				if ui.ButtonBase(c).Grow(1).Children(func() { ui.Text(c, s(response, "url")).SingleLine().FontSize(12) }).Clicked() {
					a.openRequest(s(response, "requestId"))
					a.showResponse(response)
					a.dialogOpen = false
				}
				ui.Text(c, durationText(n(response, "elapsed"))).TextColor(p.muted).FontSize(11)
			})
		}
		if len(responses) == 0 {
			ui.Text(c, "No requests have been sent").Padding(12).TextColor(p.muted)
		}
	})
}
func (a *App) palette(c *ui.Context, p colors) {
	input := ui.TextInput(c, &a.paletteQuery).Placeholder("Find a request…").Label("Search requests").AutoFocus().Margin(12, 12, 4, 12).FillWidth()
	query := strings.ToLower(strings.TrimSpace(a.paletteQuery))
	matches := []engine.Object{}
	for _, kind := range []string{"http_request", "grpc_request", "websocket_request"} {
		for _, m := range a.list(kind) {
			text := strings.ToLower(requestMethod(m) + " " + requestName(m) + " " + s(m, "url"))
			if strings.Contains(text, query) {
				matches = append(matches, m)
			}
		}
	}
	slices.SortStableFunc(matches, func(x, y engine.Object) int {
		return cmp.Compare(strings.ToLower(requestName(x)), strings.ToLower(requestName(y)))
	})
	open := func(m engine.Object) {
		a.openRequest(s(m, "id"))
		a.revealInSidebar(s(m, "id"))
		a.dialogOpen = false
	}
	if input.Submitted() && len(matches) > 0 {
		open(matches[0])
	}
	ui.Scroll(c).Height(380).Padding(4, 8, 8, 8).Gap(1).Children(func() {
		for i, m := range matches {
			row := ui.ButtonBase(c).Key(s(m, "id")).Label(requestName(m)).FillWidth().Height(32).Padding(0, 10).Gap(10).Radius(4).Justify(ui.Start)
			if row.Hovered() || i == 0 && query != "" {
				row.Background(p.border.Alpha(.45))
			}
			row.Children(func() {
				method := requestMethod(m)
				ui.Text(c, shortMethod(method)).Font("monospace").FontSize(11).TextColor(a.methodColor(method, p)).Shrink(0)
				ui.Text(c, requestName(m)).FontSize(13).SingleLine().Shrink(0)
				ui.Text(c, s(m, "url")).FontSize(12).TextColor(p.subtle).SingleLine().Grow(1).MinWidth(0)
			})
			if row.Clicked() {
				open(m)
			}
		}
		if len(matches) == 0 {
			ui.Text(c, "No matching requests").FontSize(12).TextColor(p.muted).Padding(10)
		}
	})
}
