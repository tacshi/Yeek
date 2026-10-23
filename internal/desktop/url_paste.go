package desktop

import (
	"net/url"
	"strings"

	"uuid"
	"yeek/internal/engine"
	"yeek/locales"
)

func (a *App) handleURLPaste(d *Draft) {
	text, overwrote, ok := a.templateDoc("url").state.Pasted()
	if !ok || !overwrote {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(text), "curl ") {
		a.overwriteFromCurl(d, text)
		return
	}
	if base, params, ok := splitQueryString(text); ok {
		d.URL = base
		d.Parameters = append(params, d.Parameters...)
		d.Tab, d.Dirty = 1, true
	}
}

func splitQueryString(raw string) (string, []KV, bool) {
	base, query, found := strings.Cut(raw, "?")
	if !found || query == "" {
		return "", nil, false
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return "", nil, false
	}
	rows := []KV{}
	for pair := range strings.SplitSeq(query, "&") {
		name, _, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(name)
		if err != nil || len(values[key]) == 0 {
			continue
		}
		rows = append(rows, KV{ID: uuid.NewV4().String(), Name: key, Value: values[key][0], Enabled: true})
		values[key] = values[key][1:]
	}
	return base, rows, true
}

func (a *App) overwriteFromCurl(d *Draft, command string) {
	imported, err := engine.ParseCurl(strings.TrimSpace(command))
	if err != nil {
		a.errorMessage = err.Error()
		return
	}
	current := a.models[d.ID]
	for _, key := range []string{"id", "createdAt", "workspaceId", "folderId", "name", "sortPriority"} {
		imported[key] = current[key]
	}
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, imported)
		return func() {
			a.applyModel(saved)
			fresh := newDraft(saved)
			fresh.Tab, fresh.ResponseTab = d.Tab, d.ResponseTab
			a.drafts[d.ID] = fresh
			a.toast(locales.UpdatedRequestFromCurl())
		}, err
	})
}
