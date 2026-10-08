package desktop

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
