package desktop

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

func timelineTabLabel(d *Draft) string { return locales.Timeline() }

func timelineMenu(m *ui.Menu, d *Draft) {
	if m.Item(locales.Timeline()).Checked(!d.TimelineText).Chosen() {
		d.TimelineText = false
	}
	if m.Item(locales.TimelineText()).Checked(d.TimelineText).Chosen() {
		d.TimelineText = true
	}
}

func (a *App) responseEvents(response engine.Object) []engine.Object {
	events := []engine.Object{}
	for _, m := range a.list("http_response_event") {
		if s(m, "responseId") == s(response, "id") {
			events = append(events, m)
		}
	}
	slices.SortStableFunc(events, func(x, y engine.Object) int { return strings.Compare(s(x, "createdAt"), s(y, "createdAt")) })
	return events
}

type eventDisplay struct {
	glyph, label, summary string
	color                 ui.Color
}

func timelineDisplay(e engine.Object, p colors) eventDisplay {
	switch s(e, "type") {
	case "setting":
		source := s(e, "source_model")
		if source == "default" || source == "workspace" {
			source = ""
		}
		summary := s(e, "name") + " = " + s(e, "value")
		if source != "" {
			summary += " (" + source + ")"
		}
		return eventDisplay{"gear", locales.Setting(), summary, p.muted}
	case "info":
		return eventDisplay{"info", locales.Info(), s(e, "message"), p.muted}
	case "redirect":
		dropped := []string{}
		if b(e, "dropped_body") {
			dropped = append(dropped, locales.DropBody())
		}
		if headers := strs(e, "dropped_headers"); len(headers) > 0 {
			dropped = append(dropped, locales.DropCountHeaders(len(headers)))
		}
		summary := locales.RedirectingStatusUrl(fmt.Sprintf("%.0f", n(e, "status")), s(e, "url"))
		if len(dropped) > 0 {
			summary += " (" + strings.Join(dropped, ", ") + ")"
		}
		return eventDisplay{"redirect", locales.Redirect(), summary, p.green}
	case "send_url":
		return eventDisplay{"arrowUp", locales.Request(), s(e, "method") + " " + eventPath(e), p.accent}
	case "receive_url":
		return eventDisplay{"arrowDown", locales.Response(), s(e, "version") + " " + s(e, "status"), p.blue}
	case "header_up":
		return eventDisplay{"arrowUp", locales.Header(), s(e, "name") + ": " + s(e, "value"), p.accent}
	case "header_down":
		return eventDisplay{"arrowDown", locales.Header(), s(e, "name") + ": " + s(e, "value"), p.blue}
	case "chunk_sent":
		return eventDisplay{"info", locales.Chunk(), locales.SizeChunkSent(formatBytes(n(e, "bytes"))), p.muted}
	case "chunk_received":
		return eventDisplay{"info", locales.Chunk(), locales.SizeChunkReceived(formatBytes(n(e, "bytes"))), p.muted}
	case "dns_resolved":
		addresses := strings.Join(strs(e, "addresses"), ", ")
		if b(e, "overridden") {
			return eventDisplay{"globe", locales.DNSOverride(), locales.HostAddressesOverridden(s(e, "hostname"), addresses), p.green}
		}
		return eventDisplay{"globe", "DNS", fmt.Sprintf("%s → %s (%.0fms)", s(e, "hostname"), addresses, n(e, "duration")), p.muted}
	}
	return eventDisplay{"info", locales.Unknown(), locales.UnknownEvent(), p.muted}
}

func eventPath(e engine.Object) string {
	path := s(e, "path")
	if q := s(e, "query"); q != "" {
		path += "?" + q
	}
	if f := s(e, "fragment"); f != "" {
		path += "#" + f
	}
	return path
}

func strs(m engine.Object, key string) []string {
	out := []string{}
	for _, v := range array(m, key) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func array(m engine.Object, key string) []any {
	v, _ := m[key].([]any)
	return v
}

func formatBytes(bytes float64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%.0f B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", bytes/1024)
	}
	return fmt.Sprintf("%.1f MB", bytes/(1024*1024))
}

