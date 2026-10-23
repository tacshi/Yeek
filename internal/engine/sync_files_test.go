package engine

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type syncFixture struct {
	t                   *testing.T
	e                   *Engine
	workspace, request  string
	dir, requestPath    string
	json                bool
	beforeURL, beforeAt string
}

func newSyncFixture(t *testing.T, filename string) *syncFixture {
	t.Helper()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "name": "Sync"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "name": "Keep this request", "url": "https://example.com"})
	f := &syncFixture{t: t, e: e, workspace: str(w, "id"), request: str(r, "id"), dir: t.TempDir(), json: strings.HasSuffix(filename, ".json")}
	f.sync()
	generated := filepath.Join(f.dir, "yaak."+f.request+".yaml")
	f.requestPath = filepath.Join(f.dir, filename)
	if f.json {
		f.edit(func(Object) {})
		_ = os.Remove(generated)
	} else if err := os.Rename(generated, f.requestPath); err != nil {
		t.Fatal(err)
	}
	f.sync()
	if plan := f.changes(); len(plan) != 0 {
		t.Fatalf("fixture not in sync: %+v", plan)
	}
	f.beforeURL = str(f.dbRequest(), "url")
	f.beforeAt = str(f.dbRequest(), "updatedAt")
	return f
}

func (f *syncFixture) sync() {
	f.t.Helper()
	plan, err := f.e.PlanSync(f.t.Context(), f.workspace, f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.e.ApplySync(f.t.Context(), f.workspace, f.dir, plan, ""); err != nil {
		f.t.Fatal(err)
	}
}

func (f *syncFixture) changes() []SyncChange {
	f.t.Helper()
	plan, err := f.e.PlanSync(f.t.Context(), f.workspace, f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []SyncChange
	for _, c := range plan {
		if c.Direction != "none" || c.Conflict {
			out = append(out, c)
		}
	}
	return out
}

func (f *syncFixture) dbRequest() Object {
	f.t.Helper()
	m, err := f.e.Store.Get(f.t.Context(), f.request)
	if err != nil {
		return nil
	}
	return m
}

func (f *syncFixture) states() []Object {
	f.t.Helper()
	states, err := f.e.Store.List(f.t.Context(), "sync_state", f.workspace)
	if err != nil {
		f.t.Fatal(err)
	}
	return states
}

func (f *syncFixture) edit(edit func(Object)) {
	f.t.Helper()
	m := clone(f.dbRequest())
	edit(m)
	var data []byte
	var err error
	if f.json {
		data, err = json.Marshal(m)
	} else {
		data, err = yaml.Marshal(m)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	if err = os.WriteFile(f.requestPath, data, 0600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *syncFixture) assertInvalidFile() string {
	f.t.Helper()
	before, _ := json.Marshal(f.states(), json.Deterministic(true))
	_, err := f.e.PlanSync(f.t.Context(), f.workspace, f.dir)
	if err == nil {
		f.t.Fatal("sync went ahead")
	}
	if !strings.HasPrefix(err.Error(), "Invalid sync file: ") || !strings.Contains(err.Error(), f.requestPath) {
		f.t.Fatalf("error = %v", err)
	}
	if err := f.e.ApplySync(f.t.Context(), f.workspace, f.dir, nil, ""); err == nil {
		f.t.Fatal("apply went ahead")
	}
	r := f.dbRequest()
	if r == nil || str(r, "url") != f.beforeURL || str(r, "updatedAt") != f.beforeAt {
		f.t.Fatalf("request changed: %v", r)
	}
	after, _ := json.Marshal(f.states(), json.Deterministic(true))
	if string(after) != string(before) {
		f.t.Fatalf("sync states changed:\n%s\n%s", before, after)
	}
	return err.Error()
}

func TestTrackedModelTypeChangesAbortSync(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	f.edit(func(m Object) { m["model"] = "websocket_request" })
	f.assertInvalidFile()
}

func TestInvalidTrackedFilesAbortWithoutChangingData(t *testing.T) {
	for _, c := range []struct {
		filename string
		contents []byte
	}{
		{"request.yaml", []byte("model: http_request\nid: [broken\n")},
		{"request.json", []byte(`{"model":"http_request","id":`)},
		{"custom-request.yml", []byte("name: half-written\n")},
		{"custom-request.yml", []byte("model: http_request\n")},
		{"custom-request.yml", []byte{0xff, 0xfe}},
	} {
		f := newSyncFixture(t, c.filename)
		if err := os.WriteFile(f.requestPath, c.contents, 0600); err != nil {
			t.Fatal(err)
		}
		f.assertInvalidFile()
		if data, _ := os.ReadFile(f.requestPath); string(data) != string(c.contents) { // #nosec G304 -- the fixture's file.
			t.Fatalf("%s rewritten", c.filename)
		}
	}
}

func TestTrackedFileWithGitConflictMarkersAbortsSync(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	data, err := os.ReadFile(f.requestPath) // #nosec G304 -- the fixture's file.
	if err != nil {
		t.Fatal(err)
	}
	contents := strings.Replace(string(data), "name: Keep this request\n", "<<<<<<< HEAD\nname: Mine\n=======\nname: Theirs\n>>>>>>> origin/main\n", 1)
	if contents == string(data) {
		t.Fatal("no name line")
	}
	if err = os.WriteFile(f.requestPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if msg := f.assertInvalidFile(); !strings.Contains(msg, "line ") {
		t.Fatal(msg)
	}
}

func TestEmptyTrackedFileAbortsSync(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	if err := os.WriteFile(f.requestPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f.assertInvalidFile()
}

func TestUnreadableTrackedFileAbortsSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	f := newSyncFixture(t, "request.yaml")
	if err := os.Chmod(f.requestPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.requestPath, 0600) })
	if _, err := os.ReadFile(f.requestPath); err == nil {
		t.Skip("running as root")
	}
	if msg := f.assertInvalidFile(); !strings.Contains(msg, "permission denied") {
		t.Fatal(msg)
	}
}

func TestMismatchedIdentitiesAbortSyncForYAMLAndJSON(t *testing.T) {
	for _, filename := range []string{"request.yaml", "request.json"} {
		for _, field := range []string{"id", "workspaceId"} {
			f := newSyncFixture(t, filename)
			f.edit(func(m Object) { m[field] = "different" })
			f.assertInvalidFile()
		}
	}
}

func TestTrackedPathReplacedByADirectoryAbortsSync(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	if err := os.Remove(f.requestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.requestPath, 0700); err != nil {
		t.Fatal(err)
	}
	f.assertInvalidFile()
}

func TestDeletingATrackedFileStillDeletesTheRequest(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	if err := os.Remove(f.requestPath); err != nil {
		t.Fatal(err)
	}
	changes := f.changes()
	if len(changes) != 1 || changes[0].ID != f.request || changes[0].Direction != "pull" || changes[0].Remote != nil {
		t.Fatalf("changes = %+v", changes)
	}
	f.sync()
	if f.dbRequest() != nil {
		t.Fatal("request kept")
	}
}

func TestUnrelatedFilesAreStillIgnored(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	for name, contents := range map[string]string{
		"README.md":      "model: http_request\nid: [broken\n",
		"config.yaml":    "model: unrelated\nid: example\n",
		"unrelated.json": `{"model":"config","id":`,
		"empty.yaml":     "",
	} {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(f.dir, "other-files"), 0700); err != nil {
		t.Fatal(err)
	}
	if changes := f.changes(); len(changes) != 0 {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestAtomicWriteKeepsThePreviousFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request.yaml")
	if err := os.WriteFile(path, []byte("complete previous contents"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "child"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(blocked, []byte("new"), 0o666); err == nil {
		t.Fatal("wrote over a directory")
	}
	if data, _ := os.ReadFile(path); string(data) != "complete previous contents" { // #nosec G304 -- inside t.TempDir.
		t.Fatal(string(data))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("temporary file left: %v", entries)
	}
}

func TestAtomicWritePreservesExistingPermissionsAndNewFileDefaults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "request.yaml")
	if err := os.WriteFile(path, []byte("old contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil { // #nosec G302 -- the test checks that a file keeps this mode.
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("new contents"), 0o666); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	reference := filepath.Join(dir, "reference.yaml")
	created, err := os.Create(reference) // #nosec G304 -- inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	_ = created.Close()
	fresh := filepath.Join(dir, "new.yaml")
	if err := atomicWrite(fresh, []byte("new contents"), 0o666); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(fresh)
	b, _ := os.Stat(reference)
	if a.Mode().Perm() != b.Mode().Perm() {
		t.Fatalf("new file mode %v, default %v", a.Mode().Perm(), b.Mode().Perm())
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("replaced"), 0o666); err == nil {
		t.Fatal("replaced a read-only file")
	}
}
