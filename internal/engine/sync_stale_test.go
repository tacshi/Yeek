package engine

import (
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func (f *syncFixture) applyReviewed(plan []SyncChange) bool {
	f.t.Helper()
	err := f.e.ApplySync(f.t.Context(), f.workspace, f.dir, plan, SyncNewest)
	if errors.Is(err, ErrSyncStale) {
		return false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return true
}

func (f *syncFixture) plan() []SyncChange {
	f.t.Helper()
	plan, err := f.e.PlanSync(f.t.Context(), f.workspace, f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return plan
}

func (f *syncFixture) statesJSON() string {
	f.t.Helper()
	data, _ := json.Marshal(f.states(), json.Deterministic(true))
	return string(data)
}

func (f *syncFixture) saveDB(m Object) {
	f.t.Helper()
	saveTest(f.t, f.e, m)
}

func (f *syncFixture) workspacePath() string {
	return filepath.Join(f.dir, "yaak."+f.workspace+".yaml")
}

func (f *syncFixture) dbWorkspace() Object {
	f.t.Helper()
	m, err := f.e.Store.Get(f.t.Context(), f.workspace)
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *syncFixture) writeModel(path string, m Object) []byte {
	f.t.Helper()
	data, err := yaml.Marshal(m)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		f.t.Fatal(err)
	}
	return data
}

func sameModel(a, b Object) bool {
	x, _ := json.Marshal(a, json.Deterministic(true))
	y, _ := json.Marshal(b, json.Deterministic(true))
	return string(x) == string(y)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a file of the test's directory.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func directions(plan []SyncChange) []string {
	var out []string
	for _, c := range plan {
		if c.Conflict {
			out = append(out, "conflict")
		} else if c.Direction != "none" {
			out = append(out, c.Direction)
		}
	}
	return out
}

func TestRestoredFileInvalidatesReviewedDeletionAndPreservesHistory(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	saved := saveTest(t, f.e, Object{"model": "http_response", "requestId": f.request, "workspaceId": f.workspace, "status": 200})
	response, err := f.e.Store.Get(t.Context(), str(saved, "id"))
	if err != nil {
		t.Fatal(err)
	}
	file := readTestFile(t, f.requestPath)
	if err := os.Remove(f.requestPath); err != nil {
		t.Fatal(err)
	}
	plan := f.plan()
	if got := directions(plan); len(got) != 1 || got[0] != "pull" {
		t.Fatalf("plan = %v", got)
	}
	if err := os.WriteFile(f.requestPath, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	before := f.statesJSON()

	if f.applyReviewed(plan) {
		t.Fatal("stale deletion applied")
	}
	if r := f.dbRequest(); r == nil || str(r, "name") != "Keep this request" {
		t.Fatalf("request = %v", r)
	}
	if got, err := f.e.Store.Get(t.Context(), str(response, "id")); err != nil || !sameModel(got, response) {
		t.Fatalf("response = %v, %v", got, err)
	}
	if f.statesJSON() != before {
		t.Fatal("sync states changed")
	}
	if readTestFile(t, f.requestPath) != file {
		t.Fatal("file changed")
	}
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("changes = %+v", c)
	}
}

func TestNewerDatabaseEditInvalidatesReviewedUpdateEvenWhenFileTimestampWins(t *testing.T) {
	for _, fileIsNewer := range []bool{false, true} {
		f := newSyncFixture(t, "request.yaml")
		f.edit(func(m Object) {
			m["name"] = "Previously reviewed file version"
			if fileIsNewer {
				m["updatedAt"] = time.Now().Add(24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000000000")
			}
		})
		plan := f.plan()
		if got := directions(plan); len(got) != 1 || got[0] != "pull" {
			t.Fatalf("plan = %v", got)
		}
		before := f.statesJSON()
		request := f.dbRequest()
		request["name"] = "New edit from another window"
		f.saveDB(request)

		if f.applyReviewed(plan) {
			t.Fatalf("fileIsNewer=%v: stale update applied", fileIsNewer)
		}
		if got := str(f.dbRequest(), "name"); got != "New edit from another window" {
			t.Fatalf("fileIsNewer=%v: name = %q", fileIsNewer, got)
		}
		if f.statesJSON() != before {
			t.Fatalf("fileIsNewer=%v: sync states changed", fileIsNewer)
		}
	}
}

func TestChangedFileInvalidatesReviewedUpdateUntilReviewedAgain(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	f.edit(func(m Object) { m["name"] = "Reviewed version" })
	plan := f.plan()
	f.edit(func(m Object) { m["name"] = "New file version" })
	before := f.statesJSON()

	if f.applyReviewed(plan) {
		t.Fatal("stale update applied")
	}
	if got := str(f.dbRequest(), "name"); got != "Keep this request" {
		t.Fatalf("name = %q", got)
	}
	if f.statesJSON() != before {
		t.Fatal("sync states changed")
	}
	if !f.applyReviewed(f.plan()) {
		t.Fatal("fresh plan was stale")
	}
	if got := str(f.dbRequest(), "name"); got != "New file version" {
		t.Fatalf("name = %q", got)
	}
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("changes = %+v", c)
	}
}

func TestUnchangedReviewedPlanAppliesInAnyOrderAndCannotBeReplayed(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	if err := os.Remove(f.requestPath); err != nil {
		t.Fatal(err)
	}
	workspace := f.dbWorkspace()
	workspace["name"] = "Reviewed workspace"
	f.writeModel(f.workspacePath(), workspace)
	plan := directionsOnly(f.plan())
	if len(plan) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	plan[0], plan[1] = plan[1], plan[0]

	if !f.applyReviewed(plan) {
		t.Fatal("reviewed plan was stale")
	}
	if f.dbRequest() != nil {
		t.Fatal("request not deleted")
	}
	if got := str(f.dbWorkspace(), "name"); got != "Reviewed workspace" {
		t.Fatalf("workspace name = %q", got)
	}
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("changes = %+v", c)
	}
	before := f.statesJSON()
	if f.applyReviewed(plan) {
		t.Fatal("plan replayed")
	}
	if f.statesJSON() != before {
		t.Fatal("sync states changed")
	}
}

func directionsOnly(plan []SyncChange) []SyncChange {
	var out []SyncChange
	for _, c := range plan {
		if c.Direction != "none" || c.Conflict {
			out = append(out, c)
		}
	}
	return out
}

func TestDatabaseChangesWhileWaitingForWriterInvalidateReviewedPlan(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	f.edit(func(m Object) { m["name"] = "Incoming file version" })
	workspace := f.dbWorkspace()
	workspace["name"] = "Outbound workspace version"
	f.saveDB(workspace)
	plan := f.plan()
	if got := directions(plan); len(got) != 2 {
		t.Fatalf("plan = %v", got)
	}
	before := f.statesJSON()
	ready, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.e.Store.Write(t.Context(), Object{"type": "window"}, func(tx *modelTx) error {
			close(ready)
			<-resume
			request, err := tx.get(t.Context(), f.request)
			if err != nil {
				return err
			}
			request["name"] = "Concurrent database edit"
			_, err = tx.upsert(t.Context(), request)
			return err
		})
	}()
	<-ready
	applied := make(chan bool, 1)
	go func() { applied <- f.applyReviewed(plan) }()
	time.Sleep(50 * time.Millisecond)
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if <-applied {
		t.Fatal("stale plan applied")
	}
	if got := str(f.dbRequest(), "name"); got != "Concurrent database edit" {
		t.Fatalf("name = %q", got)
	}
	if f.statesJSON() != before {
		t.Fatal("sync states changed")
	}
}

