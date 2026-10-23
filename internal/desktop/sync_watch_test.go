package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// A file of the sync directory changed outside Yeek is reported, through
// mygo.WatchFiles, and syncs into the workspace.
func TestWatchSyncDirPullsEdits(t *testing.T) {
	a, e, ids := treeApp(t)
	dir := t.TempDir()
	if err := a.syncNow(a.workspace, dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errs := make(chan error, 8)
	e.WatchDir = watchSyncDir
	changed := func() {
		if err := a.syncNow(a.workspace, dir); err != nil {
			errs <- err
		}
	}
	stop, err := e.WatchSync(ctx, a.workspace, dir, changed, func(err error) { errs <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	file := filepath.Join(dir, "yaak."+ids["Alpha"]+".yaml")
	data, err := os.ReadFile(file) // #nosec G304 -- a file of the test's directory.
	if err != nil {
		t.Fatal(err)
	}
	// A temporary file of an atomic write is no change of its own.
	if err := os.WriteFile(filepath.Join(dir, ".yaak.x.yaml.1-1.tmp"), []byte("model: nonsense"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), "name: Alpha", "name: Watched", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		m, err := e.Store.Get(t.Context(), ids["Alpha"])
		if err == nil && m["name"] == "Watched" {
			break
		}
		select {
		case err := <-errs:
			t.Fatal(err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("edit not synced", m["name"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !engine.SyncTempFile(".yaak.x.yaml.1-1.tmp") || engine.SyncTempFile("yaak.x.yaml") {
		t.Fatal("SyncTempFile")
	}
}

// Changes coming in from the directory wait in Yaak's Changes Detected
// dialog; a plan that went stale meanwhile is reviewed again (#739).
func TestSyncReviewsIncomingChanges(t *testing.T) {
	a, e, ids := treeApp(t)
	dir := t.TempDir()
	if err := a.syncNow(a.workspace, dir); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1360, 860)
	file := filepath.Join(dir, "yaak."+ids["Alpha"]+".yaml")
	data, err := os.ReadFile(file) // #nosec G304 -- a file of the test's directory.
	if err != nil {
		t.Fatal(err)
	}
	rename := func(name string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(strings.Replace(string(data), "name: Alpha", "name: "+name, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := func() string {
		m, err := e.Store.Get(t.Context(), ids["Alpha"])
		if err != nil {
			t.Fatal(err)
		}
		return m["name"].(string)
	}

	rename("Reviewed")
	a.reviewSync(a.workspace, dir, 1)
	tt.Frame()
	if !tt.HasText("Changes Detected") || !tt.HasText("1 file in the directory has changed. Do you want to update your workspace?") || !tt.HasText("update") {
		t.Fatal(tt.Texts())
	}
	if name() != "Alpha" {
		t.Fatal("applied before review")
	}

	// The file changed again before Apply Changes: the plan is reviewed again.
	rename("Newer")
	if err := tt.Click("Apply Changes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if name() != "Alpha" || !tt.HasText("Changes Detected") || !tt.HasText(engine.ErrSyncStale.Error()) {
		t.Fatal(name(), tt.Texts())
	}
	if err := tt.Click("Apply Changes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if name() != "Newer" || a.dialogOpen {
		t.Fatal(name(), a.dialogOpen)
	}

	// Changes going out apply without asking.
	m := deepCopy(a.models[ids["Alpha"]])
	m["name"] = "Outgoing"
	if _, err := e.Save(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	a.reviewSync(a.workspace, dir, 1)
	tt.Frame()
	if a.dialogOpen {
		t.Fatal(tt.Texts())
	}
	if data, _ := os.ReadFile(file); !strings.Contains(string(data), "name: Outgoing") { // #nosec G304 -- a file of the test's directory.
		t.Fatal(string(data))
	}
}
