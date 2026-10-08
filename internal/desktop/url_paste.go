package desktop

import (
	"net/url"
	"strings"

	"uuid"
	"yeek/internal/engine"
)

// handleURLPaste is Yaak's URL bar paste handling: when a paste replaces the
// whole URL, a cURL command overwrites the request and a URL with a query
// string moves its parameters to the Params tab.
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

// splitQueryString is Yaak's prepareImportQuerystring: the URL before "?"
// and its query as parameter rows, or false when there is no query.
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
	// Keep the parameters in the order they were written.
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

// overwriteFromCurl replaces the request with what a cURL command describes,
// keeping what Yaak keeps: its id, place, name and creation time.
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
			a.toast("Updated request from Curl")
		}, err
	})
}