func timelineText(e engine.Object, prefix bool) string {
	mark, text := "*", ""
	switch s(e, "type") {
	case "send_url":
		mark, text = ">", s(e, "method")+" "+eventPath(e)
	case "receive_url":
		mark, text = "<", s(e, "version")+" "+s(e, "status")
	case "header_up":
		mark, text = ">", s(e, "name")+": "+s(e, "value")
	case "header_down":
		mark, text = "<", s(e, "name")+": "+s(e, "value")
	case "redirect":
		behavior := "preserve"
		if s(e, "behavior") == "drop_body" {
			behavior = "drop body"
		}
		dropped := []string{}
		if b(e, "dropped_body") {
			dropped = append(dropped, "body dropped")
		}
		if headers := strs(e, "dropped_headers"); len(headers) > 0 {
			dropped = append(dropped, "headers dropped: "+strings.Join(headers, ", "))
		}
		text = fmt.Sprintf("Redirect %.0f -> %s (%s", n(e, "status"), s(e, "url"), behavior)
		if len(dropped) > 0 {
			text += ", " + strings.Join(dropped, ", ")
		}
		text += ")"
	case "setting":
		text = "Setting " + s(e, "name") + "=" + s(e, "value")
	case "info":
		text = s(e, "message")
	case "chunk_sent":
		text = "[" + formatBytes(n(e, "bytes")) + " sent]"
	case "chunk_received":
		text = "[" + formatBytes(n(e, "bytes")) + " received]"
	case "dns_resolved":
		if b(e, "overridden") {
			text = "DNS override " + s(e, "hostname") + " -> " + strings.Join(strs(e, "addresses"), ", ")
		} else {
			text = fmt.Sprintf("DNS resolved %s to %s (%.0fms)", s(e, "hostname"), strings.Join(strs(e, "addresses"), ", "), n(e, "duration"))
		}
	default:
		text = "[unknown event]"
	}
	if prefix {
		return mark + " " + text
	}
	return text
}

func eventTime(m engine.Object) string {
	if at, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", s(m, "createdAt"), time.UTC); err == nil {
		return at.Local().Format("15:04:05.000")
	}
	return ""
}

func (a *App) timelineView(c *ui.Context, p colors, d *Draft, response engine.Object) {
	events := a.responseEvents(response)
	if len(events) == 0 {
		emptyState(c, p, locales.NoEventsRecorded())
		return
	}
	if d.TimelineText {
		lines := make([]string, len(events))
		for i, m := range events {
			lines[i] = timelineText(o(m, "event"), true)
		}
		a.codeView(c, p, strings.Join(lines, "\n"), "text", "Timeline text")
		return
	}
	if d.TimelineIndex >= len(events) {
		d.TimelineIndex = -1
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		list := ui.Scroll(c).FillWidth().Padding(4, 12, 4, 0).Gap(1)
		if d.TimelineIndex >= 0 {
			list.Shrink(0).Height(220)
		} else {
			list.Grow(1).MinHeight(0)
		}
		list.Children(func() {
			for i, m := range events {
				display := timelineDisplay(o(m, "event"), p)
				row := ui.ButtonBase(c).Key(s(m, "id")).Label(display.label+" "+display.summary).FillWidth().Height(26).Padding(0, 8).Gap(8).Radius(4).Justify(ui.Start)
				if i == d.TimelineIndex {
					row.Background(p.border.Alpha(.55))
				} else if row.Hovered() {
					row.Background(p.border.Alpha(.3))
				}
				row.Children(func() {
					icon(c, display.glyph).FontSize(13).TextColor(display.color).Shrink(0)
					ui.Text(c, display.summary).Font("monospace").FontSize(12).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, eventTime(m)).Font("monospace").FontSize(11).TextColor(p.subtle).Shrink(0)
				})
				if row.Clicked() {
					if d.TimelineIndex == i {
						d.TimelineIndex = -1
					} else {
						d.TimelineIndex = i
					}
				}
			}
		})
		if d.TimelineIndex >= 0 {
			a.timelineDetails(c, p, d, events[d.TimelineIndex])
		}
	})
}

func (a *App) timelineDetails(c *ui.Context, p colors, d *Draft, m engine.Object) {
	e := o(m, "event")
	display := timelineDisplay(e, p)
	title := map[string]string{"header_up": locales.HeaderSent(), "header_down": locales.HeaderReceived(), "send_url": locales.Request(), "receive_url": locales.Response(), "redirect": locales.Redirect(), "setting": locales.ApplySetting(), "chunk_sent": locales.DataSent(), "chunk_received": locales.DataReceived()}[s(e, "type")]
	if s(e, "type") == "dns_resolved" {
		title = locales.DNSResolution()
		if b(e, "overridden") {
			title = locales.DNSOverride()
		}
	}
	if title == "" {
		title = display.label
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(10, 12, 0, 8).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600)
			ui.Text(c, eventTime(m)).Font("monospace").FontSize(11).TextColor(p.subtle)
			ui.Spacer(c)
			label := locales.Text()
			if d.TimelineRaw {
				label = locales.Formatted()
			}
			if ui.Button(c, label).FontSize(11).Clicked() {
				d.TimelineRaw = !d.TimelineRaw
			}
			if smallIconButton(c, "close", locales.CloseEventDetails()).Clicked() {
				d.TimelineIndex = -1
			}
		})
		if d.TimelineRaw {
			a.codeView(c, p, timelineText(e, false), "text", "Event text")
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Children(func() { keyValueRows(c, p, timelineFields(e, p)) })
	})
}

