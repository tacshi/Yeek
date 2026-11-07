package engine

import (
	"testing"
	"time"
)

func TestSubscribersReloadAfterAnotherEngineWrites(t *testing.T) {
	dir := t.TempDir()
	app, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	changes, unsubscribe := app.Subscribe()
	defer unsubscribe()

	saveTest(t, app, Object{"model": "workspace", "name": "Own"})
	select {
	case batch := <-changes:
		if batch == nil {
			t.Fatal("the app's own write asked for a reload")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no change for the app's own write")
	}

	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := saveTest(t, other, Object{"model": "workspace", "name": "From the CLI"})
	_ = other.Close()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case batch := <-changes:
			if batch != nil {
				continue
			}
			if _, err := app.Store.Get(t.Context(), str(saved, "id")); err != nil {
				t.Fatal(err)
			}
			return
		case <-deadline:
			t.Fatal("no reload after another engine wrote")
		}
	}
}
