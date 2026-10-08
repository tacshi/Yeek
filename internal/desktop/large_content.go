package desktop

import (
	"mime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// largeBytes is the size above which Yaak asks before showing a body.
const largeBytes = 2 * 1000 * 1000

// largeBanner is Yaak's Banner over a ConfirmLarge… guard: what the size
// may cost, then its buttons.
func largeBanner(c *ui.Context, p colors, before, after string, extra, buttons func()) {
	ui.Column(c).Margin(8).Padding(12).Gap(12).Radius(6).Border(1, p.accent.Alpha(.5)).Background(p.accent.Alpha(.08)).Children(func() {
		ui.Row(c).Gap(4).Wrap().Children(func() {
			ui.Text(c, before).FontSize(13)
			ui.Text(c, sizeText(largeBytes)).Font("monospace").FontSize(12).Padding(1, 4).Radius(3).Background(p.border.Alpha(.5))
			ui.Text(c, after).FontSize(13)
		})
		if extra != nil {
			extra()
		}
		ui.Row(c).Gap(8).Wrap().Children(buttons)
	})
}

// probablyTextContentType is Yaak's isProbablyTextContentType.
func probablyTextContentType(contentType string) bool {
	kind, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		kind = contentType
	}
	kind = strings.ToLower(kind)
	if strings.HasPrefix(kind, "text/") {
		return true
	}
	for _, text := range []string{"application/json", "application/xml", "application/javascript", "application/yaml", "+json", "+xml", "+yaml", "+text"} {
		if strings.Contains(kind, text) {
			return true
		}
	}
	return false
}

func xsButton(c *ui.Context, p colors, label string, primary bool) ui.Element {
	if primary {
		return ui.PrimaryButton(c, label).FontSize(12).Padding(3, 10)
	}
	return ui.Button(c, label).FontSize(12).Padding(3, 10)
}

// confirmLargeResponse is Yaak's ConfirmLargeResponse: a response body
// over largeBytes shows once revealed.
func (a *App) confirmLargeResponse(c *ui.Context, p colors, response engine.Object) bool {
	id := s(response, "id")
	if n(response, "contentLength") <= largeBytes || a.revealedLarge[id] {
		return true
	}
	largeBanner(c, p, "Showing responses over", "may impact performance", nil, func() {
		if xsButton(c, p, "Reveal Response", true).Clicked() {
			a.revealedLarge[id] = true
		}
		if xsButton(c, p, "Save to File", false).Clicked() {
			a.saveResponse(response)
		}
		if probablyTextContentType(responseMIME(response)) && xsButton(c, p, "Copy", false).Clicked() {
			a.copyBody(id)
		}
	})
	return false
}

// confirmLargeResponseRequest is Yaak's ConfirmLargeResponseRequest, for
// the body that was sent.
func (a *App) confirmLargeResponseRequest(c *ui.Context, p colors, response engine.Object) bool {
	key := s(response, "id") + ".request"
	if n(response, "requestContentLength") <= largeBytes || a.revealedLarge[key] {
		return true
	}
	largeBanner(c, p, "Showing content over", "may impact performance", nil, func() {
		if xsButton(c, p, "Reveal Request Body", true).Clicked() {
			a.revealedLarge[key] = true
		}
		if probablyTextContentType(responseMIME(response)) && xsButton(c, p, "Copy", false).Clicked() {
			a.copyBody(key)
		}
	})
	return false
}

// confirmLargeRequestBody is Yaak's ConfirmLargeRequestBody, for a text
// body being edited.
func (a *App) confirmLargeRequestBody(c *ui.Context, p colors, d *Draft) bool {
	key := d.ID + ".body"
	if len(d.Body) <= largeBytes || a.revealedLarge[key] {
		return true
	}
	largeBanner(c, p, "Rendering content over", "may impact performance.", func() {
		ui.Row(c).Gap(4).Children(func() {
			ui.Text(c, "See").FontSize(13)
			ui.Link(c, "Working With Large Values", "https://feedback.yaak.app/en/help/articles/1198684-working-with-large-values").FontSize(13)
			ui.Text(c, "for tips.").FontSize(13)
		})
	}, func() {
		if xsButton(c, p, "Reveal Body", true).Clicked() {
			a.revealedLarge[key] = true
		}
		if xsButton(c, p, "Delete Body", false).TextColor(p.red).Border(1, p.red.Alpha(.6)).Clicked() {
			a.prompt("delete_body", "Delete Body Text", "", d.ID)
		}
	})
	return false
}

// copyBody copies a stored body in full, not its preview.
func (a *App) copyBody(id string) {
	a.background(func() (func(), error) {
		data, err := a.Engine.Body(id)
		if err == nil {
			mygo.Clipboard.WriteText(string(data))
		}
		return nil, err
	})
}

// deleteBodyDialog is Yaak's confirmation for deleting a request's body text.
func (a *App) deleteBodyDialog(c *ui.Context, p colors) {
	ui.Column(c).Padding(20).Gap(18).Children(func() {
		ui.Text(c, "Are you sure you want to delete the request body text?")
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen = false
			}
			if ui.PrimaryButton(c, "Delete Body").Label("Confirm Delete Body").Background(p.red).Clicked() {
				if d := a.drafts[a.dialogID]; d != nil {
					d.Body, d.Dirty = "", true
				}
				a.dialogOpen = false
			}
		})
	})
}
