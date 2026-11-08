package desktop

import (
	"context"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestOpenDraftFollowsOutsideChanges(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	if err := tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() { tt.Type("/mine"); tt.Key(ui.Cmd, ui.KeyS) })
	tt.Frame()
	if d.URL != "http://127.0.0.1:1/mine" || d.Dirty {
		t.Fatalf("own save: url %q dirty %v", d.URL, d.Dirty)
	}
	current, err := a.Engine.Store.Get(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	current["url"] = "https://pulled.test"
	current["folderId"] = nil
	current["description"] = "from git"
	saved, err := a.Engine.Save(t.Context(), current)
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(saved)
	if d.URL != "https://pulled.test" || d.Description != "from git" {
		t.Fatalf("draft kept stale values: %q %q", d.URL, d.Description)
	}
	d.Name = "Renamed"
	d.Dirty = true
	a.save(d)
	tt.Frame()
	stored, err := a.Engine.Store.Get(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s(stored, "url") != "https://pulled.test" || s(stored, "description") != "from git" || s(stored, "name") != "Renamed" {
		t.Fatalf("next save reverted the outside change: %v", stored)
	}
}

func TestOwnSaveEchoKeepsNewerEdits(t *testing.T) {
	a, d, _ := composerApp(t, "", engine.Object{})
	d.URL = "https://one.test"
	d.Dirty = true
	first := d.object()
	a.save(d)
	d.URL = "https://two.test"
	d.Dirty = true
	a.save(d)
	stale, err := a.Engine.Save(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	stale["url"] = "https://one.test"
	d.sync(stale)
	if d.URL != "https://two.test" {
		t.Fatalf("a late echo of an earlier save reverted the draft to %q", d.URL)
	}
}

func TestFailedSaveIsRetried(t *testing.T) {
	a, d, _ := composerApp(t, "", engine.Object{})
	ctx := a.ctx
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	a.ctx = canceled
	d.URL = "https://retry.test"
	d.Dirty = true
	a.save(d)
	a.ctx = ctx
	if !d.Dirty || a.errorMessage == "" {
		t.Fatalf("failed save left dirty %v, error %q", d.Dirty, a.errorMessage)
	}
	a.save(d)
	stored, err := a.Engine.Store.Get(t.Context(), d.ID)
	if err != nil || s(stored, "url") != "https://retry.test" || d.Dirty {
		t.Fatalf("retry: %v %v dirty %v", stored, err, d.Dirty)
	}
}
