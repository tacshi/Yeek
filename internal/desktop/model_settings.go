package desktop

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/locales"
)

type requestSetting struct {
	key                string
	title, description func() string
	kind               string
	models             []string
	fallback           any
}

var (
	settingRequestTimeout       = requestSetting{"settingRequestTimeout", locales.RequestTimeout, locales.MaximumRequestDurationInMillisecondsSet, "integer", []string{"workspace", "folder", "http_request"}, 0.0}
	settingRequestMessageSize   = requestSetting{"settingRequestMessageSize", locales.MessageSizeLimit, locales.MaximumGRPCOrWebSocketMessageSize, "megabytes", []string{"workspace", "folder", "websocket_request", "grpc_request"}, float64(64 << 20)}
	settingValidateCertificates = requestSetting{"settingValidateCertificates", locales.ValidateTLSCertificates, locales.WhenDisabledSkipValidationOfServer, "boolean", []string{"workspace", "folder", "http_request", "websocket_request", "grpc_request"}, true}
	settingFollowRedirects      = requestSetting{"settingFollowRedirects", locales.FollowRedirects, locales.FollowHTTPRedirectsAutomatically, "boolean", []string{"workspace", "folder", "http_request"}, true}
	settingHTTPVersion          = requestSetting{"settingHttpVersion", locales.HTTPVersionTitle, locales.ForceHTTP11OrHTTP, "httpVersion", []string{"workspace", "folder", "http_request"}, "auto"}
	settingSendCookies          = requestSetting{"settingSendCookies", locales.AutomaticallySendCookies, locales.AttachMatchingCookiesFromTheActive, "boolean", []string{"workspace", "folder", "http_request", "websocket_request"}, true}
	settingStoreCookies         = requestSetting{"settingStoreCookies", locales.AutomaticallyStoreCookies, locales.SaveCookiesFromSetCookieResponse, "boolean", []string{"workspace", "folder", "http_request", "websocket_request"}, true}
)

var httpVersionValues = []string{"auto", "http1", "http2"}

func httpVersionLabels() []string { return []string{locales.Automatic(), "HTTP/1.1", "HTTP/2"} }

func (r requestSetting) supports(kind string) bool { return slices.Contains(r.models, kind) }

func settingsSection(c *ui.Context, p colors, key, title string, rows func()) {
	ui.Column(c).Key(key).FillWidth().Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(13).TextColor(p.muted).Padding(0, 0, 8, 0).FillWidth().BorderWidth(0, 0, 1, 0).BorderColor(p.border)
		}
		rows()
	})
}

func settingRow(c *ui.Context, p colors, title, description string, reset func(), control func()) {
	ui.Row(c).Key(title).FillWidth().Padding(14, 0).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, title).FontSize(13)
				if reset != nil && smallIconButton(c, "undo", locales.ResetOverride()).Clicked() {
					reset()
				}
			})
			if description != "" {
				ui.Text(c, description).FontSize(12).TextColor(p.muted).MaxLines(3)
			}
		})
		ui.Row(c).Shrink(0).Gap(6).Children(control)
	})
}

const settingControlWidth = 160

func (a *App) modelSettingsEditor(c *ui.Context, p colors, d *Draft, showSectionTitles bool) {
	kind := d.Kind
	requests := []requestSetting{settingRequestTimeout, settingRequestMessageSize, settingValidateCertificates, settingFollowRedirects, settingHTTPVersion}
	cookies := []requestSetting{settingSendCookies, settingStoreCookies}
	section := func(title string, settings []requestSetting) {
		supported := slices.DeleteFunc(slices.Clone(settings), func(r requestSetting) bool { return !r.supports(kind) })
		if len(supported) == 0 {
			return
		}
		name := title
		if !showSectionTitles {
			title = ""
		}
		settingsSection(c, p, name, title, func() {
			for _, setting := range supported {
				a.requestSettingRow(c, p, d, setting)
			}
		})
	}
	section(locales.Requests(), requests)
	section(locales.Cookies(), cookies)
}

