package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type colors struct{ background, sidebar, header, response, border, text, muted, subtle, accent, green, orange, blue, red, notice ui.Color }

func (a *App) theme(c *ui.Context) colors {
	dark := c.Theme().Dark
	if s(a.settings, "appearance") == "dark" {
		dark = true
	}
	if s(a.settings, "appearance") == "light" {
		dark = false
	}
	p := colors{ui.Hex("#ffffff"), ui.Hex("#f9fafb"), ui.Hex("#f6f7fa"), ui.Hex("#fcfcfd"), ui.Hex("#e0e4eb"), ui.Hex("#141922"), ui.Hex("#526078"), ui.Hex("#8a95a7"), ui.Hex("#9133ff"), ui.Hex("#1d9142"), ui.Hex("#b86700"), ui.Hex("#007bcc"), ui.Hex("#d41f6e"), ui.Hex("#ad8100")}
	if dark {
		p = colors{ui.Hex("#1b1a2b"), ui.Hex("#201f32"), ui.Hex("#171626"), ui.Hex("#201f32"), ui.Hex("#35334e"), ui.Hex("#d1d0e2"), ui.Hex("#8b88a7"), ui.Hex("#696586"), ui.Hex("#c294ff"), ui.Hex("#02df72"), ui.Hex("#ff9438"), ui.Hex("#42aaff"), ui.Hex("#f76f9a"), ui.Hex("#ebcf5b")}
	}
	key := "themeLight"
	if dark {
		key = "themeDark"
	}
	selected := s(a.settings, key)
	for _, theme := range builtinThemes {
		if theme.ID == selected {
			p = applyPalette(p, theme.Colors)
			break
		}
	}
	for _, theme := range a.Engine.PluginThemes() {
		if "plugin:"+theme.Name == selected {
			p = applyPalette(p, theme.Colors)
			break
		}
	}
	t := *c.Theme()
	t.Dark = dark
	t.Background = p.background
	t.Surface = p.sidebar
	t.SurfaceHover = p.border
	t.SurfacePressed = p.border
	t.Border = p.border
	t.Text = p.text
	t.TextMuted = p.muted
	t.Accent = p.accent
	t.AccentHover = p.accent
	t.AccentPressed = p.accent
	t.AccentText = p.background
	t.Selection = p.accent.Alpha(.23)
	t.Focus = p.accent.Alpha(.55)
	t.Danger = p.red
	t.Success = p.green
	t.Warning = p.orange
	t.Radius = 5
	t.Spacing = 3
	t.FontSize = 13
	t.Inverse = p.border
	t.InverseText = p.text
	if size := n(a.settings, "interfaceFontSize"); size > 0 {
		t.FontSize = float32(size)
	}
	t.Font = s(a.settings, "interfaceFont")
	c.SetTheme(&t)
	return p
}

// methodColor follows Yaak's HttpMethodTag colors.
func methodColor(method string, p colors) ui.Color {
	switch strings.ToUpper(method) {
	case "GET":
		return p.accent
	case "POST":
		return p.green
	case "PUT":
		return p.orange
	case "PATCH":
		return p.notice
	case "DELETE":
		return p.red
	case "OPTIONS", "GRAPHQL", "GQL", "GRPC", "WEBSOCKET", "WS":
		return p.blue
	default:
		return p.muted
	}
}

var shortMethods = map[string]string{"GET": "GET", "PUT": "PUT", "POST": "POST", "PATCH": "PTCH", "DELETE": "DELE", "OPTIONS": "OPTN", "HEAD": "HEAD", "QUERY": "QURY", "GRAPHQL": "GQL", "GRPC": "GRPC", "WEBSOCKET": "WS"}

// shortMethod is Yaak's four-column sidebar method label.
func shortMethod(method string) string {
	method = strings.ToUpper(method)
	label, ok := shortMethods[method]
	if !ok {
		label = method[:min(4, len(method))]
	}
	return fmt.Sprintf("%-4s", label)
}

// requestMethod is the method label Yaak shows for any request model.
func requestMethod(m engine.Object) string {
	switch {
	case s(m, "model") == "grpc_request":
		return "GRPC"
	case s(m, "model") == "websocket_request":
		return "WEBSOCKET"
	case s(m, "bodyType") == "graphql":
		return "GRAPHQL"
	}
	return strings.ToUpper(s(m, "method"))
}

// statusColor and statusLabel follow Yaak's HttpStatusTag.
func statusColor(response engine.Object, p colors) ui.Color {
	status := n(response, "status")
	switch {
	case s(response, "state") == "initialized":
		return p.muted
	case status < 100:
		return p.red
	case status < 200:
		return p.blue
	case status < 300:
		return p.green
	case status < 400:
		return p.accent
	case status < 500:
		return p.orange
	}
	return p.red
}
func statusLabel(response engine.Object, short bool) string {
	switch {
	case s(response, "state") == "initialized":
		if short {
			return "CONN"
		}
		return "CONNECTING"
	case n(response, "status") < 100:
		if short {
			return "ERR"
		}
		return "ERROR"
	case short:
		return fmt.Sprintf("%.0f", n(response, "status"))
	}
	return strings.TrimSpace(fmt.Sprintf("%.0f %s", n(response, "status"), s(response, "statusReason")))
}

