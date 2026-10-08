package engine

import (
	"bytes"
	"encoding/hex"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

type memorySecrets map[string]string

func (m memorySecrets) Get(s, u string) (string, error) {
	v, ok := m[s+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}
func (m memorySecrets) Set(s, u, v string) error { m[s+u] = v; return nil }
func TestEncryptionYaakCompatibility(t *testing.T) {
	encoded := "7941346b336e43017c7cb13467eecaa963b11734be636f9cc4152de348584fb27e95d1a70973e557cd335cf29e12d0d305d63ca0aa168f1b17003b1690a9d49140f0"
	data, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptBytes(bytes.Repeat([]byte{7}, 32), data)
	if err != nil || string(plain) != "yaak golden vector" {
		t.Fatal(string(plain), err)
	}
	key := []byte("f1a2d4b3c8e799af1456be3478a4c3f2")
	if got := humanKey(key); got != "YKCRRP-2CK46H-H36RSR-CMVKJE-B1CRRK-8D9PC9-JK6D1Q-71GK8R-SKCRS0" {
		t.Fatal(got)
	}
	e := testEngine(t)
	e.secrets = memorySecrets{}
	w := saveTest(t, e, Object{"model": "workspace"})
	wid := str(w, "id")
	if err = e.EnableEncryption(t.Context(), wid); err != nil {
		t.Fatal(err)
	}
	secured, err := e.SecureValue(t.Context(), wid, "credential")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := e.Render(t.Context(), secured, wid, "", "")
	if err != nil || rendered != "credential" {
		t.Fatal(rendered, err)
	}
	all, _ := e.Store.List(t.Context(), "", "")
	if bytes.Contains([]byte(jsonString(all)), []byte("credential")) {
		t.Fatal("plaintext stored in database")
	}
}
func TestSyncRoundTripAndConflict(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "name": "Sync"})
	wid := str(w, "id")
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": "https://example.com/original"})
	dir := t.TempDir()
	plan, err := e.PlanSync(t.Context(), wid, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ApplySync(t.Context(), wid, dir, plan, ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "yaak."+str(r, "id")+".yaml")
	raw, err := os.ReadFile(path) // #nosec G304 -- path is generated inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var disk Object
	if err = yaml.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	disk["url"] = "https://example.com/disk"
	raw, err = yaml.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = e.PlanSync(t.Context(), wid, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ApplySync(t.Context(), wid, dir, plan, ""); err != nil {
		t.Fatal(err)
	}
	updated, err := e.Store.Get(t.Context(), str(r, "id"))
	if err != nil || str(updated, "url") != "https://example.com/disk" {
		t.Fatal(updated, err)
	}
	updated["url"] = "https://example.com/local"
	saveTest(t, e, updated)
	disk["url"] = "https://example.com/both"
	raw, _ = yaml.Marshal(disk)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err = e.PlanSync(t.Context(), wid, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.ApplySync(t.Context(), wid, dir, plan, ""); err == nil {
		t.Fatal("conflict was overwritten")
	}
	if err = e.ApplySync(t.Context(), wid, dir, plan, "push"); err != nil {
		t.Fatal(err)
	}
}
func TestResponseFiltersAndSSE(t *testing.T) {
	got, err := FilterResponse(`{"items":[{"id":1},{"id":2}]}`, "$.items[*].id")
	if err != nil {
		t.Fatal(err)
	}
	var values []int
	if err = json.Unmarshal([]byte(got), &values); err != nil || len(values) != 2 || values[1] != 2 {
		t.Fatal(got, err)
	}
	events := ParseSSE("id: 1\r\nevent: update\r\ndata: one\r\ndata: two\r\n\r\ndata: next\n\n")
	if len(events) != 2 || events[0].Data != "one\ntwo" || events[1].ID != "1" {
		t.Fatal(events)
	}
}
func TestNestedTemplatesAndBrunoImport(t *testing.T) {
	rendered, err := renderText("${[ base64.encode(value=url.encode(value='a b')) ]}", nil, map[string]bool{}, 0)
	if err != nil || rendered != "YSUyMGI=" {
		t.Fatal(rendered, err)
	}
	source := `meta {
 name: Create user
 type: http
}
post {
 url: {{host}}/users
}
body:json {
 {"name":"Ada", "nested":{"value":"}"}}
}
headers {
 Content-Type: application/json
}
`
	models, err := ParseImport([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || str(models[1], "method") != "POST" || str(models[1], "bodyType") != "application/json" {
		t.Fatal(models)
	}
}

func TestGraphQLIntrospectionAndFormatting(t *testing.T) {
	if _, err := FormatGraphQL(introspectionQuery); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body Object
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		if !strings.Contains(str(body, "query"), "__schema") {
			t.Error("missing introspection query")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[{"name":"hello","type":{"kind":"SCALAR","name":"String"}}]}]}}}`))
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": server.URL, "bodyType": "graphql"})
	schema, err := e.GraphQLSchema(t.Context(), str(r, "id"), "")
	if err != nil || str(obj(schema, "queryType"), "name") != "Query" {
		t.Fatal(schema, err)
	}
	stored, err := e.Store.Find(t.Context(), "graphql_introspection", "requestId", str(r, "id"))
	if err != nil || len(stored) != 1 {
		t.Fatal(stored, err)
	}
}
