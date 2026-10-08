package engine

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}
func saveTest(t *testing.T, e *Engine, m Object) Object {
	t.Helper()
	v, err := e.Save(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestHTTPRoundTrip(t *testing.T) {
	e := testEngine(t)
	var gotMethod, gotHeader, gotBody, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Environment")
		gotPath = r.URL.RequestURI()
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "example", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode}) // #nosec G124 -- loopback HTTP fixture verifies non-TLS cookie replay.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"created":true}`))
	}))
	defer server.Close()
	workspace := saveTest(t, e, Object{"model": "workspace", "name": "API", "headers": []any{Object{"name": "X-Environment", "value": "base"}}})
	wid := str(workspace, "id")
	if err := e.Store.EnsureWorkspace(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	env := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "variables": []any{Object{"name": "host", "value": server.URL}, Object{"name": "value", "value": "staging"}}})
	request := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "method": "POST", "url": "${[ host ]}/items/:id", "urlParameters": []any{Object{"name": ":id", "value": "a b"}, Object{"name": "q", "value": "x&y"}}, "headers": []any{Object{"name": "X-Environment", "value": "${[ value ]}"}}, "bodyType": "application/json", "body": Object{"text": "{\"stage\":\"${[ value ]}\"}"}})
	response, err := e.SendHTTP(t.Context(), str(request, "id"), SendOptions{EnvironmentID: str(env, "id")})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotHeader != "staging" || gotBody != `{"stage":"staging"}` || gotPath != "/items/a%20b?q=x%26y" {
		t.Fatalf("received %s %s %s %s", gotMethod, gotHeader, gotBody, gotPath)
	}
	if number(response, "status") != 201 || str(response, "state") != "closed" {
		t.Fatalf("response %#v", response)
	}
	raw, err := e.Body(str(response, "id"))
	if err != nil || string(raw) != `{"created":true}` {
		t.Fatalf("body=%s err=%v", raw, err)
	}
	jars, err := e.Store.List(t.Context(), "cookie_jar", wid)
	if err != nil || len(jars) != 1 || len(array(jars[0], "cookies")) != 1 {
		t.Fatalf("cookies=%v err=%v", jars, err)
	}
}
func TestCancelStreamingRequest(t *testing.T) {
	e := testEngine(t)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": server.URL, "method": "GET"})
	done := make(chan error, 1)
	go func() { _, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{}); done <- err }()
	<-started
	e.Cancel(str(r, "id"))
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation must be reported")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop the request")
	}
}
func TestStorePersistenceAndCascade(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := saveTest(t, e, Object{"model": "workspace", "name": "Persistent"})
	folder := saveTest(t, e, Object{"model": "folder", "workspaceId": str(w, "id"), "name": "Group"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "folderId": str(folder, "id"), "name": "Read"})
	env := saveTest(t, e, Object{"model": "environment", "workspaceId": str(w, "id"), "parentModel": "folder", "parentId": str(folder, "id")})
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	if got, err := e.Store.Get(t.Context(), str(r, "id")); err != nil || str(got, "name") != "Read" {
		t.Fatal(got, err)
	}
	if err = e.Delete(t.Context(), str(folder, "id")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{str(folder, "id"), str(r, "id"), str(env, "id")} {
		if _, err = e.Store.Get(t.Context(), id); err == nil {
			t.Fatalf("orphaned %s", id)
		}
	}
}
func TestImportExportRoundTrip(t *testing.T) {
	e := testEngine(t)
	source := []byte(`{"info":{"name":"Pets"},"item":[{"name":"Animals","item":[{"name":"Get pet","request":{"method":"GET","url":"https://example.com/pets/1"}}]}]}`)
	models, err := e.Import(t.Context(), source, "")
	if err != nil {
		t.Fatal(err)
	}
	var wid string
	for _, m := range models {
		if str(m, "model") == "workspace" {
			wid = str(m, "id")
		}
	}
	raw, err := e.Export(t.Context(), wid, false)
	if err != nil {
		t.Fatal(err)
	}
	var doc Object
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	resources := obj(doc, "resources")
	if len(array(resources, "folders")) != 1 || len(array(resources, "httpRequests")) != 1 {
		t.Fatal(string(raw))
	}
	second := testEngine(t)
	if _, err = second.Import(t.Context(), raw, ""); err != nil {
		t.Fatal(err)
	}
}
func TestTemplateCyclesAndCurl(t *testing.T) {
	if _, err := renderText("${[ a ]}", map[string]string{"a": "${[ b ]}", "b": "${[ a ]}"}, map[string]bool{}, 0); err == nil {
		t.Fatal("cycle accepted")
	}
	m, err := ParseCurl("curl 'https://example.com?a=x' -H 'X-Name: a b' --json '{\"x\":1}'")
	if err != nil {
		t.Fatal(err)
	}
	if str(m, "method") != "POST" || str(m, "bodyType") != "application/json" || !strings.Contains(str(obj(m, "body"), "text"), "x") {
		t.Fatal(m)
	}
	_ = context.Background()
}

func TestDisabledFieldsDoNotRender(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	// #nosec G101 -- deliberately unresolved template text, not a credential.
	request := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": "https://example.com", "bodyType": nil, "body": Object{"text": "${[ missing ]}"}, "headers": []any{Object{"name": "X-Disabled", "value": "${[ absent ]}", "enabled": false}}, "authenticationType": "none", "authentication": Object{"token": "${[ missing ]}"}})
	if _, err := e.resolve(t.Context(), request, ""); err != nil {
		t.Fatal(err)
	}
}
