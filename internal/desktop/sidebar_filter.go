package desktop

import (
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
	"yeek/internal/query"
	"yeek/locales"
)

var sidebarFilterColumns = []string{"method", "url", "name", "type", "folder", "grpc_service", "grpc_method"}

var sidebarSuggestionOrder = []string{"url", "folder", "method", "type", "grpc_service", "grpc_method", "name"}

var sidebarFilterSchema = query.NewSchema(sidebarFilterColumns, query.FreeTextFuzzy)

type sidebarFilter struct {
	text    string
	matcher *query.Matcher
}

func (a *App) sidebarMatcher() *query.Matcher {
	if a.filter.matcher == nil || a.filter.text != a.search {
		a.filter = sidebarFilter{text: a.search, matcher: query.NewMatcher(a.search, sidebarFilterSchema)}
	}
	return a.filter.matcher
}

func sidebarItemText(m engine.Object) string {
	text := requestName(m) + " " + s(m, "url")
	if s(m, "model") == "http_request" {
		text = s(m, "method") + " " + text
	}
	return text
}

func (a *App) sidebarItemFields(m engine.Object) map[string]string {
	fields := map[string]string{"name": requestName(m), "type": "http", "url": s(m, "url")}
	switch s(m, "model") {
	case "http_request":
		fields["method"] = strings.ToUpper(s(m, "method"))
	case "grpc_request":
		fields["type"], fields["grpc_method"], fields["grpc_service"] = "grpc", s(m, "method"), s(m, "service")
	case "websocket_request":
		fields["type"] = "ws"
	}
	if parent := a.models[s(m, "folderId")]; s(parent, "model") == "folder" {
		fields["folder"] = s(parent, "name")
	}
	return fields
}

func (a *App) sidebarItemMatches(m engine.Object) bool {
	matcher := a.sidebarMatcher()
	return matcher.IsEmpty() || matcher.Match(sidebarItemText(m), a.sidebarItemFields(m))
}

func sidebarSuggestionValue(source string) string {
	root := query.Parse(source)
	if root == nil {
		return ""
	}
	switch root.Kind {
	case query.NodeTerm, query.NodePhrase:
		return strings.TrimSpace(root.Text)
	case query.NodeField:
		if root.Value.Kind == query.ValueText {
			return strings.TrimSpace(root.Value.Text)
		}
		return strings.TrimSpace(root.ValueSpan.Slice(source))
	}
	return ""
}

func (a *App) sidebarSuggestions() []string {
	value := sidebarSuggestionValue(a.search)
	if value == "" {
		return nil
	}
	found := map[string]bool{}
	for _, kind := range []string{"http_request", "grpc_request", "websocket_request"} {
		for _, m := range a.list(kind) {
			for field, v := range a.sidebarItemFields(m) {
				if v != "" && query.ContainsIgnoreCase(v, value) {
					found[field] = true
				}
			}
		}
	}
	var suggestions []string
	for _, field := range sidebarSuggestionOrder {
		if found[field] {
			suggestions = append(suggestions, query.FormatField(field, value, false))
		}
	}
	return suggestions
}

func (a *App) sidebarNoResults(c *ui.Context, p colors) {
	suggestions := a.sidebarSuggestions()
	ui.Column(c).Padding(12).Center().Gap(6).Children(func() {
		if len(suggestions) == 0 {
			ui.Text(c, locales.NoResultsForQuery(a.search)).FontSize(12).TextColor(p.muted).MaxLines(2)
			return
		}
		ui.Text(c, locales.NoResultsButFoundMatchesFor()).FontSize(12).TextColor(p.muted).MaxLines(2)
		ui.Row(c).Gap(4).Wrap().Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			for i, suggestion := range suggestions {
				if i > 0 {
					ui.Text(c, locales.Or()).FontSize(12).TextColor(p.muted)
				}
				chip := ui.ButtonBase(c).Label(suggestion).Key(suggestion).Padding(1, 5).Radius(4).Border(1, p.border).Background(p.border.Alpha(.25)).MaxWidth(150)
				if chip.Hovered() {
					chip.Background(p.border.Alpha(.55))
				}
				chip.Children(func() { ui.Text(c, suggestion).Font("monospace").FontSize(11).SingleLine() })
				if chip.Clicked() {
					a.search = suggestion
				}
			}
		})
	})
}
