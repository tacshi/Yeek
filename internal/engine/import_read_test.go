package engine

import (
	"compress/gzip"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImportFixture(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestImportOpenAPIExternalReferences(t *testing.T) {
	e := testEngine(t)
	dir := t.TempDir()
	path := writeImportFixture(t, dir, "api.yaml", `openapi: 3.0.3
info: {title: Reference API}
paths:
  /items/{id}:
    $ref: './parts.yaml#/path'
`)
	writeImportFixture(t, dir, "parts.yaml", `path:
  parameters:
    - $ref: '#/parameters/id'
  post:
    operationId: createItem
    requestBody:
      $ref: '#/body'
parameters:
  id: {name: id, in: path, required: true, schema: {type: string, default: '42'}}
body:
  content:
    application/json:
      schema:
        $ref: './schemas.json#/Item~1Object'
`)
	writeImportFixture(t, dir, "schemas.json", `{"Item/Object":{"type":"object","properties":{"name":{"type":"string","example":"Widget"},"child":{"$ref":"#/Item~1Object"}}}}`)
	doc, err := e.ReadImport(t.Context(), ImportInput{Origin: path}, "")
	if err != nil {
		t.Fatal(err)
	}
	var request Object
	for _, m := range doc.Models {
		if str(m, "model") == "http_request" {
			request = m
		}
	}
	if str(request, "method") != "POST" || !strings.Contains(str(request, "url"), "/items/:id") || str(objects(array(request, "urlParameters"))[0], "value") != "42" {
		t.Fatal(request)
	}
	var body Object
	if err = json.Unmarshal([]byte(str(obj(request, "body"), "text")), &body); err != nil || str(body, "name") != "Widget" {
		t.Fatal(body, err)
	}
	if len(str(obj(request, "body"), "text")) > 10000 {
		t.Fatal("unbounded recursive schema")
	}
	// Selecting the API file grants access to its source tree, not arbitrary files.
	writeImportFixture(t, filepath.Dir(dir), filepath.Base(dir)+"-outside.json", `{"type":"string"}`)
	defer func() { _ = os.Remove(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-outside.json")) }()
	writeImportFixture(t, dir, "api.yaml", "openapi: 3.0.3\ninfo: {title: API}\npaths: {}\ncomponents:\n  schemas:\n    X: {$ref: '../"+filepath.Base(dir)+"-outside.json'}\n")
	if _, err = e.ReadImport(t.Context(), ImportInput{Origin: path}, ""); err == nil || !strings.Contains(err.Error(), "source directory") {
		t.Fatal(err)
	}
}

func TestImportBrunoFolderAndStableRequestIDs(t *testing.T) {
	e := testEngine(t)
	dir := t.TempDir()
	writeImportFixture(t, dir, "bruno.json", `{"name":"Bruno API"}`)
	writeImportFixture(t, dir, "collection.bru", "headers {\n X-Collection: yes\n}\nvars {\n host: https://example.test\n}")
	writeImportFixture(t, dir, "Items/folder.bru", "meta {\n name: Items\n}\nvars {\n kind: widgets\n}")
	writeImportFixture(t, dir, "Items/read.bru", "meta {\n name: Read\n}\nget {\n url: {{host}}/items\n}")
	writeImportFixture(t, dir, "environments/Staging.bru", "vars {\n host: https://staging.test\n}")
	writeImportFixture(t, dir, "node_modules/ignore.bru", "invalid")
	p, err := e.PlanImport(t.Context(), []ImportInput{{Kind: "folder", Origin: dir}}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	wid := str(p.NewWorkspaces()[0], "id")
	if str(p.NewWorkspaces()[0], "name") != "Bruno API" {
		t.Fatal(p.NewWorkspaces())
	}
	var rid string
	for _, item := range p.Items {
		if item.Kind == "http_request" {
			rid = item.ID
			if item.ParentID == "" {
				t.Fatal("lost hierarchy")
			}
		}
	}
	commitTestImport(t, e, p)
	writeImportFixture(t, dir, "Items/read.bru", "meta {\n name: Renamed\n}\nget {\n url: {{host}}/items\n}")
	p, err = e.PlanImport(t.Context(), []ImportInput{{Kind: "folder", Origin: dir}}, ImportDestination{WorkspaceID: wid})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range p.Items {
		if item.Kind == "http_request" && (item.ID != rid || item.Action != "update") {
			t.Fatal(item)
		}
	}
}

func TestImportURLAndRemoteReferences(t *testing.T) {
	e := testEngine(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.json":
			http.Redirect(w, r, "/nested/api.json", http.StatusFound)
		case "/nested/api.json":
			w.Header().Set("Content-Encoding", "gzip")
			compressed := gzip.NewWriter(w)
			_, _ = compressed.Write([]byte(`{"openapi":"3.0.3","info":{"title":"Remote"},"paths":{"/items":{"post":{"requestBody":{"$ref":"./parts.json#/Body"}}}}}`))
			_ = compressed.Close()
		case "/nested/parts.json":
			_, _ = w.Write([]byte(`{"Body":{"content":{"application/json":{"example":{"ok":true}}}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	doc, err := e.ReadImport(t.Context(), ImportInput{Kind: "url", Origin: server.URL + "/api.json"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var request Object
	for _, m := range doc.Models {
		if str(m, "model") == "http_request" {
			request = m
		}
	}
	if doc.Origin != server.URL+"/api.json" || len(doc.Models) != 4 || !strings.Contains(str(obj(request, "body"), "text"), `"ok": true`) {
		t.Fatal(doc)
	}
	if _, err = e.ReadImport(t.Context(), ImportInput{Kind: "url", Origin: server.URL + "/missing"}, ""); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatal(err)
	}
}

func TestImportPostmanPreservesInheritanceAndURLParameters(t *testing.T) {
	e := testEngine(t)
	doc, err := e.ReadImport(t.Context(), ImportInput{Kind: "text", Content: `{
"info":{"name":"Postman"},"auth":{"type":"basic","basic":[{"key":"username","value":"collection-user"},{"key":"password","value":"fixture-password"}]},
"event":[{"listen":"prerequest","script":{"exec":["console.log('retained');"]}}],
"item":[{"id":"folder","name":"Items","variable":[{"key":"count","value":5}],"item":[
{"id":"read","name":"Read","request":{"url":{"raw":"{{host}}/items/:id?disabled=yes&q=search","query":[{"key":"disabled","value":"yes","disabled":true},{"key":"q","value":"search"}],"variable":[{"key":"id","value":42}]},"description":{"content":"Description object"},"method":"GET"}},
{"id":"upload","name":"Upload","request":{"url":{"protocol":"https","host":["example","test"],"path":["upload"]},"method":"POST","auth":{"type":"noauth"},"body":{"mode":"file","file":{"src":"/tmp/body.bin"}}}}
]}]}`}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Warnings) != 1 {
		t.Fatal("scripts silently discarded", doc.Warnings)
	}
	p, err := e.planImportDocuments(t.Context(), []ImportDocument{doc}, ImportDestination{})
	if err != nil {
		t.Fatal(err)
	}
	commitTestImport(t, e, p)
	var request, upload Object
	for _, item := range p.Items {
		if item.Name == "Read" {
			request, _ = e.Store.Get(t.Context(), item.ID)
		}
		if item.Name == "Upload" {
			upload = item.After
		}
	}
	params := objects(array(request, "urlParameters"))
	if len(params) != 3 || enabled(params[0]) || str(params[2], "value") != "42" || strings.Contains(str(request, "url"), "?") || str(request, "description") != "Description object" {
		t.Fatal(request)
	}
	if str(upload, "url") != "https://example.test/upload" || str(upload, "authenticationType") != "none" || str(obj(upload, "body"), "filePath") != "/tmp/body.bin" {
		t.Fatal(upload)
	}
	request["url"] = "https://example.test"
	resolved, err := e.resolve(t.Context(), request, "")
	if err != nil || str(resolved.Model, "authenticationType") != "basic" || str(obj(resolved.Model, "authentication"), "username") != "collection-user" {
		t.Fatal(resolved, err)
	}
}
