package desktop

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/locales"
)

type pathListItem struct {
	path, icon string
	subtitle   func()
	failed     bool
}

type pathListAdd struct {
	label    string
	onSelect func()
}

func pathList(c *ui.Context, p colors, items []pathListItem, onRemove func(string), add []pathListAdd, addLabel, addHint string, hovering, disabled bool, extra func()) ui.Element {
	list := ui.Column(c).FillWidth().Gap(6).Padding(6).Margin(-6).Radius(8)
	if hovering {
		list.Background(p.border.Alpha(.35))
	}
	list.Children(func() {
		for _, item := range items {
			name, parent := splitPath(item.path)
			card := ui.Row(c).Key(item.path).FillWidth().Gap(12).Padding(8, 6, 8, 12).Radius(6).Background(p.border.Alpha(.25))
			iconColor, subtitleColor := p.muted, p.subtle
			if item.failed {
				card.Background(p.red.Alpha(.1))
				iconColor, subtitleColor = p.red, p.red
			}
			card.Tooltip(item.path).Children(func() {
				icon(c, item.icon).FontSize(16).TextColor(iconColor).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, name).FontSize(13).FontWeight(600).SingleLine()
					ui.Row(c).Gap(6).MinWidth(0).Children(func() {
						switch {
						case item.subtitle != nil:
							item.subtitle()
						case parent != "":
							ui.Text(c, parent).Font("monospace").FontSize(11).TextColor(subtitleColor).SingleLine().MinWidth(0)
						}
					})
				})
				if smallIconButton(c, "trash", locales.RemoveName(name)).TextColor(p.muted).Disabled(disabled).Shrink(0).Clicked() {
					onRemove(item.path)
				}
			})
		}
		if extra != nil {
			extra()
		}
		border := p.border
		if hovering {
			border = p.notice
		}
		row := ui.ButtonBase(c).Label(addLabel).FillWidth().Height(32).Padding(0, 12).Gap(8).Radius(6).Border(1, border).BorderStyle(ui.BorderDashed).Justify(ui.Start).Disabled(disabled)
		if row.Hovered() && !disabled {
			row.Border(1, p.accent)
		}
		row.Children(func() {
			icon(c, "plus").FontSize(13).TextColor(p.subtle)
			if hovering {
				ui.Text(c, locales.DropToAdd()).FontSize(13).TextColor(p.muted)
				return
			}
			ui.Text(c, addLabel).FontSize(13).TextColor(p.muted)
			if addHint != "" {
				ui.Box(c).Grow(1)
				ui.Text(c, addHint).FontSize(11).TextColor(p.subtle)
			}
		})
		row.Menu(func(m *ui.Menu) {
			for _, item := range add {
				if m.Item(item.label).Chosen() {
					item.onSelect()
				}
			}
		})
	})
	return list
}

var urlScheme = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://`)

func splitPath(path string) (name, parent string) {
	if urlScheme.MatchString(path) {
		u, err := url.Parse(path)
		if err != nil {
			return path, ""
		}
		var segments []string
		for _, segment := range strings.Split(u.EscapedPath(), "/") {
			if segment != "" {
				segments = append(segments, segment)
			}
		}
		if len(segments) == 0 {
			return u.Host, ""
		}
		parent = strings.TrimSuffix(u.Host+"/"+strings.Join(segments[:len(segments)-1], "/"), "/")
		return segments[len(segments)-1], parent
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return path, ""
	}
	dirs := parts[:len(parts)-1]
	if len(dirs) > 3 {
		return parts[len(parts)-1], "…/" + strings.Join(dirs[len(dirs)-3:], "/")
	}
	return parts[len(parts)-1], strings.Join(dirs, "/")
}
