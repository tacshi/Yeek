package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

const importFixture = `{"info":{"name":"Imported API"},"item":[{"id":"get","name":"Read Items","request":{"method":"GET","url":"https://example.test/v1/items"}}]}`

func TestImportNativePreviewDoesNotWriteUntilApplied(t *testing.T) {
	a, e := cookieApp(t)
	wid := a.workspace
	a.openImport(nil, engine.ImportDestination{WorkspaceID: wid})
	a.imports.entryKind = "Text / cURL"
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Collection text"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() {
		tt.Type(importFixture)
		if err := tt.Click("Preview Import"); err != nil {
			t.Fatal(err)
		}
	})
	if a.imports.plan == nil {
		t.Fatal(a.imports.error)
	}
	models, _ := e.Store.List(t.Context(), "http_request", wid)
	if len(models) != 0 {
		t.Fatal("preview imported data")
	}
	if !tt.HasText("Read Items") {
		t.Fatal("preview resource missing")
	}
	if err := tt.Click("Apply Import"); err != nil {
		t.Fatal(err)
	}
	models, _ = e.Store.List(t.Context(), "http_request", wid)
	if a.dialogOpen || len(models) != 1 || s(models[0], "url") != "https://example.test/v1/items" {
		t.Fatal(a.imports.error, models)
	}
}

func TestImportNativeFileReimportConflictAndUnlink(t *testing.T) {
	a, e := cookieApp(t)
	path := filepath.Join(t.TempDir(), "collection.json")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(importFixture)
	a.ImportPath(path)
	tt := ui.NewTester(a.View, 1360, 860)
	if a.imports.plan == nil || !a.dialogOpen {
		t.Fatal(a.imports.error)
	}
	if err := tt.Click("Apply Import"); err != nil {
		t.Fatal(err)
	}
	wid := a.workspace
	requests, _ := e.Store.List(t.Context(), "http_request", wid)
	if len(requests) != 1 {
		t.Fatal(requests)
	}
	rid := s(requests[0], "id")
	a.openRequest(rid)
	a.drafts[rid].Description = "Local notes"
	a.drafts[rid].Dirty = true
	a.saveActive()
	write(strings.ReplaceAll(importFixture, "/v1/items", "/v2/items"))
	a.importFile()
	tt.Frame()
	if err := tt.Click("Reimport collection.json"); err != nil {
		t.Fatal(err)
	}
	if a.imports.plan == nil {
		t.Fatal(a.imports.error)
	}
	if err := tt.Click("Inspect Read Items"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Conflict") || !tt.HasText("Keep Mine") {
		t.Fatal("conflict choice missing")
	}
	if err := tt.Click("Conflict resolution"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Take Source"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Apply Import"); err != nil {
		t.Fatal(err)
	}
	requests, _ = e.Store.List(t.Context(), "http_request", wid)
	if len(requests) != 1 || s(requests[0], "id") != rid || s(requests[0], "url") != "https://example.test/v2/items" || s(requests[0], "description") != "" {
		t.Fatal(requests)
	}
	if a.drafts[rid].URL != "https://example.test/v2/items" {
		t.Fatal("active draft did not refresh")
	}
	a.importFile()
	tt.Frame()
	if err := tt.Click("Unlink collection.json"); err != nil {
		t.Fatal(err)
	}
	sources, _ := e.Store.List(t.Context(), "import_source", wid)
	if len(sources) != 0 {
		t.Fatal(sources)
	}
	if _, err := e.Store.Get(t.Context(), rid); err != nil {
		t.Fatal("unlink deleted request", err)
	}
}

func TestImportNativeSelectionAndStalePreview(t *testing.T) {
	a, e := cookieApp(t)
	a.openImport([]engine.ImportInput{{Kind: "text", Content: importFixture}}, engine.ImportDestination{WorkspaceID: a.workspace})
	a.previewImport()
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Deselect All"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Import Read Items"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Apply Import"); err != nil {
		t.Fatal(err)
	}
	requests, _ := e.Store.List(t.Context(), "http_request", a.workspace)
	if len(requests) != 1 {
		t.Fatal(requests)
	}
	a.ImportPath(filepath.Join(t.TempDir(), "missing.json"))
	tt.Frame()
	if a.imports.plan != nil || a.imports.error == "" || !a.dialogOpen {
		t.Fatal("failed source has no inline error")
	}
}
