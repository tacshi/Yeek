package desktop

import (
	"math"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func (a *App) inheritedSetting(d *Draft, key string) any {
	seen := map[string]bool{}
	for id := s(d.Model, "folderId"); id != "" && !seen[id]; {
		seen[id] = true
		m := a.models[id]
		if m == nil {
			break
		}
		if setting := o(m, key); b(setting, "enabled") {
			return setting["value"]
		}
		id = s(m, "folderId")
	}
	return a.models[s(d.Model, "workspaceId")][key]
}

// settingControlWidth keeps the controls of a settings list in one column.
const settingControlWidth = 160

// settingRow is a label on the left and its control on the right.
func settingRow(c *ui.Context, p colors, label string, control func()) {
	ui.Row(c).Key(label).MinHeight(30).Gap(10).Children(func() {
		ui.Text(c, label).Grow(1).MinWidth(0).FontSize(13).TextColor(p.text)
		control()
	})
}
func (a *App) numericRequestSetting(c *ui.Context, p colors, d *Draft, key, label string, scale, maximum float64) {
	setting := o(d.Model, key)
	on := b(setting, "enabled")
	inherited := n(engine.Object{"value": a.inheritedSetting(d, key)}, "value") / scale
	value := n(setting, "value") / scale
	if !on {
		value = inherited
	}
	settingRow(c, p, label, func() {
		if ui.Checkbox(c, &on, "Override").Label("Override " + label).FontSize(12).Changed() {
			d.Model[key] = engine.Object{"enabled": on, "value": math.Round(value * scale)}
			d.Dirty = true
		}
		minimum := 0.0
		if key == "settingRequestMessageSize" {
			minimum = 1 / scale
		}
		if ui.NumberInput(c, &value, minimum, maximum, 1).Label(label).Disabled(!on).Width(settingControlWidth).Changed() {
			d.Model[key] = engine.Object{"enabled": on, "value": math.Round(value * scale)}
			d.Dirty = true
		}
	})
}
func (a *App) networkRequestOverrides(c *ui.Context, p colors, d *Draft) {
	if d.Kind == "http_request" || d.Kind == "folder" {
		a.numericRequestSetting(c, p, d, "settingRequestTimeout", "Timeout (milliseconds)", 1, 3600000)
		setting := o(d.Model, "settingHttpVersion")
		label := "Inherit"
		labels := map[string]string{"auto": "Automatic", "http1": "HTTP/1.1", "http2": "HTTP/2"}
		if b(setting, "enabled") {
			label = labels[s(setting, "value")]
		}
		settingRow(c, p, "HTTP version", func() {
			if ui.Select(c, &label, []string{"Inherit", "Automatic", "HTTP/1.1", "HTTP/2"}).Label("Request HTTP version").Width(settingControlWidth).Changed() {
				version := "auto"
				for k, v := range labels {
					if v == label {
						version = k
					}
				}
				d.Model["settingHttpVersion"] = engine.Object{"enabled": label != "Inherit", "value": version}
				d.Dirty = true
			}
		})
	}
	if d.Kind == "grpc_request" || d.Kind == "websocket_request" || d.Kind == "folder" {
		a.numericRequestSetting(c, p, d, "settingRequestMessageSize", "Message limit (MiB)", 1<<20, float64(math.MaxInt32)/(1<<20))
	}
}
