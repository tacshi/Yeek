package engine

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportWorkspaceSelectionAndPrivateHierarchy(t *testing.T) {
	e := testEngine(t)
	first := saveTest(t, e, Object{"model": "workspace", "name": "First"})
	second := saveTest(t, e, Object{"model": "workspace", "name": "Second"})
	wid := str(first, "id")
	base := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "workspace", "parentId": wid, "name": "Base", "variables": []any{Object{"name": "base", "value": "public"}}})
	private := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "parentId": str(base, "id"), "name": "Private", "public": false, "variables": []any{Object{"name": "private", "value": "private-fixture-value"}}})
	child := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "parentId": str(private, "id"), "name": "Public Child"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "name": "Read", "url": "https://example.test"})
	saveTest(t, e, Object{"model": "http_response", "workspaceId": wid, "requestId": str(r, "id"), "url": "https://example.test", "headers": []any{Object{"name": "X-History", "value": "history-fixture-value"}}})
	saveTest(t, e, Object{"model": "cookie_jar", "workspaceId": wid, "name": "Jar", "cookies": []any{Object{"name": "session", "value": "cookie-fixture-value"}}})
	saveTest(t, e, Object{"model": "websocket_request", "workspaceId": str(second, "id"), "name": "Stream", "url": "wss://example.test"})
	data, err := e.ExportWorkspaces(t.Context(), ExportOptions{WorkspaceIDs: []string{wid, str(second, "id"), wid}})
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"private-fixture-value", "history-fixture-value", "cookie-fixture-value"} {
		if strings.Contains(string(data), excluded) {
			t.Fatal("excluded data exported", excluded)
		}
	}
	var document Object
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	resources := obj(document, "resources")
	if len(array(resources, "workspaces")) != 2 || len(array(resources, "httpRequests")) != 1 || len(array(resources, "websocketRequests")) != 1 || number(document, "yaakSchema") != 5 || str(document, "timestamp") == "" {
		t.Fatal(document)
	}
	for _, env := range objects(array(resources, "environments")) {
		if str(env, "id") == str(child, "id") && str(env, "parentId") != str(base, "id") {
			t.Fatal("public child references excluded private parent", env)
		}
	}
	// The saved public export is a valid collection in a clean profile.
	other := testEngine(t)
	if _, err = other.Import(t.Context(), data, ""); err != nil {
		t.Fatal(err)
	}
	data, err = e.ExportWorkspaces(t.Context(), ExportOptions{WorkspaceIDs: []string{wid}, IncludePrivateEnvironments: true})
	if err != nil || !strings.Contains(string(data), "private-fixture-value") {
		t.Fatal(err, string(data))
	}
	if err = json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, env := range objects(array(obj(document, "resources"), "environments")) {
		if str(env, "id") == str(child, "id") && str(env, "parentId") != str(private, "id") {
			t.Fatal("private export lost hierarchy")
		}
	}
}

func TestExportFileIsAtomicAndRestricted(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "name": "API"})
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	path := filepath.Join(dir, "export.json")
	if err := os.WriteFile(path, []byte("previous export"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.ExportToFile(t.Context(), path, ExportOptions{WorkspaceIDs: []string{"missing"}}); err == nil {
		t.Fatal("missing workspace accepted")
	}
	if data, _ := root.ReadFile("export.json"); string(data) != "previous export" {
		t.Fatal("failed export destroyed existing file")
	}
	if err := e.ExportToFile(t.Context(), path, ExportOptions{WorkspaceIDs: []string{str(w, "id")}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	data, err := root.ReadFile("export.json")
	var document Object
	if err != nil || json.Unmarshal(data, &document) != nil {
		t.Fatal(err)
	}
	if _, err = e.ExportWorkspaces(t.Context(), ExportOptions{}); err == nil {
		t.Fatal("empty selection accepted")
	}
}
