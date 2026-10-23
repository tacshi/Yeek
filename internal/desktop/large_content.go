package desktop

import (
	"mime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

const largeBytes = 2 * 1000 * 1000

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

func (a *App) confirmLargeResponse(c *ui.Context, p colors, response engine.Object) bool {
	id := s(response, "id")
	if n(response, "contentLength") <= largeBytes || a.revealedLarge[id] {
		return true
	}
	largeBanner(c, p, locales.ShowingResponsesOver(), locales.MayImpactPerformance(), nil, func() {
		if xsButton(c, p, locales.RevealResponse(), true).Clicked() {
			a.revealedLarge[id] = true
		}
		if xsButton(c, p, locales.SaveToFile(), false).Clicked() {
			a.saveResponse(response)
		}
		if probablyTextContentType(responseMIME(response)) && xsButton(c, p, locales.Copy(), false).Clicked() {
			a.copyBody(id)
		}
	})
	return false
}

func (a *App) confirmLargeResponseRequest(c *ui.Context, p colors, response engine.Object) bool {
	key := s(response, "id") + ".request"
	if n(response, "requestContentLength") <= largeBytes || a.revealedLarge[key] {
		return true
	}
	largeBanner(c, p, locales.ShowingContentOver(), locales.MayImpactPerformance(), nil, func() {
		if xsButton(c, p, locales.RevealRequestBody(), true).Clicked() {
			a.revealedLarge[key] = true
		}
		if probablyTextContentType(responseMIME(response)) && xsButton(c, p, locales.Copy(), false).Clicked() {
			a.copyBody(key)
		}
	})
	return false
}

func (a *App) confirmLargeRequestBody(c *ui.Context, p colors, d *Draft) bool {
	key := d.ID + ".body"
	if len(d.Body) <= largeBytes || a.revealedLarge[key] {
		return true
	}
	largeBanner(c, p, locales.RenderingContentOver(), locales.MayImpactPerformanceLower(), nil, func() {
		if xsButton(c, p, locales.RevealBody(), true).Clicked() {
			a.revealedLarge[key] = true
		}
		if xsButton(c, p, locales.DeleteBody(), false).TextColor(p.red).Border(1, p.red.Alpha(.6)).Clicked() {
			a.prompt("delete_body", locales.DeleteBodyText(), "", d.ID)
		}
	})
	return false
}

func (a *App) copyBody(id string) {
	a.background(func() (func(), error) {
		data, err := a.Engine.Body(id)
		if err == nil {
			mygo.Clipboard.WriteText(string(data))
		}
		return nil, err
	})
}

func (a *App) deleteBodyDialog(c *ui.Context, p colors) {
	ui.Column(c).Padding(20).Gap(18).Children(func() {
		ui.Text(c, locales.AreYouSureYouWantTo())
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				a.dialogOpen = false
			}
			if ui.PrimaryButton(c, locales.DeleteBody()).Label(locales.ConfirmDeleteBody()).Background(p.red).Clicked() {
				if d := a.drafts[a.dialogID]; d != nil {
					d.Body, d.Dirty = "", true
				}
				a.dialogOpen = false
			}
		})
	})
}
