package desktop

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type cookieDraft struct {
	oldKey, name, value, domain, path, expires, sameSite string
	secure, httpOnly, subdomains, session                bool
}
type cookieManager struct {
	jar, filter, selected, error string
	draft                        *cookieDraft
	busy, focusTable             bool
	pane                         float32
	row                          int
	list                         ui.ListState
	sort                         ui.SortOrder
}

func (a *App) activeCookieJar() engine.Object {
	if a.cookieSelections == nil {
		a.cookieSelections = map[string]string{}
	}
	id := a.cookieJar
	valid := func(id string) bool {
		m := a.models[id]
		return s(m, "model") == "cookie_jar" && s(m, "workspaceId") == a.workspace
	}
	if !valid(id) {
		id = a.cookieSelections[a.workspace]
	}
	if !valid(id) {
		id = ""
		if jars := a.list("cookie_jar"); len(jars) > 0 {
			id = s(jars[0], "id")
		}
	}
	a.cookieJar = id
	if id != "" {
		a.cookieSelections[a.workspace] = id
	}
	return a.models[id]
}
func (a *App) selectCookieJar(id string) {
	if jar := a.models[id]; s(jar, "model") != "cookie_jar" || s(jar, "workspaceId") != a.workspace {
		return
	}
	a.cookieJar = id
	if a.cookieSelections == nil {
		a.cookieSelections = map[string]string{}
	}
	a.cookieSelections[a.workspace] = id
	a.persistSession()
}
func (a *App) cookieMenu(c *ui.Context) {
	jar := a.activeCookieJar()
	iconButton(c, "cookie", "Cookie jars").Disabled(a.workspace == "").Tooltip("Cookie jar: " + s(jar, "name")).Menu(func(menu *ui.Menu) {
		jars := a.list("cookie_jar")
		for _, candidate := range jars {
			if menu.Item(s(candidate, "name")).Checked(s(candidate, "id") == a.cookieJar).Chosen() {
				a.selectCookieJar(s(candidate, "id"))
			}
		}
		menu.Separator()
		if menu.Item("Manage Cookies…").Disabled(jar == nil).Chosen() {
			a.openCookies(s(jar, "id"))
		}
		if menu.Item("Rename Cookie Jar…").Disabled(jar == nil).Chosen() {
			a.prompt("rename", "Rename Cookie Jar", s(jar, "name"), s(jar, "id"))
		}
		if menu.Item("Delete Cookie Jar…").Disabled(len(jars) < 2).Chosen() {
			a.prompt("delete_cookie_jar", "Delete Cookie Jar", s(jar, "name"), s(jar, "id"))
		}
		menu.Separator()
		if menu.Item("New Cookie Jar…").Chosen() {
			a.prompt("cookie_jar", "New Cookie Jar", "", "")
		}
	})
}
func (a *App) deleteCookieJar(id string) {
	a.run(func() (func(), error) {
		err := a.Engine.DeleteCookieJar(a.ctx, id)
		return func() {
			delete(a.models, id)
			a.modelVersion++
			if a.cookieJar == id {
				a.cookieJar = ""
			}
			for workspace, selected := range a.cookieSelections {
				if selected == id {
					delete(a.cookieSelections, workspace)
				}
			}
			a.activeCookieJar()
			a.persistSession()
		}, err
	})
}
func (a *App) openCookies(id string) {
	a.cookies = &cookieManager{jar: id, row: -1, pane: 245, sort: ui.SortOrder{Column: "Name"}}
	a.prompt("cookies", s(a.models[id], "name")+" Cookies", "", id)
}
func (c *cookieDraft) object() (engine.Object, error) {
	domain := engine.Object{"HostOnly": c.domain}
	if c.subdomains {
		domain = engine.Object{"Suffix": c.domain}
	}
	var expiration any = "SessionEnd"
	if !c.session {
		value, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.expires))
		if err != nil {
			value, err = time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(c.expires), time.Local)
		}
		if err != nil {
			return nil, errors.New("enter the expiration as an RFC 3339 date or YYYY-MM-DD HH:MM")
		}
		expiration = engine.Object{"AtUtc": strconv.FormatInt(value.Unix(), 10)}
	}
	return engine.NormalizeCookie(engine.Object{"name": c.name, "value": c.value, "domain": domain, "path": c.path, "secure": c.secure, "httpOnly": c.httpOnly, "sameSite": c.sameSite, "expires": expiration})
}
func newCookieDraft(cookie engine.Object) *cookieDraft {
	host, subdomains := engine.CookieDomain(cookie)
	d := &cookieDraft{name: s(cookie, "name"), value: s(cookie, "value"), domain: host, subdomains: subdomains, path: cmp.Or(s(cookie, "path"), "/"), secure: b(cookie, "secure"), httpOnly: b(cookie, "httpOnly"), sameSite: s(cookie, "sameSite"), session: true}
	if cookie != nil {
		d.oldKey = engine.CookieKey(cookie)
	} else {
		d.subdomains = false
	}
	if expires, err := engine.CookieExpiry(cookie); err == nil && !expires.IsZero() {
		d.session = false
		d.expires = expires.UTC().Format(time.RFC3339)
	}
	return d
}
func cookieDomainText(cookie engine.Object) string {
	domain, subdomains := engine.CookieDomain(cookie)
	if domain == "" {
		return "—"
	}
	if subdomains {
		return "." + domain
	}
	return domain
}
func cookieExpiryText(cookie engine.Object) string {
	expires, err := engine.CookieExpiry(cookie)
	if err != nil {
		return "Invalid expiration"
	}
	if expires.IsZero() {
		return "Session"
	}
	return expires.Local().Format("2006-01-02 15:04:05")
}
func cookieRows(jar engine.Object, filter string, sort ui.SortOrder) []engine.Object {
	filter = strings.ToLower(strings.TrimSpace(filter))
	out := []engine.Object{}
	seen := map[string]bool{}
	cookies := oslice(jar, "cookies")
	for i := len(cookies) - 1; i >= 0; i-- {
		cookie := cookies[i]
		key := engine.CookieKey(cookie)
		if seen[key] {
			continue
		}
		seen[key] = true
		if filter != "" && !strings.Contains(strings.ToLower(s(cookie, "name")+"\n"+s(cookie, "value")+"\n"+cookieDomainText(cookie)+"\n"+s(cookie, "path")), filter) {
			continue
		}
		out = append(out, cookie)
	}
	text := func(cookie engine.Object) string {
		switch sort.Column {
		case "Value":
			return s(cookie, "value")
		case "Domain":
			return cookieDomainText(cookie)
		case "Path":
			return s(cookie, "path")
		case "Expires":
			return cookieExpiryText(cookie)
		case "Same Site":
			return s(cookie, "sameSite")
		default:
			return s(cookie, "name")
		}
	}
	slices.SortFunc(out, func(a, b engine.Object) int {
		order := strings.Compare(strings.ToLower(text(a)), strings.ToLower(text(b)))
		switch sort.Column {
		case "Size":
			order = cmp.Compare(len(s(a, "name"))+len(s(a, "value")), len(s(b, "name"))+len(s(b, "value")))
		case "HTTP Only":
			order = cmp.Compare(fmt.Sprint(a["httpOnly"]), fmt.Sprint(b["httpOnly"]))
		case "Secure":
			order = cmp.Compare(fmt.Sprint(a["secure"]), fmt.Sprint(b["secure"]))
		}
		if order == 0 {
			order = strings.Compare(engine.CookieKey(a), engine.CookieKey(b))
		}
		if sort.Descending {
			return -order
		}
		return order
	})
	return out
}

