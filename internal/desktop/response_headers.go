package desktop

import (
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

func (a *App) detailsBanner(c *ui.Context, p colors, key string, defaultOpen bool, summary func(), body func()) {
	if a.detailsOpen == nil {
		a.detailsOpen = map[string]bool{}
	}
	open, ok := a.detailsOpen[key]
	if !ok {
		open = defaultOpen
	}
	ui.Column(c).Key(key).Shrink(0).Radius(6).Border(1, p.border).Background(p.border.Alpha(.18)).Padding(6, 10).Children(func() {
		toggle := ui.ButtonBase(c).Label(key).FillWidth().Height(24).Gap(10).Justify(ui.Start).Opacity(.75)
		toggle.Children(func() {
			disclosure(c, open, 11, p.muted)
			summary()
		})
		if toggle.Clicked() {
			a.detailsOpen[key] = !open
		}
		if open {
			ui.Column(c).Padding(6, 0, 4, 0).Children(body)
		}
	})
}

type keyValue struct {
	label, value string
	color        ui.Color
	right        func()
}

func keyValueRows(c *ui.Context, p colors, rows []keyValue) {
	longest := 0
	for _, row := range rows {
		longest = max(longest, utf8.RuneCountInString(row.label))
	}
	labelWidth := min(160, float32(longest)*7.3+8)
	ui.Column(c).FillWidth().Children(func() {
		for i, row := range rows {
			line := ui.Row(c).Key(i).Padding(3, 0).Gap(8).AlignItems(ui.Start)
			if i > 0 {
				line.BorderWidth(1, 0, 0, 0).BorderColor(p.border.Alpha(.6))
			}
			line.Children(func() {
				ui.Text(c, row.label).Font("monospace").FontSize(12).TextColor(row.color).Width(labelWidth).Shrink(0)
				value := ui.Text(c, row.value).Font("monospace").FontSize(12).MinWidth(0).Selectable()
				if row.right == nil {
					value.Grow(1)
					return
				}
				row.right()
				ui.Spacer(c)
			})
		}
	})
}

func sortedHeaders(headers []engine.Object) []engine.Object {
	sorted := slices.Clone(headers)
	slices.SortStableFunc(sorted, func(x, y engine.Object) int {
		return strings.Compare(strings.ToLower(s(x, "name")), strings.ToLower(s(y, "name")))
	})
	return sorted
}

func (a *App) responseHeaders(c *ui.Context, p colors, response engine.Object) {
	requestID := s(response, "requestId")
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(6, 12, 16, 0).Gap(12).Children(func() {
		a.detailsBanner(c, p, requestID+".general", false, func() { ui.Text(c, locales.Info()).FontSize(13) }, func() {
			sent := s(response, "createdAt")
			if at, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", sent, time.UTC); err == nil {
				sent = formatDate(at.Local(), "Jan 2, 2006, 3:04:05 PM MST", "yMMMdjmmssz")
			}
			dash := func(v string) string {
				if v == "" {
					return "--"
				}
				return v
			}
			address := s(response, "url")
			keyValueRows(c, p, []keyValue{
				{label: locales.Sent(), value: sent, color: p.muted},
				{label: locales.RequestURL(), value: address, color: p.muted, right: func() {
					if u, err := url.Parse(address); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
						if smallIconButton(c, "external", locales.OpenInBrowser()).Opacity(.6).Clicked() {
							c.OpenURL(address)
						}
					}
				}},
				{label: locales.RemoteAddress(), value: dash(s(response, "remoteAddr")), color: p.muted},
				{label: locales.Version(), value: dash(s(response, "version")), color: p.muted},
			})
		})
		section := func(key, title string, headers []engine.Object, color ui.Color, open bool) {
			a.detailsBanner(c, p, requestID+key, open, func() {
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, title).FontSize(13)
					badgePair(len(headers), 0).viewSingle(c, p)
				})
			}, func() {
				if len(headers) == 0 {
					ui.Text(c, locales.NoHeaders()).FontSize(12).Italic().TextColor(p.subtle)
					return
				}
				rows := make([]keyValue, len(headers))
				for i, h := range headers {
					rows[i] = keyValue{label: s(h, "name"), value: s(h, "value"), color: color}
				}
				keyValueRows(c, p, rows)
			})
		}
		section(".request_headers", locales.RequestHeaders(), sortedHeaders(oslice(response, "requestHeaders")), p.accent, false)
		section(".response_headers", locales.ResponseHeaders(), sortedHeaders(oslice(response, "headers")), p.blue, true)
	})
}
