package desktop

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestResponseHistoryPinsAndDeletes(t *testing.T) {
	a, e, ids := treeApp(t)
	rid := ids["Alpha"]
	respond := func(status float64) engine.Object {
		r, err := e.Save(t.Context(), engine.Object{"model": "http_response", "workspaceId": a.workspace, "requestId": rid, "state": "closed", "status": status, "elapsed": 12.0, "contentLength": 1500.0})
		if err != nil {
			t.Fatal(err)
		}
		a.applyModel(r)
		return r
	}
	older := respond(404)
	newer := respond(200)
	a.openRequest(rid)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Show response history"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, want := range []string{"Delete", "Delete all", "Recent", "404  •  12 ms  •  1.5 KB", "200  •  12 ms  •  1.5 KB"} {
		if !slices.Contains(menu, want) {
			t.Fatalf("menu lacks %q: %v", want, menu)
		}
	}
	if slices.Contains(menu, "Unpin Response") {
		t.Fatal("unpin shown for the latest response")
	}
	if err := tt.ChooseMenuItem("404  •  12 ms  •  1.5 KB"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s(a.activeResponse(rid), "id") != s(older, "id") || !tt.HasText("404") {
		t.Fatal(a.activeResponse(rid))
	}
	if err := tt.Click("Show response history"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Unpin Response"); err != nil {
		t.Fatal(tt.Menu())
	}
	if s(a.activeResponse(rid), "id") != s(newer, "id") {
		t.Fatal("not unpinned")
	}
	// A pin lasts until a newer response comes.
	a.pinResponse(rid, s(older, "id"))
	time.Sleep(2 * time.Millisecond)
	newest := respond(201)
	if s(a.activeResponse(rid), "id") != s(newest, "id") {
		t.Fatal("a newer response did not unpin")
	}
	if err := tt.Click("Show response history"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete all"); err != nil {
		t.Fatal(err)
	}
	if a.activeResponse(rid) != nil || len(a.requestResponses(rid)) != 0 {
		t.Fatal(a.requestResponses(rid))
	}
}

func TestFormatMillisAndHistoryGroups(t *testing.T) {
	for ms, want := range map[float64]string{12: "12 ms", 1500: "1.5 s", 12_400: "12 s", 125_000: "2m 5s"} {
		if got := formatMillis(ms); got != want {
			t.Errorf("formatMillis(%v) = %q, want %q", ms, got, want)
		}
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	for ago, want := range map[time.Duration]string{time.Minute: "Just now", 10 * time.Minute: "5 minutes ago", 30 * time.Minute: "15 minutes ago", 2 * time.Hour: "1 hour ago", 4 * time.Hour: "3 hours ago", 7 * time.Hour: "Today", 30 * time.Hour: "Yesterday", 72 * time.Hour: "Oct 6", 400 * 24 * time.Hour: "Sep 4, 2025"} {
		if got := historyGroup(now.Add(-ago), now); got != want {
			t.Errorf("historyGroup(-%v) = %q, want %q", ago, got, want)
		}
	}
}
