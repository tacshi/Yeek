package engine

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func importDocumentForTest() ImportDocument {
	return ImportDocument{Format: "Yaak", Origin: "/collection.yaak", Label: "API", Models: []Object{
		{"model": "workspace", "id": "w", "name": "API"},
		{"model": "environment", "id": "e", "workspaceId": "w", "parentId": "w", "parentModel": "workspace", "name": "Base", "variables": []any{Object{"name": "host", "value": "https://example.test"}}},
		{"model": "folder", "id": "f", "workspaceId": "w", "name": "Group"},
		{"model": "http_request", "id": "r", "workspaceId": "w", "folderId": "f", "name": "Read", "url": "${[ host ]}/first", "headers": []any{Object{"name": "Accept", "value": "application/json"}}},
	}}
}
func testImportPlan(t *testing.T, e *Engine, doc ImportDocument, workspace string) *ImportPlan {
	t.Helper()
	plan, err := e.planImportDocuments(t.Context(), []ImportDocument{doc}, ImportDestination{WorkspaceID: workspace})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func commitTestImport(t *testing.T, e *Engine, p *ImportPlan) {
	t.Helper()
	if _, err := e.CommitImport(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}
func planItem(t *testing.T, plan *ImportPlan, key string) *ImportItem {
	t.Helper()
	for _, item := range plan.Items {
		if item.SourceKey == key {
			return item
		}
	}
	t.Fatalf("no import item %s", key)
	return nil
}
func TestImportPreviewReimportAndConflicts(t *testing.T) {
	e := testEngine(t)
	doc := importDocumentForTest()
	before, _ := e.Store.List(t.Context(), "", "")
	p := testImportPlan(t, e, doc, "")
	after, _ := e.Store.List(t.Context(), "", "")
	if importHash(before) != importHash(after) {
		t.Fatal("preview wrote data")
	}
	wid := str(p.NewWorkspaces()[0], "id")
	rid := planItem(t, p, "http_request:r").ID
	commitTestImport(t, e, p)
	if _, err := e.CommitImport(t.Context(), p); err == nil {
		t.Fatal("committed twice")
	}
	p = testImportPlan(t, e, doc, wid)
	for _, item := range p.Items {
		if item.Action != "unchanged" {
			t.Fatalf("reimport %s: %s (%v)", item.Name, item.Action, item.ChangedFields)
		}
	}
	// Editor row identifiers and empty optional fields are not content changes.
	saveTest(t, e, Object{"model": "http_request", "id": rid, "headers": []any{Object{"id": "editor-row", "name": "Accept", "value": "application/json", "enabled": true}}, "description": "", "body": Object{}})
	p = testImportPlan(t, e, doc, wid)
	if item := planItem(t, p, "http_request:r"); item.Action != "unchanged" {
		t.Fatal(item)
	}
	doc.Models[3]["url"] = "${[ host ]}/second"
	p = testImportPlan(t, e, doc, wid)
	if item := planItem(t, p, "http_request:r"); item.ID != rid || item.Action != "update" || !slices.Equal(item.ChangedFields, []string{"url"}) {
		t.Fatal(item)
	}
	commitTestImport(t, e, p)
	saveTest(t, e, Object{"model": "http_request", "id": rid, "description": "Local notes"})
	p = testImportPlan(t, e, doc, wid)
	if item := planItem(t, p, "http_request:r"); item.Action != "keep_local" {
		t.Fatal(item)
	}
	doc.Models[3]["url"] = "${[ host ]}/third"
	p = testImportPlan(t, e, doc, wid)
	if item := planItem(t, p, "http_request:r"); item.Action != "conflict" || item.Resolution != "keep_mine" {
		t.Fatal(item)
	}
	commitTestImport(t, e, p)
	r, _ := e.Store.Get(t.Context(), rid)
	if str(r, "description") != "Local notes" || !strings.HasSuffix(str(r, "url"), "/second") {
		t.Fatal(r)
	}
	doc.Models[3]["url"] = "${[ host ]}/fourth"
	p = testImportPlan(t, e, doc, wid)
	planItem(t, p, "http_request:r").Resolution = "take_source"
	commitTestImport(t, e, p)
	r, _ = e.Store.Get(t.Context(), rid)
	if str(r, "description") != "" || !strings.HasSuffix(str(r, "url"), "/fourth") {
		t.Fatal(r)
	}
}

func TestImportSelectionAndBaseEnvironmentIsolation(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "name": "Existing"})
	wid := str(w, "id")
	if err := e.Store.EnsureWorkspace(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	doc := importDocumentForTest()
	p := testImportPlan(t, e, doc, wid)
	base := planItem(t, p, "environment:e")
	if str(base.After, "parentModel") != "environment" {
		t.Fatal("would replace base variables")
	}
	planItem(t, p, "folder:f").Selected = false
	if _, err := e.CommitImport(t.Context(), p); err == nil {
		t.Fatal("unselected missing parent accepted")
	}
	requests, _ := e.Store.List(t.Context(), "http_request", wid)
	if len(requests) != 0 {
		t.Fatal("partial import")
	}
	planItem(t, p, "http_request:r").Selected = false
	commitTestImport(t, e, p)
	p = testImportPlan(t, e, doc, wid)
	if planItem(t, p, "folder:f").Action != "ignored" || planItem(t, p, "http_request:r").Selected {
		t.Fatal("ignored choices lost")
	}
	planItem(t, p, "folder:f").Selected = true
	planItem(t, p, "http_request:r").Selected = true
	commitTestImport(t, e, p)
	p = testImportPlan(t, e, doc, wid)
	for _, item := range p.Items {
		if item.Kind == "workspace" {
			if item.Action != "keep_local" {
				t.Fatal(item)
			}
			continue
		}
		if item.Action != "unchanged" {
			t.Fatal(item)
		}
	}
}

func TestImportWorkspaceSettingsAndSelection(t *testing.T) {
	e := testEngine(t)
	doc := importDocumentForTest()
	doc.Models[0]["headers"] = []any{Object{"name": "X-Workspace", "value": "one"}}
	p := testImportPlan(t, e, doc, "")
	wid := str(p.NewWorkspaces()[0], "id")
	for _, item := range p.Items {
		item.Selected = false
	}
	commitTestImport(t, e, p)
	if _, err := e.Store.Get(t.Context(), wid); err == nil {
		t.Fatal("deselected workspace was created")
	}
	p = testImportPlan(t, e, doc, "")
	wid = str(p.NewWorkspaces()[0], "id")
	commitTestImport(t, e, p)
	doc.Models[0]["headers"] = []any{Object{"name": "X-Workspace", "value": "two"}}
	p = testImportPlan(t, e, doc, wid)
	if item := planItem(t, p, "workspace:w"); item.Action != "update" {
		t.Fatal(item)
	}
	commitTestImport(t, e, p)
	w, _ := e.Store.Get(t.Context(), wid)
	if str(objects(array(w, "headers"))[0], "value") != "two" {
		t.Fatal(w)
	}
	saveTest(t, e, Object{"model": "workspace", "id": wid, "name": "Locally Renamed"})
	doc.Models[0]["settingFollowRedirects"] = false
	p = testImportPlan(t, e, doc, wid)
	if item := planItem(t, p, "workspace:w"); item.Action != "conflict" || item.Resolution != "keep_mine" {
		t.Fatal(item)
	}
	commitTestImport(t, e, p)
	w, _ = e.Store.Get(t.Context(), wid)
	if str(w, "name") != "Locally Renamed" || !boolean(w, "settingFollowRedirects") {
		t.Fatal("unselected settings replaced", w)
	}
}

func TestImportPlanConcurrentCommit(t *testing.T) {
	e := testEngine(t)
	p := testImportPlan(t, e, importDocumentForTest(), "")
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { _, err := e.CommitImport(t.Context(), p); results <- err })
	}
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("committed %d times", successes)
	}
}