func (a *App) cookiesDialog(c *ui.Context, p colors) {
	manager := a.cookies
	if manager == nil {
		a.cookies = &cookieManager{jar: a.dialogID, row: -1, pane: 245}
		manager = a.cookies
	}
	jar := a.models[manager.jar]
	if jar == nil {
		ui.Text(c, "Select a cookie jar.").Padding(20)
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		var addButton *ui.Element
		ui.Row(c).Padding(10, 14).Gap(8).Children(func() {
			ui.TextInput(c, &manager.filter).Placeholder("Filter cookies").Label("Filter cookies").Grow(1).MinWidth(0)
			if manager.filter != "" && smallIconButton(c, "close", "Clear cookie filter").Clicked() {
				manager.filter = ""
			}
			addButton = ui.Button(c, "Add Cookie").Disabled(manager.busy)
			if addButton.Clicked() {
				manager.draft = newCookieDraft(nil)
				manager.selected = ""
				manager.error = ""
				if d := a.drafts[a.active]; d != nil {
					if u, err := url.Parse(d.URL); err == nil {
						host := u.Hostname()
						if !strings.ContainsAny(host, "{}[] ") {
							manager.draft.domain = host
						}
					}
				}
			}
			if ui.Button(c, "Clear All").Disabled(manager.busy || len(oslice(jar, "cookies")) == 0).Clicked() {
				a.changeCookieManager(manager, func() (engine.Object, error) { return a.Engine.ClearCookies(a.ctx, manager.jar) }, "")
			}
		})
		rows := cookieRows(jar, manager.filter, manager.sort)
		if manager.focusTable && len(rows) == 0 {
			addButton.Focus()
			manager.focusTable = false
		}
		var selected engine.Object
		for _, cookie := range rows {
			if engine.CookieKey(cookie) == manager.selected {
				selected = cookie
				break
			}
		}
		table := func() { a.cookieTable(c, p, manager, rows) }
		if selected != nil || manager.draft != nil {
			ui.SplitVertical(c, &manager.pane, table, func() { a.cookieDetails(c, p, manager, selected) }).Grow(1).MinHeight(0)
		} else {
			ui.Column(c).Grow(1).MinHeight(0).Children(table)
		}
		if manager.error != "" {
			ui.Text(c, manager.error).TextColor(p.red).FontSize(12).Padding(8, 14).MaxLines(3)
		}
	})
}
func (a *App) cookieTable(c *ui.Context, p colors, manager *cookieManager, rows []engine.Object) {
	if len(rows) == 0 {
		ui.Column(c).Fill().Center().Children(func() {
			label := "No cookies stored"
			if manager.filter != "" {
				label = "No cookies match this filter"
			}
			ui.Text(c, label).TextColor(p.muted).FontSize(12)
		})
		return
	}
	manager.row = slices.IndexFunc(rows, func(cookie engine.Object) bool { return engine.CookieKey(cookie) == manager.selected })
	manager.list.Selected = &manager.row
	manager.list.Sort = &manager.sort
	manager.list.Key = func(i int) any { return engine.CookieKey(rows[i]) }
	manager.list.Label = func(i int) string {
		return "Cookie " + s(rows[i], "name") + " on " + cookieDomainText(rows[i]) + " at " + s(rows[i], "path")
	}
	if manager.focusTable && manager.row >= 0 {
		manager.list.ScrollTo(manager.row, ui.Center)
	}
	columns := []ui.TableColumn{{Title: "Name", Width: 130, Sortable: true}, {Title: "Value", Width: 205, Sortable: true}, {Title: "Domain", Width: 165, Sortable: true}, {Title: "Path", Width: 85, Sortable: true}, {Title: "Expires", Width: 165, Sortable: true}, {Title: "Size", Width: 60, Sortable: true}, {Title: "HTTP Only", Width: 90, Sortable: true}, {Title: "Secure", Width: 65, Sortable: true}, {Title: "Same Site", Width: 90, Sortable: true}, {Title: "", Width: 40, Fixed: true}}
	action := false
	table := ui.Table(c, &manager.list, columns, len(rows), func(row, col int) {
		cookie := rows[row]
		text := ""
		switch col {
		case 0:
			text = s(cookie, "name")
		case 1:
			text = s(cookie, "value")
		case 2:
			text = cookieDomainText(cookie)
		case 3:
			text = s(cookie, "path")
		case 4:
			text = cookieExpiryText(cookie)
		case 5:
			text = fmt.Sprint(len(s(cookie, "name")) + len(s(cookie, "value")))
		case 6:
			text = yesNo(b(cookie, "httpOnly"))
		case 7:
			text = yesNo(b(cookie, "secure"))
		case 8:
			text = cmp.Or(s(cookie, "sameSite"), "—")
		case 9:
			if smallIconButton(c, "close", "Delete cookie "+s(cookie, "name")).Disabled(manager.busy).Clicked() {
				action = true
				key := engine.CookieKey(cookie)
				a.changeCookieManager(manager, func() (engine.Object, error) { return a.Engine.DeleteCookie(a.ctx, manager.jar, key) }, "")
			}
			return
		}
		ui.Text(c, text).Font("monospace").FontSize(11).SingleLine().Tooltip(text)
	}).Grow(1).MinHeight(0).FontSize(11).Label("Cookies")
	if manager.focusTable {
		table.Focus()
		manager.focusTable = false
	}
	if !action && table.Changed() && manager.row >= 0 && manager.row < len(rows) {
		key := engine.CookieKey(rows[manager.row])
		if key != manager.selected {
			manager.selected = key
			manager.draft = nil
			manager.error = ""
		}
	}
	if table.Submitted() && manager.row >= 0 && manager.row < len(rows) {
		manager.draft = newCookieDraft(rows[manager.row])
		manager.error = ""
	}
}
func yesNo(value bool) string {
	if value {
		return "Yes"
	}
	return "No"
}

