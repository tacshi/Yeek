package desktop

import (
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// openFolder shows a folder's page in place of a request, as Yaak does
// after duplicating a folder.
func (a *App) openFolder(id string) {
	if a.models[id] == nil {
		return
	}
	a.saveActive()
	a.activeFolder = id
	a.tree.selected, a.tree.anchor, a.tree.last = []string{id}, id, id
	a.revealInSidebar(id)
}

// folderLayout is Yaak's FolderLayout: the folder's name with Send All,
// over a card for each of its children.
func (a *App) folderLayout(c *ui.Context, p colors, folder engine.Object) {
	id := s(folder, "id")
	page := ui.Scroll(c).Fill().Padding(16, 24, 24, 24)
	width := page.Bounds().W
	if width == 0 {
		// Its first frame: the window less the sidebar.
		width, _ = c.Size()
		if !a.hideSidebar {
			width -= float32(a.sidebarWidth)
		}
	}
	columns := 1
	if w := width - 48; w >= 896 {
		columns = 3
	} else if w >= 512 {
		columns = 2
	}
	page.Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			icon(c, "folder").FontSize(22).TextColor(p.muted)
			ui.Text(c, requestName(folder)).FontSize(22).FontWeight(600).SingleLine().Grow(1).MinWidth(0)
			send := ui.ButtonBase(c).Label("Send All").Height(26).Padding(0, 10).Gap(6).Radius(4).Border(1, p.border)
			if send.Hovered() {
				send.Background(p.border.Alpha(.3))
			}
			send.Children(func() {
				ui.Text(c, "Send All").FontSize(12)
				icon(c, "send").FontSize(12).TextColor(p.muted)
			})
			if send.Clicked() {
				a.sendFolder(id)
			}
		})
		ui.Box(c).Height(1).FillWidth().Background(p.border).Margin(12, 0, 32, 0)
		ui.Grid(c).Columns(columns).Gap(16).FillWidth().Children(func() {
			for _, child := range a.folderChildren(id) {
				a.folderCard(c, p, child)
			}
		})
	})
}

// folderChildren lists a folder's folders, then its requests, as Yaak's
// FolderLayout does.
func (a *App) folderChildren(id string) []engine.Object {
	var folders, requests []engine.Object
	for _, m := range a.treeItems(id) {
		if s(m, "model") == "folder" {
			folders = append(folders, m)
		} else {
			requests = append(requests, m)
		}
	}
	return append(folders, requests...)
}

func (a *App) folderCard(c *ui.Context, p colors, child engine.Object) {
	id := s(child, "id")
	ui.Column(c).Key(id).MinWidth(0).Padding(4, 12, 12, 12).Gap(12).Radius(8).Border(1, p.border).Background(p.border.Alpha(.18)).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if s(child, "model") == "folder" {
				icon(c, "folder").FontSize(16)
			}
			ui.Text(c, requestName(child)).FontSize(16).FontWeight(600).SingleLine().Grow(1).MinWidth(0)
			if smallIconButton(c, "external", "Open "+requestName(child)).Clicked() {
				a.openRequest(id)
			}
			if smallIconButton(c, "send", "Send "+requestName(child)).Clicked() {
				a.sendRequests([]string{id})
			}
		})
		switch s(child, "model") {
		case "folder":
			if ui.PrimaryButton(c, "Open").AlignSelf(ui.Start).Clicked() {
				a.openFolder(id)
			}
		case "http_request":
			a.httpRequestCard(c, p, child)
		default:
			ui.Text(c, "TODO "+id).FontSize(13).TextColor(p.muted)
		}
	})
}

func (a *App) httpRequestCard(c *ui.Context, p colors, request engine.Object) {
	id := s(request, "id")
	ui.Text(c, requestMethod(request)+" "+s(request, "url")).Font("monospace").FontSize(12).TextColor(p.blue).SingleLine().FillWidth().Padding(2, 10).Radius(3).Border(1, p.blue)
	response := a.responses[id]
	if response == nil {
		ui.Text(c, "No Responses").FontSize(13).TextColor(p.muted)
		return
	}
	summary := ui.ButtonBase(c).Label("Response Preview").Height(24).Padding(0, 6).Gap(8).Radius(3).Border(1, p.border).AlignSelf(ui.Start)
	summary.Children(func() {
		if s(response, "state") != "closed" {
			ui.Spinner(c).Size(12, 12).TextColor(p.subtle)
		}
		mono := func(text string, color ui.Color) {
			ui.Text(c, text).Font("monospace").FontSize(12).TextColor(color).SingleLine()
		}
		mono(statusLabel(response, false), statusColor(response, p))
		mono("•", p.subtle)
		mono(formatMillis(n(response, "elapsed")), p.muted)
		mono("•", p.subtle)
		mono(sizeText(n(response, "contentLength")), p.muted)
	})
	if summary.Clicked() {
		a.prompt("response_preview", "Response Preview", "", id)
	}
}

// responsePreview is the response of a folder page's request, in a dialog.
func (a *App) responsePreview(c *ui.Context, p colors) {
	d := a.drafts[a.dialogID]
	if d == nil {
		m := a.models[a.dialogID]
		if m == nil {
			return
		}
		if a.previewDraft == nil || a.previewDraft.ID != a.dialogID {
			a.previewDraft = newDraft(m)
		}
		d = a.previewDraft
	}
	ui.Box(c).Height(560).Padding(12).Children(func() { a.responsePane(c, p, d) })
}