func TestCurrentOutboundOnlyPlanStillSyncsAutomatically(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	request := f.dbRequest()
	request["name"] = "New database version"
	f.saveDB(request)
	plan := f.plan()
	if got := directions(plan); len(got) != 1 || got[0] != "push" {
		t.Fatalf("plan = %v", got)
	}
	if !f.applyReviewed(plan) {
		t.Fatal("plan was stale")
	}
	if !strings.Contains(readTestFile(t, f.requestPath), "New database version") {
		t.Fatal("file not written")
	}
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("changes = %+v", c)
	}
}

func TestReviewedImportAndFileDeletionApplyTogether(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	added := clone(f.dbRequest())
	added["id"] = "rq_sync_added"
	f.writeModel(filepath.Join(f.dir, "added.yaml"), added)
	if err := f.e.Delete(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	plan := f.plan()
	if got := directions(plan); len(got) != 2 || got[0] == got[1] {
		t.Fatalf("plan = %v", got)
	}

	if !f.applyReviewed(plan) {
		t.Fatal("plan was stale")
	}
	if _, err := os.Stat(f.requestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("deleted request's file kept", err)
	}
	if got, err := f.e.Store.Get(t.Context(), "rq_sync_added"); err != nil || str(got, "id") != "rq_sync_added" {
		t.Fatalf("added = %v, %v", got, err)
	}
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("changes = %+v", c)
	}
}