func timelineFields(e engine.Object, p colors) []keyValue {
	row := func(label, value string) keyValue { return keyValue{label: label, value: value, color: p.muted} }
	switch s(e, "type") {
	case "header_up", "header_down":
		return []keyValue{row(locales.Header(), s(e, "name")), row(locales.ValueTitle(), s(e, "value"))}
	case "send_url":
		port := n(e, "port")
		defaultPort := s(e, "scheme") == "http" && port == 80 || s(e, "scheme") == "https" && port == 443
		auth := ""
		if s(e, "username") != "" || s(e, "password") != "" {
			auth = s(e, "username") + ":" + s(e, "password") + "@"
		}
		portText := ""
		if !defaultPort {
			portText = fmt.Sprintf(":%.0f", port)
		}
		rows := []keyValue{row("URL", s(e, "scheme")+"://"+auth+s(e, "host")+portText+eventPath(e)), row(locales.Method(), s(e, "method")), row(locales.Scheme(), s(e, "scheme"))}
		if s(e, "username") != "" {
			rows = append(rows, row(locales.Username(), s(e, "username")))
		}
		if s(e, "password") != "" {
			rows = append(rows, row(locales.Password(), s(e, "password")))
		}
		rows = append(rows, row(locales.Host(), s(e, "host")))
		if !defaultPort {
			rows = append(rows, row(locales.Port(), fmt.Sprintf("%.0f", port)))
		}
		rows = append(rows, row(locales.Path(), s(e, "path")))
		if s(e, "query") != "" {
			rows = append(rows, row(locales.Query(), s(e, "query")))
		}
		if s(e, "fragment") != "" {
			rows = append(rows, row(locales.Fragment(), s(e, "fragment")))
		}
		return rows
	case "receive_url":
		return []keyValue{row(locales.HTTPVersion(), s(e, "version")), row(locales.Status(), s(e, "status"))}
	case "redirect":
		behavior := locales.PreserveMethodAndBody()
		if s(e, "behavior") == "drop_body" {
			behavior = locales.DropBodyChangeToGET()
		}
		dropped, headers := locales.No(), "--"
		if b(e, "dropped_body") {
			dropped = locales.Yes()
		}
		if h := strs(e, "dropped_headers"); len(h) > 0 {
			headers = strings.Join(h, ", ")
		}
		return []keyValue{row(locales.Status(), fmt.Sprintf("%.0f", n(e, "status"))), row(locales.Location(), s(e, "url")), row(locales.Behavior(), behavior), row(locales.BodyDropped(), dropped), row(locales.HeadersDropped(), headers)}
	case "setting":
		rows := []keyValue{row(locales.Setting(), s(e, "name")), row(locales.ValueTitle(), s(e, "value"))}
		if source := s(e, "source_model"); source != "" {
			label := strings.ReplaceAll(source, "_", " ")
			if source == "default" {
				label = locales.Default()
			} else if name := s(e, "source_name"); name != "" {
				label = name + " (" + label + ")"
			}
			rows = append(rows, row(locales.Source(), label))
		}
		return rows
	case "chunk_sent", "chunk_received":
		return []keyValue{row(locales.Size(), formatBytes(n(e, "bytes")))}
	case "dns_resolved":
		duration := fmt.Sprintf("%.0fms", n(e, "duration"))
		if b(e, "overridden") {
			duration = "--"
		}
		rows := []keyValue{row(locales.Hostname(), s(e, "hostname")), row(locales.Addresses(), strings.Join(strs(e, "addresses"), ", ")), row(locales.Duration(), duration)}
		if b(e, "overridden") {
			rows = append(rows, row(locales.Source(), locales.WorkspaceOverride()))
		}
		return rows
	}
	return []keyValue{row(locales.Event(), timelineDisplay(e, p).summary)}
}