func (a *App) requestSettingRow(c *ui.Context, p colors, d *Draft, setting requestSetting) {
	plain := d.Kind == "workspace"
	value, overridden := setting.fallback, false
	if plain {
		if v, ok := d.Model[setting.key]; ok && v != nil {
			value = v
		}
	} else {
		override := o(d.Model, setting.key)
		overridden = b(override, "enabled")
		if overridden {
			value = override["value"]
		} else if inherited := a.inheritedSetting(d, setting.key); inherited != nil {
			value = inherited
		}
	}
	set := func(v any) {
		if plain {
			d.Model[setting.key] = v
		} else {
			d.Model[setting.key] = engine.Object{"enabled": true, "value": v}
		}
		d.Dirty = true
	}
	var reset func()
	if overridden {
		reset = func() {
			override := deepCopy(o(d.Model, setting.key))
			override["enabled"] = false
			d.Model[setting.key] = override
			d.Dirty = true
		}
	}
	settingRow(c, p, setting.title(), setting.description(), reset, func() {
		switch setting.kind {
		case "boolean":
			on, _ := value.(bool)
			if ui.Checkbox(c, &on, "").Label(locales.EnableSetting(setting.title())).Changed() {
				set(on)
			}
		case "integer":
			number := n(engine.Object{"v": value}, "v")
			placeholder := strconv.FormatFloat(n(engine.Object{"v": setting.fallback}, "v"), 'f', -1, 64)
			if text, ok := numberUnitInput(c, p, strconv.FormatFloat(number, 'f', -1, 64), locales.SettingSetting(setting.title()), "ms", placeholder, validInteger); ok {
				set(parseNumber(text, math.Trunc))
			}
		case "megabytes":
			bytes := n(engine.Object{"v": value}, "v")
			if text, ok := numberUnitInput(c, p, formatMegabytes(bytes), locales.SettingSetting(setting.title()), "MB", "0", validMegabytes); ok {
				set(parseNumber(text, func(mb float64) float64 { return math.Round(mb * bytesPerMB) }))
			}
		case "httpVersion":
			version, _ := value.(string)
			labels := httpVersionLabels()
			label := labels[max(0, slices.Index(httpVersionValues, version))]
			if ui.Select(c, &label, labels).Label(locales.SettingSetting(setting.title())).Width(settingControlWidth).Changed() {
				set(httpVersionValues[slices.Index(labels, label)])
			}
		}
	})
}

const (
	bytesPerMB       = 1 << 20
	maxMessageSizeMB = 2147483647.0 / bytesPerMB
)

func numberUnitInput(c *ui.Context, p colors, value, label, unit, placeholder string, valid func(string) bool) (string, bool) {
	box := ui.Row(c).Key(label).Width(192).Height(28).Radius(4).Background(p.background).AlignItems(ui.Stretch)
	text := ui.Local(box, "text", func() string { return value })
	edited := ui.Local(box, "edited", func() bool { return false })
	changed := false
	border := p.border
	if *edited && !valid(*text) {
		border = p.red
	}
	box.Border(1, border).Children(func() {
		in := ui.TextInputBase(c, text).Label(label).Placeholder(placeholder).Grow(1).MinWidth(0).Padding(0, 8).FontSize(12)
		if in.Changed() {
			changed, *edited = true, true
		}
		if !in.Focused() && !changed {
			*text, *edited = value, false
		}
		ui.Row(c).Padding(0, 8).AlignItems(ui.Center).BorderWidth(0, 0, 0, 1).BorderColor(p.border).Children(func() {
			ui.Text(c, unit).FontSize(11).FontWeight(500).TextColor(p.muted)
		})
	})
	return *text, changed
}

func validInteger(s string) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return s == "" || err == nil && v == math.Trunc(v) && v >= 0
}

func validMegabytes(s string) bool {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return s == "" || err == nil && !math.IsInf(v, 0) && v >= 0 && v <= maxMessageSizeMB
}

func parseNumber(s string, convert func(float64) float64) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(v, 0) {
		v = 0
	}
	return convert(v)
}

func formatMegabytes(bytes float64) string {
	mb := bytes / bytesPerMB
	if mb == math.Trunc(mb) {
		return strconv.FormatFloat(mb, 'f', -1, 64)
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(mb, 'f', 3, 64), "0"), ".")
}