func TestReviewedInboundAndOutboundUpdatesApplyTogether(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	f.edit(func(m Object) { m["name"] = "Incoming request" })
	workspace := f.dbWorkspace()
	workspace["name"] = "Outgoing workspace"
	f.saveDB(workspace)
	plan := f.plan()
	if got := directions(plan); len(got) != 2 || got[0] == got[1] {
		t.Fatalf("plan = %v", got)
	}

	if !f.applyReviewed(plan) {
		t.Fatal("plan was stale")
	}
	if got := str(f.dbRequest(), "name"); got != "Incoming request" {
		t.Fatalf("name = %q", got)
	}
	if !strings.Contains(readTestFile(t, f.workspacePath()), "Outgoing workspace") {
		t.Fatal("workspace file not written")
	}
	if c := f.changes(); len(c) != 0 {
		t.Fatalf("changes = %+v", c)
	}
}

func TestLaterFileEditsInvalidateReviewedOutboundOperations(t *testing.T) {
	for _, creating := range []bool{false, true} {
		for _, filePresent := range []bool{true, false} {
			f := newSyncFixture(t, "yaak.rq_sync_test.yaml")
			if creating {
				for _, state := range f.states() {
					if str(state, "modelId") == f.request {
						if err := f.e.Delete(t.Context(), str(state, "id")); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			request := f.dbRequest()
			request["name"] = "Reviewed outgoing request"
			f.saveDB(request)
			workspace := f.dbWorkspace()
			incoming := clone(workspace)
			incoming["name"] = "Reviewed incoming workspace"
			content := f.writeModel(f.workspacePath(), incoming)
			if !filePresent {
				if err := os.Remove(f.requestPath); err != nil {
					t.Fatal(err)
				}
			}
			plan := f.plan()
			if got := directions(plan); len(got) != 2 {
				t.Fatalf("creating=%v, filePresent=%v: plan = %v", creating, filePresent, got)
			}
			request, states := f.dbRequest(), f.statesJSON()

			edited := f.writeModel(f.requestPath, func() Object {
				m := clone(request)
				m["name"] = "Later external file edit"
				m["updatedAt"] = str(workspace, "updatedAt")
				return m
			}())

			if f.applyReviewed(plan) {
				t.Fatalf("creating=%v, filePresent=%v: stale plan applied", creating, filePresent)
			}
			if readTestFile(t, f.requestPath) != string(edited) {
				t.Fatal("request file overwritten")
			}
			if readTestFile(t, f.workspacePath()) != string(content) {
				t.Fatal("workspace file changed")
			}
			if !sameModel(f.dbRequest(), request) {
				t.Fatal("request changed")
			}
			if !sameModel(f.dbWorkspace(), workspace) {
				t.Fatal("workspace changed")
			}
			if f.statesJSON() != states {
				t.Fatal("sync states changed")
			}
			if !f.applyReviewed(f.plan()) {
				t.Fatal("fresh plan was stale")
			}
			if c := f.changes(); len(c) != 0 {
				t.Fatalf("changes = %+v", c)
			}
		}
	}
}
