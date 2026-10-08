package desktop

import (
	"math"
	"slices"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// requestSetting mirrors Yaak's request setting definitions: which models
// have the setting, its title and description, and the kind of control.
type requestSetting struct {
	key, title, description, kind string
	models                        []string
	fallback                      any
}

var (
	settingRequestTimeout       = requestSetting{"settingRequestTimeout", "Request Timeout", "Maximum request duration in milliseconds. Set to 0 to disable.", "integer", []string{"workspace", "folder", "http_request"}, 0.0}
	settingRequestMessageSize   = requestSetting{"settingRequestMessageSize", "Message Size Limit", "Maximum gRPC or WebSocket message size in MB. Set to 0 to disable.", "megabytes", []string{"workspace", "folder", "websocket_request", "grpc_request"}, float64(64 << 20)}
	settingValidateCertificates = requestSetting{"settingValidateCertificates", "Validate TLS certificates", "When disabled, skip validation of server certificates.", "boolean", []string{"workspace", "folder", "http_request", "websocket_request", "grpc_request"}, true}
	settingFollowRedirects      = requestSetting{"settingFollowRedirects", "Follow redirects", "Follow HTTP redirects automatically.", "boolean", []string{"workspace", "folder", "http_request"}, true}
	settingHTTPVersion          = requestSetting{"settingHttpVersion", "HTTP version", "Force HTTP/1.1 or HTTP/2 for servers that don't negotiate the version correctly.", "httpVersion", []string{"workspace", "folder", "http_request"}, "auto"}
	settingSendCookies          = requestSetting{"settingSendCookies", "Automatically send cookies", "Attach matching cookies from the active cookie jar to outgoing requests.", "boolean", []string{"workspace", "folder", "http_request", "websocket_request"}, true}
	settingStoreCookies         = requestSetting{"settingStoreCookies", "Automatically store cookies", "Save cookies from Set-Cookie response headers to the active cookie jar.", "boolean", []string{"workspace", "folder", "http_request", "websocket_request"}, true}
)

var httpVersionLabels, httpVersionValues = []string{"Automatic", "HTTP/1.1", "HTTP/2"}, []string{"auto", "http1", "http2"}

func (r requestSetting) supports(kind string) bool { return slices.Contains(r.models, kind) }

// settingsSection is Yaak's SettingsSection: an optional title over rows
// separated by rules. key tells sections apart when their titles are hidden.
func settingsSection(c *ui.Context, p colors, key, title string, rows func()) {
	ui.Column(c).Key(key).FillWidth().Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(13).TextColor(p.muted).Padding(0, 0, 8, 0).FillWidth().BorderWidth(0, 0, 1, 0).BorderColor(p.border)
		}
		rows()
	})
}

// settingRow is Yaak's SettingRow: a title and description on the left and
// the control on the right, over a rule. reset, when set, shows the button
// that removes an override next to the title.
func settingRow(c *ui.Context, p colors, title, description string, reset func(), control func()) {
	ui.Row(c).Key(title).FillWidth().Padding(14, 0).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, title).FontSize(13)
				if reset != nil && smallIconButton(c, "undo", "Reset override").Clicked() {
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

// settingControlWidth keeps the controls of a settings list in one column.
const settingControlWidth = 160

// modelSettingsEditor is Yaak's ModelSettingsEditor for a workspace, folder or
// request draft. A workspace holds plain values; folders and requests hold
// overrides ({enabled, value}) that show the inherited value until changed.
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
	section("Requests", requests)
	section("Cookies", cookies)
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
	settingRow(c, p, setting.title, setting.description, reset, func() {
		switch setting.kind {
		case "boolean":
			on, _ := value.(bool)
			if ui.Checkbox(c, &on, "").Label("Enable " + setting.title).Changed() {
				set(on)
			}
		case "integer":
			number := n(engine.Object{"v": value}, "v")
			if ui.NumberInput(c, &number, 0, 3600000, 1).Label(setting.title + " setting").Width(settingControlWidth).Changed() {
				set(math.Round(number))
			}
		case "megabytes":
			megabytes := n(engine.Object{"v": value}, "v") / (1 << 20)
			if ui.NumberInput(c, &megabytes, 0, 2047, 1).Label(setting.title + " setting").Width(settingControlWidth).Changed() {
				set(math.Round(megabytes * (1 << 20)))
			}
		case "httpVersion":
			version, _ := value.(string)
			label := httpVersionLabels[max(0, slices.Index(httpVersionValues, version))]
			if ui.Select(c, &label, httpVersionLabels).Label(setting.title + " setting").Width(settingControlWidth).Changed() {
				set(httpVersionValues[slices.Index(httpVersionLabels, label)])
			}
		}
	})
}