func TestImportReimportFromMultiWorkspaceFileStaysScoped(t *testing.T) {
	e := testEngine(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "workspaces.json")
	content := `{"yaakSchema":5,"resources":{"workspaces":[{"id":"one","name":"One"},{"id":"two","name":"Two"}],"httpRequests":[{"id":"r1","workspaceId":"one","name":"First","url":"https://one.test"},{"id":"r2","workspaceId":"two","name":"Second","url":"https://two.test"}]}}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := e.PlanImport(t.Context(), []ImportInput{{Origin: path}}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	commitTestImport(t, e, p)
	sources, err := e.Store.List(t.Context(), "import_source", "")
	if err != nil || len(sources) != 2 {
		t.Fatal(sources, err)
	}
	for _, source := range sources {
		p, err := e.PlanImport(t.Context(), []ImportInput{{Origin: path, SourceWorkspaceID: str(source, "sourceWorkspaceId")}}, ImportDestination{WorkspaceID: str(source, "workspaceId")})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Sources) != 1 || len(p.Items) != 2 {
			t.Fatal("reimport pulled another workspace into its destination", p.Items)
		}
		for _, item := range p.Items {
			if item.Action != "unchanged" {
				t.Fatal(item)
			}
		}
	}
}

func TestImportLegacyYaakEnvironmentMigrations(t *testing.T) {
	e := testEngine(t)
	data := `{"yaakSchema":2,"resources":{"workspaces":[{"id":"w","name":"Legacy","variables":[{"name":"host","value":"https://base.test"}]}],"environments":[{"id":"e","workspaceId":"w","name":"Staging","variables":[{"name":"host","value":"https://staging.test"}]}],"requests":[{"id":"r","workspaceId":"w","url":"${[ host ]}/items"}]}}`
	models, err := e.Import(t.Context(), []byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	request := importedNamed(t, models, "http_request", "")
	resolved, err := e.resolve(t.Context(), request, "")
	if err != nil || str(resolved.Model, "url") != "https://base.test/items" {
		t.Fatal(resolved.Model, err)
	}
	env := importedNamed(t, models, "environment", "Staging")
	resolved, err = e.resolve(t.Context(), request, str(env, "id"))
	if err != nil || str(resolved.Model, "url") != "https://staging.test/items" {
		t.Fatal(resolved.Model, err)
	}
	models = openAPIModels(t, `{"yaakSchema":4,"resources":{"workspaces":[{"id":"w","name":"v4"}],"environments":[{"id":"b","workspaceId":"w","base":true},{"id":"c","workspaceId":"w","base":false}]}}`)
	for _, m := range models {
		if str(m, "id") == "b" && str(m, "parentModel") != "workspace" || str(m, "id") == "c" && str(m, "parentModel") != "environment" {
			t.Fatal(m)
		}
	}
}

func TestImportDeletionPreviewIncludesLocalDescendants(t *testing.T) {
	e := testEngine(t)
	doc := importDocumentForTest()
	p := testImportPlan(t, e, doc, "")
	wid := str(p.NewWorkspaces()[0], "id")
	fid := planItem(t, p, "folder:f").ID
	commitTestImport(t, e, p)
	env := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "folder", "parentId": fid, "name": "Folder variables"})
	child := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "parentId": str(env, "id"), "name": "Nested local"})
	doc.Models = doc.Models[:2]
	p = testImportPlan(t, e, doc, wid)
	deletes := 0
	for _, item := range p.Items {
		if item.Action == "delete" {
			deletes++
			if item.Selected {
				t.Fatal("deletion selected by default")
			}
			item.Selected = true
		}
	}
	if deletes != 4 {
		t.Fatalf("previewed %d of 4 deletions", deletes)
	}
	for _, item := range p.Items {
		if item.ID == str(child, "id") {
			item.Selected = false
		}
	}
	if _, err := e.CommitImport(t.Context(), p); err == nil {
		t.Fatal("hidden cascade accepted")
	}
	for _, item := range p.Items {
		if item.Action == "delete" {
			item.Selected = true
		}
	}
	commitTestImport(t, e, p)
	for _, id := range []string{fid, str(env, "id"), str(child, "id")} {
		if _, err := e.Store.Get(t.Context(), id); err == nil {
			t.Fatalf("%s survived", id)
		}
	}
}

func TestImportMoveOutOfDeletedFolder(t *testing.T) {
	e := testEngine(t)
	doc := importDocumentForTest()
	p := testImportPlan(t, e, doc, "")
	wid := str(p.NewWorkspaces()[0], "id")
	rid := planItem(t, p, "http_request:r").ID
	commitTestImport(t, e, p)
	doc.Models[3]["folderId"] = nil
	doc.Models = append(doc.Models[:2], doc.Models[3])
	p = testImportPlan(t, e, doc, wid)
	planItem(t, p, "folder:f").Selected = true
	commitTestImport(t, e, p)
	r, err := e.Store.Get(t.Context(), rid)
	if err != nil || str(r, "folderId") != "" {
		t.Fatal(r, err)
	}
}

func TestImportRejectsStaleAndMutatedPreviews(t *testing.T) {
	e := testEngine(t)
	doc := importDocumentForTest()
	p := testImportPlan(t, e, doc, "")
	wid := str(p.NewWorkspaces()[0], "id")
	rid := planItem(t, p, "http_request:r").ID
	commitTestImport(t, e, p)
	doc.Models[3]["url"] = "https://example.test/update"
	p = testImportPlan(t, e, doc, wid)
	saveTest(t, e, Object{"model": "http_request", "id": rid, "name": "Changed after preview"})
	if _, err := e.CommitImport(t.Context(), p); err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Fatal(err)
	}
	p = testImportPlan(t, e, doc, wid)
	planItem(t, p, "http_request:r").After["url"] = "https://unpreviewed.test"
	if _, err := e.CommitImport(t.Context(), p); err == nil {
		t.Fatal("mutated preview accepted")
	}
	if _, err := e.planImportDocuments(t.Context(), []ImportDocument{doc, doc}, ImportDestination{WorkspaceID: wid}); err == nil {
		t.Fatal("duplicate source accepted")
	}
}

func TestImportRejectsParentCycles(t *testing.T) {
	for _, kind := range []string{"folder", "environment"} {
		t.Run(kind, func(t *testing.T) {
			e := testEngine(t)
			doc := ImportDocument{Models: []Object{{"model": "workspace", "id": "w"}, {"model": kind, "workspaceId": "w", "id": "a", "folderId": "b", "parentId": "b", "parentModel": kind}, {"model": kind, "workspaceId": "w", "id": "b", "folderId": "a", "parentId": "a", "parentModel": kind}}}
			if _, err := e.planImportDocuments(t.Context(), []ImportDocument{doc}, ImportDestination{}); err == nil {
				t.Fatal("cycle accepted")
			}
		})
	}
}

func TestImportFileLinksPersistAndTemplatesRemap(t *testing.T) {
	e := testEngine(t)
	doc := importDocumentForTest()
	doc.Models = append(doc.Models, Object{"model": "http_request", "id": "second", "workspaceId": "w", "url": `${[ response.body(request='r') ]}`})
	resources := Object{}
	for collection, kind := range resourceKinds {
		for _, m := range doc.Models {
			if str(m, "model") == kind {
				resources[collection] = append(array(resources, collection), m)
			}
		}
	}
	data, _ := json.Marshal(Object{"resources": resources})
	path := filepath.Join(t.TempDir(), "api.yaak")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	path, _ = filepath.EvalSymlinks(path)
	p, err := e.PlanImport(t.Context(), []ImportInput{{Origin: path}}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	wid := str(p.NewWorkspaces()[0], "id")
	rid := planItem(t, p, "http_request:r").ID
	if !strings.Contains(str(planItem(t, p, "http_request:second").After, "url"), rid) {
		t.Fatal("template not remapped")
	}
	commitTestImport(t, e, p)
	sources, _ := e.Store.List(t.Context(), "import_source", wid)
	if len(sources) != 1 || str(sources[0], "origin") != path {
		t.Fatal(sources)
	}
	p, err = e.PlanImport(t.Context(), []ImportInput{{Origin: path}}, ImportDestination{WorkspaceID: wid})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range p.Items {
		if item.Action != "unchanged" {
			t.Fatal(item)
		}
	}
	if err = e.Delete(t.Context(), str(sources[0], "id")); err != nil {
		t.Fatal(err)
	}
	bindings, _ := e.Store.List(t.Context(), "import_source_resource", wid)
	if len(bindings) != 0 {
		t.Fatal("orphaned bindings")
	}
	if _, err = e.Store.Get(t.Context(), rid); err != nil {
		t.Fatal("unlink removed requests", err)
	}
}
