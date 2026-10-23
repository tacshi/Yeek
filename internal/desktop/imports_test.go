package desktop

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

const importFixture = `{"info":{"name":"Imported API"},"item":[{"id":"get","name":"Read Items","request":{"method":"GET","url":"https://example.test/v1/items"}}]}`

func writeImportFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestImportDialogPreviewsThenApplies(t *testing.T) {
	a, e := cookieApp(t)
	before := a.workspace
	tt := ui.NewTester(a.View, 1360, 860)
	a.importFile()
	tt.Frame()
	for _, text := range []string{"Import Data", "files", "folder", "URL", "Supports OpenAPI, Swagger, Postman, Insomnia, Bruno, curl, and Yaak exports", "Import into", "Cancel", "Preview Import"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	path := filepath.Join(t.TempDir(), "collection.json")
	writeImportFixture(t, path, importFixture)
	a.ImportPath(path)
	tt.Frame()
	if !tt.HasText("collection.json") || !tt.HasText("Postman") || !tt.HasText("Add files, a folder, or a URL") {
		t.Fatalf("source card: %v", tt.Texts())
	}
	if err := tt.Click("Preview Import"); err != nil {
		t.Fatal(err)
	}
	if a.imports.plan == nil {
		t.Fatal(a.imports.error)
	}
	tt.Frame()
	for _, text := range []string{"Detected format", "Postman", "New workspace", "Imported API", "Read Items", "new", "Variables", "Back"} {
		if !tt.HasText(text) {
			t.Fatalf("preview missing %q: %v", text, tt.Texts())
		}
	}
	if models, _ := e.Store.List(t.Context(), "http_request", ""); len(models) != 0 {
		t.Fatal("preview imported data")
	}
	apply := ""
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "Apply ") {
			apply = text
		}
	}
	if apply == "" {
		t.Fatalf("no apply button: %v", tt.Texts())
	}
	if err := tt.Click(apply); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dialog != "import_complete" || !tt.HasText("1 Workspace") || !tt.HasText("1 HTTP Request") {
		t.Fatalf("dialog %s: %v", a.dialog, tt.Texts())
	}
	if a.workspace == before || s(a.models[a.workspace], "name") != "Imported API" || s(a.models[a.environment], "model") != "environment" {
		t.Fatalf("workspace %v environment %q", a.models[a.workspace], a.environment)
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	if a.dialogOpen {
		t.Fatal("Import Complete stayed open")
	}
}

func TestImportDialogReimportResolvesConflicts(t *testing.T) {
	a, e := cookieApp(t)
	path := filepath.Join(t.TempDir(), "collection.json")
	writeImportFixture(t, path, importFixture)
	tt := ui.NewTester(a.View, 1360, 860)
	a.ImportPath(path)
	a.previewImport()
	a.applyImport()
	a.dialogOpen = false
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
	writeImportFixture(t, path, strings.ReplaceAll(importFixture, "/v1/items", "/v2/items"))

	a.importFile()
	tt.Frame()
	real, _ := filepath.EvalSymlinks(path)
	if len(a.imports.sources) != 1 || a.imports.sources[0].path != real || a.importDestinationKind(a.imports) != "current" {
		t.Fatalf("sources %v destination %s", a.imports.sources, a.importDestinationKind(a.imports))
	}
	if err := tt.Click("Preview Import"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Keep mine") || !tt.HasText("Take source") || !tt.HasText("Destination") {
		t.Fatalf("conflict row: %v", tt.Texts())
	}
	if !tt.HasText("Done") {
		t.Fatalf("a kept conflict is no change: %v", tt.Texts())
	}
	if err := tt.Click("Take source"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Apply 1 Change"); err != nil {
		t.Fatalf("%v: %v", err, tt.Texts())
	}
	requests, _ = e.Store.List(t.Context(), "http_request", wid)
	if len(requests) != 1 || s(requests[0], "id") != rid || s(requests[0], "url") != "https://example.test/v2/items" || s(requests[0], "description") != "" {
		t.Fatal(requests)
	}
	if a.drafts[rid] == nil || a.drafts[rid].URL != "https://example.test/v2/items" {
		t.Fatal("active draft did not refresh")
	}
}

