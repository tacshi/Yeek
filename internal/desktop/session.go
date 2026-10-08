package desktop

import (
	"encoding/json/v2"
	"maps"

	"yeek/internal/engine"
)

type windowState struct {
	CookieJar                       string
	CookieJars                      map[string]string
	Workspace, Request, Environment string
	Tabs                            []string
	Sidebar, RequestPane            float32
	Vertical, HideSidebar           bool
}

func (a *App) restoreSession() {
	for _, m := range a.list("key_value") {
		if s(m, "namespace") != "no_sync" || s(m, "key") != "main-window" {
			continue
		}
		var state windowState
		if json.Unmarshal([]byte(s(m, "value")), &state) != nil {
			return
		}
		if _, ok := a.models[state.Workspace]; ok {
			a.workspace = state.Workspace
		}
		a.active = state.Request
		a.environment = state.Environment
		a.cookieJar = state.CookieJar
		a.cookieSelections = state.CookieJars
		a.tabs = state.Tabs
		if state.Sidebar > 100 {
			a.sidebarWidth = state.Sidebar
		}
		if state.RequestPane > 100 {
			a.requestWidth = state.RequestPane
		}
		a.vertical, a.hideSidebar = state.Vertical, state.HideSidebar
		return
	}
}
func (a *App) persistSession() {
	state := windowState{Workspace: a.workspace, Request: a.active, Environment: a.environment, CookieJar: a.cookieJar, CookieJars: maps.Clone(a.cookieSelections), Tabs: append([]string{}, a.tabs...), Sidebar: a.sidebarWidth, RequestPane: a.requestWidth, Vertical: a.vertical, HideSidebar: a.hideSidebar}
	data, err := json.Marshal(state)
	if err != nil {
		a.errorMessage = err.Error()
		return
	}
	a.run(func() (func(), error) {
		_, err := a.Engine.Save(a.ctx, engine.Object{"model": "key_value", "namespace": "no_sync", "key": "main-window", "value": string(data)})
		return nil, err
	})
}
