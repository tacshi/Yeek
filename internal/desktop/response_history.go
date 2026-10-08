package desktop

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// requestResponses lists a request's responses, newest first.
func (a *App) requestResponses(requestID string) []engine.Object {
	var responses []engine.Object
	for _, response := range a.list("http_response") {
		if s(response, "requestId") == requestID {
			responses = append(responses, response)
		}
	}
	slices.SortFunc(responses, func(x, y engine.Object) int { return strings.Compare(s(y, "createdAt"), s(x, "createdAt")) })
	return responses
}

// pinKey is Yaak's key for the response pinned while latest is the newest:
// a newer response unpins it.
func pinKey(latest string) string { return "pinned_http_response_id::" + latest }

func (a *App) pinnedResponseID(latest string) string {
	for _, m := range a.list("key_value") {
		if s(m, "namespace") == "global" && s(m, "key") == pinKey(latest) {
			var id string
			_ = json.Unmarshal([]byte(s(m, "value")), &id)
			return id
		}
	}
	return ""
}

// activeResponse is Yaak's usePinnedHttpResponse: the pinned response,
// else the latest.
func (a *App) activeResponse(requestID string) engine.Object {
	latest := a.responses[requestID]
	if latest == nil {
		return nil
	}
	if pinned := a.models[a.pinnedResponseID(s(latest, "id"))]; pinned != nil && s(pinned, "requestId") == requestID {
		a.loadResponseBody(pinned)
		return pinned
	}
	return latest
}

// pinResponse pins a response, or unpins the one pinned.
func (a *App) pinResponse(requestID, id string) {
	latest := s(a.responses[requestID], "id")
	if latest == "" {
		return
	}
	value := "null"
	if a.pinnedResponseID(latest) != id {
		data, _ := json.Marshal(id)
		value = string(data)
	}
	model := engine.Object{"model": "key_value", "namespace": "global", "key": pinKey(latest), "value": value}
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, model)
		return func() { a.applyModel(saved) }, err
	})
}

// deleteResponses deletes responses, and finds each request's latest again.
func (a *App) deleteResponses(responses []engine.Object) {
	for _, response := range responses {
		id, request := s(response, "id"), s(response, "requestId")
		a.run(func() (func(), error) {
			return func() {
				delete(a.models, id)
				a.modelVersion++
				delete(a.responses, request)
				if remaining := a.requestResponses(request); len(remaining) > 0 {
					a.responses[request] = remaining[0]
					a.loadResponseBody(remaining[0])
				}
			}, a.Engine.Delete(a.ctx, id)
		})
	}
}

// formatMillis is Yaak's formatMillis.
func formatMillis(ms float64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%v ms", math.Round(ms))
	case ms < 60_000:
		if ms < 10_000 {
			return fmt.Sprintf("%.1f s", ms/1000)
		}
		return fmt.Sprintf("%.0f s", ms/1000)
	}
	return fmt.Sprintf("%dm %ds", int(ms/60_000), int(math.Round(math.Mod(ms, 60_000)/1000)))
}

// historyGroup is how long ago Yaak's response history says a response came.
func historyGroup(created, now time.Time) string {
	ago := now.Sub(created)
	switch {
	case ago < 5*time.Minute:
		return "Just now"
	case ago < 15*time.Minute:
		return "5 minutes ago"
	case ago < time.Hour:
		return "15 minutes ago"
	case ago < 3*time.Hour:
		return "1 hour ago"
	case ago < 6*time.Hour:
		return "3 hours ago"
	}
	y, m, d := now.Date()
	cy, cm, cd := created.Date()
	switch {
	case y == cy && m == cm && d == cd:
		return "Today"
	case now.AddDate(0, 0, -1).Format(time.DateOnly) == created.Format(time.DateOnly):
		return "Yesterday"
	case y == cy:
		return created.Format("Jan 2")
	}
	return created.Format("Jan 2, 2006")
}

// responseHistoryMenu is Yaak's RecentHttpResponsesDropdown.
func (a *App) responseHistoryMenu(m *ui.Menu, requestID string, active engine.Object) {
	responses := a.requestResponses(requestID)
	latest := ""
	if len(responses) > 0 {
		latest = s(responses[0], "id")
	}
	if m.Item("Delete").Chosen() {
		a.deleteResponses([]engine.Object{active})
	}
	if m.Item("Delete all").Disabled(len(responses) == 0).Chosen() {
		a.deleteResponses(responses)
	}
	if latest != s(active, "id") && m.Item("Unpin Response").Disabled(len(responses) == 0).Chosen() {
		a.pinResponse(requestID, s(active, "id"))
	}
	m.Separator()
	m.Item("Recent").Disabled(true)
	now := time.Now()
	last, recent, shownEmpty := "", false, false
	for i, response := range responses {
		created, err := time.Parse(time.RFC3339Nano, s(response, "createdAt"))
		if err != nil {
			created, _ = time.Parse("2006-01-02T15:04:05.999999999", s(response, "createdAt"))
		}
		group := historyGroup(created.Local(), now)
		if group == "Just now" {
			recent = true
		} else if !recent && !shownEmpty {
			m.Item("No recent requests").Disabled(true)
			shownEmpty = true
		}
		if group != "Just now" && group != last {
			m.Separator()
			m.Item(group).Disabled(true)
			last = group
		}
		elapsed := "n/a"
		if n(response, "elapsed") >= 0 {
			elapsed = formatMillis(n(response, "elapsed"))
		}
		label := fmt.Sprintf("%s  •  %s  •  %s", statusLabel(response, true), elapsed, sizeText(n(response, "contentLength")))
		if m.Item(label).Checked(s(response, "id") == s(active, "id")).Chosen() {
			a.pinResponse(requestID, s(responses[i], "id"))
		}
	}
	if !recent && !shownEmpty {
		m.Item("No recent requests").Disabled(true)
	}
}

// historyIcon is a pin while an earlier response is pinned.
func (a *App) historyIcon(c *ui.Context, requestID string, active engine.Object) ui.Element {
	glyph := "history"
	if latest := a.responses[requestID]; latest != nil && s(latest, "id") != s(active, "id") {
		glyph = "pin"
	}
	return iconButton(c, glyph, "Show response history")
}