func TestImportDialogSelectionAndFailedSources(t *testing.T) {
	a, e := cookieApp(t)
	a.openImport([]engine.ImportInput{{Kind: "text", Content: importFixture}}, engine.ImportDestination{WorkspaceID: a.workspace})
	a.previewImport()
	tt := ui.NewTester(a.View, 1360, 860)
	tt.Frame()
	if err := tt.Click("Toggle Read Items"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Apply 1 Change") {
		t.Fatalf("unchecking: %v", tt.Texts())
	}
	if err := tt.Click("Toggle Read Items"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Apply 2 Changes"); err != nil {
		t.Fatalf("%v: %v", err, tt.Texts())
	}
	if requests, _ := e.Store.List(t.Context(), "http_request", a.workspace); len(requests) != 1 {
		t.Fatal(requests)
	}
	a.dialogOpen = false
	a.ImportPath(filepath.Join(t.TempDir(), "missing.json"))
	tt.Frame()
	if d := a.imports.detections[a.imports.sources[0].path]; d == nil || d.status != "error" || !tt.HasText(d.message) {
		t.Fatalf("failed source: %+v %v", d, tt.Texts())
	}
	if err := tt.Click("Preview Import"); err != nil {
		t.Fatal(err)
	}
	if a.imports.plan != nil {
		t.Fatal("previewed a failed source")
	}
	if err := tt.Click("Remove missing.json"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.imports.sources) != 0 || !tt.HasText("Supports OpenAPI, Swagger, Postman, Insomnia, Bruno, curl, and Yaak exports") {
		t.Fatal("source not removed")
	}
}

func TestImportDeepLinkOpensTheImportedWorkspace(t *testing.T) {
	data, err := os.ReadFile("../engine/testdata/insomnia/basic.input.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()

	a, _ := cookieApp(t)
	before := a.workspace
	var asked string
	a.confirmMessage = func(message, confirm string) bool { asked = message + "|" + confirm; return false }
	a.ImportDeepLink(server.URL+"/insomnia.json", "", "My API")
	if asked != "Import My API from "+server.URL+"/insomnia.json?|Import" || a.workspace != before {
		t.Fatalf("asked %q, workspace %s", asked, a.workspace)
	}

	a.confirmMessage = func(string, string) bool { return true }
	a.ImportDeepLink(server.URL+"/insomnia.json", "", "")
	if a.workspace == before || s(a.models[a.workspace], "name") != "Dummy" {
		t.Fatalf("workspace %v", a.models[a.workspace])
	}
	if env := a.models[a.environment]; s(env, "model") != "environment" || s(env, "workspaceId") != a.workspace {
		t.Fatalf("environment %v", env)
	}
	if last := a.toasts[len(a.toasts)-1]; last.message != "Imported data for 1 workspaces" {
		t.Fatal(last.message)
	}

	path := filepath.Join(t.TempDir(), "insomnia.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	asked = ""
	a.confirmMessage = func(message, _ string) bool { asked = message; return false }
	a.ImportDeepLink("", path, "")
	if asked != "" || s(a.models[a.workspace], "name") != "Dummy" {
		t.Fatalf("asked %q for a path", asked)
	}
	a.ImportDeepLink("", "", "")
	if last := a.toasts[len(a.toasts)-1]; last.message != "Failed to import data" {
		t.Fatal(last.message)
	}
}

func TestProtoFilesShowAsAPathList(t *testing.T) {
	a, e := cookieApp(t)
	m, _ := e.Save(t.Context(), engine.Object{"model": "grpc_request", "workspaceId": a.workspace, "name": "Greeter"})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	a.setProtoFiles(s(m, "id"), []string{"/home/me/protos/api/v1/greeter.proto", "/home/me/protos/include"})
	a.prompt("grpc_schema", "Configure Schema", "", s(m, "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	tt.Frame()
	for _, text := range []string{"greeter.proto", "…/protos/api/v1", "include", "home/me/protos", "Add proto files or an import folder", "Refresh Schema", "Done"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	if err := tt.Click("Remove greeter.proto"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if files := a.protoFiles(s(m, "id")); len(files) != 1 || files[0] != "/home/me/protos/include" {
		t.Fatal(files)
	}
}

func TestSplitPathMatchesYaak(t *testing.T) {
	for path, want := range map[string][2]string{
		"/a/b/c/d/e.json":                    {"e.json", "…/b/c/d"},
		"/a/b/e.json":                        {"e.json", "a/b"},
		`C:\Users\me\api.yaml`:               {"api.yaml", "C:/Users/me"},
		"https://example.com/specs/api.json": {"api.json", "example.com/specs"},
		"https://example.com/api.json":       {"api.json", "example.com"},
		"https://example.com/":               {"example.com", ""},
		"collection.json":                    {"collection.json", ""},
	} {
		if name, parent := splitPath(path); name != want[0] || parent != want[1] {
			t.Errorf("%s: %q %q", path, name, parent)
		}
	}
}