func (a *App) cookieDetails(c *ui.Context, p colors, manager *cookieManager, cookie engine.Object) {
	scope := ui.Column(c).Fill().MinHeight(0).Background(p.sidebar)
	scope.Children(func() {
		if scope.Shortcut(ui.Cmd, ui.KeyEnter) && manager.draft != nil && !manager.busy {
			a.saveCookie(manager)
		}
		ui.Row(c).Padding(10, 14).Gap(8).BorderWidth(1, 0, 1, 0).BorderColor(p.border).Children(func() {
			title := s(cookie, "name")
			if manager.draft != nil {
				title = "Edit Cookie"
				if manager.draft.oldKey == "" {
					title = "New Cookie"
				}
			}
			ui.Text(c, title).FontSize(14).FontWeight(600).Grow(1)
			if manager.draft == nil {
				if ui.Button(c, "Copy Value").FontSize(11).Clicked() {
					c.WriteClipboard(s(cookie, "value"))
				}
				if ui.Button(c, "Edit Cookie").FontSize(11).Clicked() {
					manager.draft = newCookieDraft(cookie)
					manager.error = ""
				}
			} else {
				if ui.Button(c, "Cancel Edit").Disabled(manager.busy).FontSize(11).Clicked() {
					manager.draft = nil
					manager.error = ""
					manager.focusTable = true
				}
				if ui.PrimaryButton(c, "Save Cookie").Disabled(manager.busy).FontSize(11).Clicked() {
					a.saveCookie(manager)
				}
			}
			if iconButton(c, "close", "Close cookie details").Disabled(manager.busy).Clicked() {
				manager.selected = ""
				manager.draft = nil
				manager.error = ""
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(12, 16).Gap(9).Disabled(manager.busy).Children(func() {
			row := func(label string, content func()) {
				ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
					ui.Text(c, label).Width(100).FontSize(12).TextColor(p.muted)
					ui.Column(c).Grow(1).MinWidth(0).Children(content)
				})
			}
			if draft := manager.draft; draft != nil {
				row("Name", func() { ui.TextInput(c, &draft.name).Label("Cookie name").AutoFocus().FillWidth() })
				row("Value", func() {
					ui.TextArea(c, &draft.value).Label("Cookie value").Height(80).FillWidth().Font("monospace").FontSize(12)
				})
				row("Domain", func() {
					ui.TextInput(c, &draft.domain).Label("Cookie domain").Placeholder("example.com").FillWidth()
					ui.Checkbox(c, &draft.subdomains, "Include subdomains").FontSize(11)
				})
				row("Path", func() { ui.TextInput(c, &draft.path).Label("Cookie path").Placeholder("/").FillWidth() })
				row("Expires", func() {
					if ui.Checkbox(c, &draft.session, "Session cookie").Changed() && !draft.session && draft.expires == "" {
						draft.expires = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
					}
					if !draft.session {
						ui.TextInput(c, &draft.expires).Label("Cookie expiration").Placeholder("YYYY-MM-DDTHH:MM:SSZ").FillWidth()
					}
				})
				row("Size", func() { ui.Text(c, fmt.Sprintf("%d bytes", len(draft.name)+len(draft.value))).FontSize(12) })
				row("HTTP Only", func() { ui.Checkbox(c, &draft.httpOnly, "").Label("Cookie HTTP Only") })
				row("Secure", func() {
					ui.Checkbox(c, &draft.secure, "").Label("Cookie Secure").Tooltip("Sent over HTTPS, or to localhost and loopback addresses.")
				})
				row("Same Site", func() {
					selected := cmp.Or(draft.sameSite, "Unspecified")
					if ui.Select(c, &selected, []string{"Unspecified", "Lax", "Strict", "None"}).Label("Cookie Same Site").Width(180).Changed() {
						draft.sameSite = selected
						if selected == "Unspecified" {
							draft.sameSite = ""
						}
					}
				})
			} else {
				for _, field := range []struct{ name, value string }{{"Name", s(cookie, "name")}, {"Value", s(cookie, "value")}, {"Domain", cookieDomainText(cookie)}, {"Path", s(cookie, "path")}, {"Expires", cookieExpiryText(cookie)}, {"Size", fmt.Sprintf("%d bytes", len(s(cookie, "name"))+len(s(cookie, "value")))}, {"HTTP Only", yesNo(b(cookie, "httpOnly"))}, {"Secure", yesNo(b(cookie, "secure"))}, {"Same Site", cmp.Or(s(cookie, "sameSite"), "Unspecified")}} {
					row(field.name, func() { ui.Text(c, field.value).Font("monospace").FontSize(12).Selectable() })
				}
			}
		})
	})
}
func (a *App) saveCookie(manager *cookieManager) {
	if a.deferUntilInputs(func() { a.saveCookie(manager) }) {
		return
	}
	if manager.draft == nil {
		return
	}
	value, err := manager.draft.object()
	if err != nil {
		manager.error = err.Error()
		return
	}
	id, key := manager.jar, manager.draft.oldKey
	a.changeCookieManager(manager, func() (engine.Object, error) { return a.Engine.EditCookie(a.ctx, id, key, value) }, engine.CookieKey(value))
}
func (a *App) changeCookieManager(manager *cookieManager, operation func() (engine.Object, error), selected string) {
	manager.busy = true
	manager.error = ""
	a.run(func() (func(), error) {
		model, err := operation()
		return func() {
			manager.busy = false
			if err != nil {
				manager.error = err.Error()
				return
			}
			a.applyModel(model)
			manager.selected = selected
			manager.draft = nil
			manager.focusTable = true
		}, nil
	})
}

func (a *App) responseCookies(c *ui.Context, p colors, response engine.Object) {
	sent, received := engine.ResponseCookies(response)
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(12).Gap(12).Children(func() {
		for i, section := range []struct {
			title  string
			values []engine.Object
		}{{"Sent cookies", sent}, {"Received cookies", received}} {
			ui.Column(c).Key(i).Gap(8).Children(func() {
				ui.Text(c, section.title).FontSize(13).FontWeight(600)
				if len(section.values) == 0 {
					ui.Text(c, "None").FontSize(12).TextColor(p.muted)
				}
				for n, cookie := range section.values {
					ui.Column(c).Key(n).Padding(9).Gap(6).Border(1, p.border).Radius(4).Children(func() {
						if s(cookie, "error") != "" {
							ui.Text(c, s(cookie, "raw")).Font("monospace").FontSize(12).Selectable()
							ui.Text(c, s(cookie, "error")).FontSize(11).TextColor(p.red)
							return
						}
						ui.Row(c).Gap(8).Children(func() {
							ui.Text(c, s(cookie, "name")).Font("monospace").FontSize(12).FontWeight(600).Grow(1)
							if b(cookie, "deleted") {
								ui.Text(c, "Deleted").TextColor(p.orange).FontSize(11)
							}
							if smallIconButton(c, "copy", "Copy cookie "+s(cookie, "name")).Clicked() {
								c.WriteClipboard(s(cookie, "value"))
							}
						})
						ui.Text(c, s(cookie, "value")).Font("monospace").FontSize(12).Selectable()
						if i == 1 {
							for _, field := range []struct{ key, label string }{{"domain", "Domain"}, {"path", "Path"}, {"expires", "Expires"}, {"maxAge", "Max-Age"}, {"sameSite", "Same Site"}} {
								if value := s(cookie, field.key); value != "" {
									ui.Row(c).Gap(8).Children(func() {
										ui.Text(c, field.label).Width(78).TextColor(p.muted).FontSize(11)
										ui.Text(c, value).Font("monospace").FontSize(11).Selectable()
									})
								}
							}
							flags := []string{}
							if b(cookie, "secure") {
								flags = append(flags, "Secure")
							}
							if b(cookie, "httpOnly") {
								flags = append(flags, "HTTP Only")
							}
							if len(flags) > 0 {
								ui.Text(c, strings.Join(flags, " · ")).FontSize(11).TextColor(p.muted)
							}
						}
					})
				}
			})
		}
	})
}
