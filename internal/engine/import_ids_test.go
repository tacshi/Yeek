package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func keptIDDocument() ImportDocument {
	return ImportDocument{Format: "Yeek", Label: "API", Models: []Object{
		{"model": "workspace", "id": "wk_source", "name": "API"},
		{"model": "environment", "id": "ev_base", "workspaceId": "wk_source", "parentModel": "workspace", "name": "Base"},
		{"model": "folder", "id": "fl_group", "workspaceId": "wk_source", "name": "Group"},
		{"model": "http_request", "id": "rq_read", "workspaceId": "wk_source", "folderId": "fl_group", "name": "Read", "url": "https://example.test/a"},
	}}
}

func itemByID(p *ImportPlan, id string) *ImportItem {
	for _, item := range p.Items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

func hasWarning(p *ImportPlan, title string) *ImportWarning {
	for i := range p.Warnings {
		if p.Warnings[i].Title == title {
			return &p.Warnings[i]
		}
	}
	return nil
}

func TestImportKeepsYeekIDs(t *testing.T) {
	e := testEngine(t)
	p, err := e.planImportDocuments(t.Context(), []ImportDocument{keptIDDocument()}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"wk_source", "ev_base", "fl_group", "rq_read"} {
		if item := itemByID(p, id); item == nil || item.Action != "create" {
			t.Fatalf("%s not kept: %+v", id, item)
		}
	}
	if str(itemByID(p, "rq_read").After, "folderId") != "fl_group" {
		t.Fatal("parent not kept")
	}
	commitTestImport(t, e, p)

	again, err := e.planImportDocuments(t.Context(), []ImportDocument{keptIDDocument()}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	if itemByID(again, "rq_read") != nil || itemByID(again, "wk_source") != nil {
		t.Fatal("a second copy reused IDs another workspace has")
	}
	warning := hasWarning(again, "New IDs assigned")
	if warning == nil || warning.Detail != "4 resources already exist in API, so the imported copies get new IDs" {
		t.Fatalf("%+v", again.Warnings)
	}
}

func TestImportUpdatesMatchedIDsInPlace(t *testing.T) {
	e := testEngine(t)
	p, err := e.planImportDocuments(t.Context(), []ImportDocument{keptIDDocument()}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	commitTestImport(t, e, p)

	stale := keptIDDocument()
	stale.Models[3] = Object{"model": "http_request", "id": "rq_read", "workspaceId": "wk_source", "folderId": "fl_group", "name": "Read", "url": "https://example.test/old", "updatedAt": "2000-01-01T00:00:00"}
	if read := itemByID(testImportPlan(t, e, stale, "wk_source"), "rq_read"); read == nil || read.Action != "conflict" || read.Resolution != "keep_mine" {
		t.Fatalf("an export older than the local copy should conflict: %+v", read)
	}

	doc := keptIDDocument()
	doc.Models[3] = Object{"model": "http_request", "id": "rq_read", "workspaceId": "wk_source", "folderId": "fl_group", "name": "Read", "url": "https://example.test/b", "updatedAt": "9999-01-01T00:00:00"}
	plan := testImportPlan(t, e, doc, "wk_source")
	read, group, base := itemByID(plan, "rq_read"), itemByID(plan, "fl_group"), itemByID(plan, "ev_base")
	if read == nil || read.Action != "update" || !read.Selected || strings.Join(read.ChangedFields, ",") != "url" {
		t.Fatalf("request: %+v", read)
	}
	if group == nil || group.Action != "unchanged" || group.Selected {
		t.Fatalf("folder: %+v", group)
	}
	if base == nil || base.Action != "unchanged" || str(base.After, "parentModel") != "workspace" || strings.HasSuffix(str(base.After, "name"), "(Imported)") {
		t.Fatalf("base environment: %+v", base)
	}
	if hasWarning(plan, "Base environment kept separate") != nil || hasWarning(plan, "New IDs assigned") != nil {
		t.Fatalf("%+v", plan.Warnings)
	}
	commitTestImport(t, e, plan)
	updated, err := e.Store.Get(t.Context(), "rq_read")
	if err != nil || str(updated, "url") != "https://example.test/b" {
		t.Fatal(updated, err)
	}
}

func TestImportOtherFormatsGetFreshIDs(t *testing.T) {
	e := testEngine(t)
	doc := keptIDDocument()
	doc.Format = "Postman"
	p, err := e.planImportDocuments(t.Context(), []ImportDocument{doc}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	if itemByID(p, "rq_read") != nil || itemByID(p, "fl_group") != nil {
		t.Fatal("kept a placeholder ID")
	}
}

func TestImportKeepsOnlyWellFormedIDs(t *testing.T) {
	for id, kind := range map[string]string{"rq_x": "http_request", "fl_a-b_c": "folder"} {
		if !hasImportIDPrefix(id, kind) {
			t.Errorf("%s rejected", id)
		}
	}
	for id, kind := range map[string]string{"rq_": "http_request", "fl_x": "http_request", "rq_a b": "http_request", "r": "http_request"} {
		if hasImportIDPrefix(id, kind) {
			t.Errorf("%s kept as %s", id, kind)
		}
	}
}

func TestImportBreaksFolderCyclesThroughTheDestination(t *testing.T) {
	e := testEngine(t)
	p, err := e.planImportDocuments(t.Context(), []ImportDocument{keptIDDocument()}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	commitTestImport(t, e, p)
	inner, err := e.Save(t.Context(), Object{"model": "folder", "id": "fl_inner", "workspaceId": "wk_source", "folderId": "fl_group", "name": "Inner"})
	if err != nil {
		t.Fatal(err)
	}
	doc := ImportDocument{Format: "Yeek", Label: "API", Models: []Object{
		{"model": "workspace", "id": "wk_source", "name": "API"},
		{"model": "folder", "id": "fl_group", "workspaceId": "wk_source", "folderId": str(inner, "id"), "name": "Group"},
	}}
	plan := testImportPlan(t, e, doc, "wk_source")
	group := itemByID(plan, "fl_group")
	if group == nil || str(group.After, "folderId") != "" {
		t.Fatalf("cycle kept: %+v", group)
	}
	if hasWarning(plan, "Folder moved to the top level") == nil {
		t.Fatalf("%+v", plan.Warnings)
	}
}

func TestImportIntoAFolderNeverKeepsItsAncestors(t *testing.T) {
	e := testEngine(t)
	p, err := e.planImportDocuments(t.Context(), []ImportDocument{keptIDDocument()}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	commitTestImport(t, e, p)
	plan, err := e.planImportDocuments(t.Context(), []ImportDocument{keptIDDocument()}, ImportDestination{WorkspaceID: "wk_source", FolderID: "fl_group"})
	if err != nil {
		t.Fatal(err)
	}
	if itemByID(plan, "fl_group") != nil {
		t.Fatal("the destination folder was imported into itself")
	}
}

func TestReimportedExportConflictsOnlyWithLaterEdits(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "name": "API"})
	wid := str(w, "id")
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "name": "Read", "url": "https://example.test/a"})
	data, err := e.ExportWorkspaces(t.Context(), ExportOptions{WorkspaceIDs: []string{wid}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "api.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	plan := func() *ImportItem {
		t.Helper()
		p, err := e.PlanImport(t.Context(), []ImportInput{{Origin: path}}, ImportDestination{WorkspaceID: wid})
		if err != nil {
			t.Fatal(err)
		}
		return itemByID(p, str(r, "id"))
	}
	if item := plan(); item == nil || item.Action != "unchanged" {
		t.Fatalf("untouched: %+v", item)
	}
	time.Sleep(5 * time.Millisecond)
	r["url"] = "https://example.test/edited"
	saveTest(t, e, r)
	if item := plan(); item == nil || item.Action != "conflict" || item.Resolution != "keep_mine" {
		t.Fatalf("edited after export: %+v", item)
	}
}
