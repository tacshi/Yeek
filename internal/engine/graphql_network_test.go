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

const networkGraphQLSchema = `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[{"name":"message","args":[],"type":{"kind":"SCALAR","name":"String"}}]}]}}}`

func TestGraphQLIntrospectionUsesDraftCookiesAndCompressedResponse(t *testing.T) {
	e := testEngine(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/current" || r.Method != "POST" {
			t.Errorf("used saved request instead of draft: %s %s", r.Method, r.URL)
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "fixture" || password != "secret" {
			t.Error("missing inherited authentication")
		}
		if r.Header.Get("X-Environment") != "staging" {
			t.Error("environment header was not resolved")
		}
		if len(r.Header.Values("Content-Type")) != 1 {
			t.Error("duplicate content type")
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "fixture" {
			t.Error("selected cookie jar was not sent")
		}
		var payload Object
		if err = json.UnmarshalRead(r.Body, &payload); err != nil || !strings.Contains(str(payload, "query"), "__schema") {
			t.Error("not an introspection request")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(networkGraphQLSchema))
		_ = gz.Close()
	}))
	defer server.Close()
	w := saveTest(t, e, Object{"model": "workspace", "authenticationType": "basic", "authentication": Object{"username": "fixture", "password": "secret"}})
	wid := str(w, "id")
	env := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "variables": []any{Object{"name": "stage", "value": "staging"}}})
	jar := saveTest(t, e, Object{"model": "cookie_jar", "workspaceId": wid, "cookies": []any{Object{"name": "session", "value": "fixture", "domain": Object{"HostOnly": "127.0.0.1"}, "path": "/"}}})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": server.URL + "/saved", "bodyType": "graphql", "body": Object{"query": "{message}"}, "headers": []any{Object{"name": "Content-Type", "value": "application/json"}, Object{"name": "X-Environment", "value": "${[ stage ]}"}}})
	draft := clone(r)
	draft["url"] = server.URL + "/current"
	raw, err := e.IntrospectGraphQL(t.Context(), draft, SendOptions{EnvironmentID: str(env, "id"), CookieJarID: str(jar, "id")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.StoreGraphQLSchema(t.Context(), str(r, "id"), string(raw), "draft-key", ""); err != nil {
		t.Fatal(err)
	}
	saved, err := e.Store.Get(t.Context(), str(r, "id"))
	if err != nil {
		t.Fatal(err)
	}
	if str(saved, "url") != server.URL+"/saved" || str(obj(saved, "body"), "query") != "{message}" {
		t.Fatal("introspection changed the saved request")
	}
	responses, err := e.Store.Find(t.Context(), "http_response", "requestId", str(r, "id"))
	if err != nil || len(responses) != 0 {
		t.Fatal("introspection created response history")
	}
	models, err := e.Snapshot(t.Context(), wid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range models {
		if str(m, "model") == "graphql_introspection" {
			found = str(m, "schemaKey") == "draft-key"
		}
	}
	if !found {
		t.Fatal("schema cache missing from workspace snapshot")
	}
}

func TestGraphQLSchemaFilesAndRequestOperation(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id")})
	for _, content := range []string{languageSchema, networkGraphQLSchema} {
		path := filepath.Join(t.TempDir(), "schema")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		m, err := e.LoadGraphQLSchemaFile(t.Context(), str(r, "id"), path, "key")
		if err != nil || str(m, "schemaFile") != path {
			t.Fatal(m, err)
		}
	}
	body, _, err := requestBody(Object{"bodyType": "graphql", "body": Object{"query": "query A {message} query B {message}", "operationName": "B", "variables": "{}"}})
	if err != nil {
		t.Fatal(err)
	}
	var payload Object
	if err = json.Unmarshal(body, &payload); err != nil || str(payload, "operationName") != "B" {
		t.Fatal(string(body), err)
	}
}