var iconPaths = map[string]string{
	"plus":       `<path d="M12 5v14M5 12h14"/>`,
	"chevron":    `<path d="m9 5 7 7-7 7"/>`,
	"down":       `<path d="m5 9 7 7 7-7"/>`,
	"folder":     `<path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3Z"/>`,
	"search":     `<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6"/>`,
	"gear":       `<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`,
	"send":       `<path d="m4 3 17 9-17 9 3-9Zm3 9h14"/>`,
	"close":      `<path d="m6 6 12 12M18 6 6 18"/>`,
	"copy":       `<rect x="8" y="8" width="12" height="13" rx="2"/><path d="M16 8V3H3v13h5"/>`,
	"download":   `<path d="M12 3v12m-5-5 5 5 5-5M4 17v4h16v-4"/>`,
	"sidebar":    `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>`,
	"split":      `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M12 4v16"/>`,
	"history":    `<path d="M3 10a9 9 0 1 1 1 8M3 4v6h6M12 7v6l4 2"/>`,
	"more":       `<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`,
	"braces":     `<path d="M8 3H6v6l-3 3 3 3v6h2M16 3h2v6l3 3-3 3v6h-2"/>`,
	"globe":      `<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c-6 6-6 12 0 18 6-6 6-12 0-18"/>`,
	"key":        `<circle cx="8" cy="8" r="5"/><path d="m12 12 9 9m-3-3 3-3m-6 0 3-3"/>`,
	"cookie":     `<path d="M21 12a9 9 0 1 1-9-9c-1 5 4 4 4 4s-1 5 5 5Z"/><path d="M8 8h.01M7 14h.01M12 17h.01M13 12h.01"/>`,
	"plusCircle": `<circle cx="12" cy="12" r="9"/><path d="M12 8v8M8 12h8"/>`,
	"panelOpen":  `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/><path d="m15 10-2 2 2 2"/>`,
	"panelShut":  `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/><path d="m13 10 2 2-2 2"/>`,
	"columns":    `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M12 4v16"/>`,
	"rows":       `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 12h18"/>`,
	"moreV":      `<circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/>`,
	"wrench":     `<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94Z"/>`,
	"crosshair":  `<circle cx="12" cy="12" r="9"/><path d="M22 12h-4M6 12H2M12 6V2M12 22v-4"/>`,
	"expand":     `<path d="m7 15 5 5 5-5M7 9l5-5 5 5"/>`,
	"collapse":   `<path d="m7 20 5-5 5 5M7 4l5 5 5-5"/>`,
	"save":       `<path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z"/><path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7M7 3v4a1 1 0 0 0 1 1h7"/>`,
	"filter":     `<path d="M22 3H2l8 9.46V19l4 2v-8.54Z"/>`,
	"eye":        `<path d="M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0"/><circle cx="12" cy="12" r="3"/>`,
	"pencil":     `<path d="M21.17 6.81a1 1 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5zM15 5l4 4"/>`,
	"branch":     `<circle cx="6" cy="4" r="2"/><circle cx="6" cy="20" r="2"/><circle cx="18" cy="6" r="2"/><path d="M6 6v12M18 8c0 6-12 4-12 10"/>`,
}
var icons = func() map[string]*ui.SVG {
	m := map[string]*ui.SVG{}
	for name, path := range iconPaths {
		m[name] = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
	}
	return m
}()

func icon(c *ui.Context, name string) *ui.Element { return ui.Icon(c, icons[name]).FontSize(16) }

// chevronToggle is the borderless disclosure arrow used by trees.
func chevronToggle(c *ui.Context, p colors, open bool, name string) *ui.Element {
	glyph, label := "chevron", "Expand "+name
	if open {
		glyph, label = "down", "Collapse "+name
	}
	button := ui.ButtonBase(c).Label(label).Size(20, 20).Radius(4).Justify(ui.Center)
	if button.Hovered() {
		button.Background(p.border.Alpha(.5))
	}
	button.Children(func() { icon(c, glyph).FontSize(12).TextColor(p.subtle) })
	return button
}

// iconButton is a 28×28 toolbar button holding a 16px icon.
func iconButton(c *ui.Context, name, label string) *ui.Element {
	return sizedIconButton(c, name, label, 28, 16)
}

// smallIconButton is a 22×22 button holding a 14px icon, for actions inside rows and fields.
func smallIconButton(c *ui.Context, name, label string) *ui.Element {
	return sizedIconButton(c, name, label, 22, 14)
}
func sizedIconButton(c *ui.Context, name, label string, size, glyph float32) *ui.Element {
	button := ui.ButtonBase(c).Label(label).Tooltip(label).Size(size, size).Shrink(0).Radius(5).Justify(ui.Center)
	if button.Hovered() {
		button.Background(c.Theme().SurfaceHover)
	}
	button.Children(func() { icon(c, name).FontSize(glyph).TextColor(c.Theme().TextMuted) })
	return button
}

type themeSpec struct {
	ID, Name string
	Dark     bool
	Colors   map[string]string
}

func applyPalette(p colors, values map[string]string) colors {
	targets := map[string]*ui.Color{"background": &p.background, "sidebar": &p.sidebar, "header": &p.header, "response": &p.response, "border": &p.border, "text": &p.text, "muted": &p.muted, "subtle": &p.subtle, "accent": &p.accent, "green": &p.green, "orange": &p.orange, "blue": &p.blue, "red": &p.red, "notice": &p.notice, "yellow": &p.notice}
	for name, value := range values {
		if target := targets[name]; target != nil && value != "" {
			*target = ui.Hex(value)
		}
	}
	if values["border"] == "" {
		p.border = p.background.Mix(p.text, .17)
	}
	if values["sidebar"] == "" {
		p.sidebar = p.background.Mix(p.text, .025)
	}
	if values["response"] == "" {
		p.response = p.sidebar
	}
	if values["header"] == "" {
		p.header = p.background
	}
	return p
}

func (a *App) methodColor(method string, p colors) ui.Color {
	if !b(a.settings, "coloredMethods") {
		return p.muted
	}
	return methodColor(method, p)
}
