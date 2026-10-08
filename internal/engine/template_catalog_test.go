package engine

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTemplateExpressionRoundTrip(t *testing.T) {
	for _, source := range []string{
		`${[ base64.encode(value="日本\n\t\x00\"\\", encoding="base64") ]}`,
		`${[ json.jsonpath(input=response.body.raw(request='abc', behavior='never'), query='$.rows', formatted=true, limit=12.5) ]}`,
		`${[ base_url ]}`,
	} {
		before, err := ParseTemplateExpression(source)
		if err != nil {
			t.Fatal(err)
		}
		after, err := ParseTemplateExpression(FormatTemplateTag(before))
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("round trip %s: %#v, %v", source, after, err)
		}
	}
	value := "日本\n\t\x00\"\\"
	expression := TemplateExpression{Kind: "function", Value: "base64.encode", Arguments: map[string]TemplateExpression{"value": {Kind: "string", Value: value}}}
	got, err := renderText(FormatTemplateTag(expression), nil, map[string]bool{}, 0)
	if err != nil || got != base64.StdEncoding.EncodeToString([]byte(value)) {
		t.Fatalf("escaped value: %q %v", got, err)
	}
	// Like Yaak, null arguments are left out when a tag is written.
	if got := FormatTemplateTag(TemplateExpression{Kind: "function", Value: "fn", Arguments: map[string]TemplateExpression{"n": {Kind: "null"}, "a": {Kind: "string", Value: "aaa"}}}); got != "${[ fn(a='aaa') ]}" {
		t.Fatal(got)
	}
	for _, source := range []string{`name() junk`, `name(value="\uXYZW")`, `name(value=)`, `${[ name()`} {
		if _, err := ParseTemplateExpression(source); err == nil {
			t.Fatalf("accepted malformed template %q", source)
		}
	}
}

func TestTemplatePreviewDoesNotExecuteSensitiveFunctionsOrRequests(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	wid := str(w, "id")
	for _, name := range []string{"fs", "fs.read", "fs.readFile", "secure", "keyring", "keychain", "extension.function"} {
		_, err := e.PreviewTemplate(t.Context(), "${[ "+name+"() ]}", wid, "", "", "")
		if err == nil || !strings.Contains(err.Error(), "evaluated when") {
			t.Errorf("preview allowed %s: %v", name, err)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte("saved")) }))
	defer server.Close()
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": server.URL})
	id := str(r, "id")
	for _, behavior := range []string{"smart", "always", "ttl", "never"} {
		_, err := e.PreviewTemplate(t.Context(), `${[ response.body.raw(request="`+id+`", behavior="`+behavior+`") ]}`, wid, "", "", "")
		if err == nil {
			t.Fatal("preview should report no saved response")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("preview sent the referenced request")
	}
	if _, err := e.SendHTTP(t.Context(), id, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := e.PreviewTemplate(t.Context(), `${[ response.body.raw(request="`+id+`", behavior="always") ]}`, wid, "", "", "")
	if err != nil || got != "saved" || calls.Load() != 1 {
		t.Fatalf("saved preview=%q calls=%d err=%v", got, calls.Load(), err)
	}
}

func TestTemplateFileAndContext(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	wid := str(w, "id")
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("  日本\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value := TemplateExpression{Kind: "function", Value: "fs.readFile", Arguments: map[string]TemplateExpression{"path": {Kind: "string", Value: path}, "trim": {Kind: "boolean", Value: "true"}}}
	got, err := e.Render(t.Context(), FormatTemplateTag(value), wid, "", "")
	if err != nil || got != "日本" {
		t.Fatalf("file: %q %v", got, err)
	}
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": "https://example.com/${[ ctx.workspace() ]}/${[ ctx.request() ]}"})
	resolved, err := e.resolve(t.Context(), r, "")
	if err != nil || str(resolved.Model, "url") != "https://example.com/"+wid+"/"+str(r, "id") {
		t.Fatalf("context: %#v %v", resolved.Model, err)
	}
}

func TestVariableSnapshotMatchesExecution(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	wid := str(w, "id")
	base := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "workspace", "parentId": wid, "variables": []any{Object{"name": "host", "value": "base"}, Object{"name": "base", "value": "yes"}}})
	parent := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "parentId": str(base, "id"), "variables": []any{Object{"name": "host", "value": "parent"}}})
	child := saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "environment", "parentId": str(parent, "id"), "variables": []any{Object{"name": "host", "value": "child"}}})
	folder := saveTest(t, e, Object{"model": "folder", "workspaceId": wid})
	_ = saveTest(t, e, Object{"model": "environment", "workspaceId": wid, "parentModel": "folder", "parentId": str(folder, "id"), "variables": []any{Object{"name": "host", "value": "folder"}, Object{"name": "disabled", "value": "hidden", "enabled": false}}})
	models, err := e.Snapshot(t.Context(), wid)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := VariablesFromModels(models, wid, str(folder, "id"), str(child, "id"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := e.Variables(t.Context(), wid, str(folder, "id"), str(child, "id"))
	if err != nil || !reflect.DeepEqual(vars, runtime) || vars["host"] != "folder" || vars["base"] != "yes" || vars["disabled"] != "" {
		t.Fatalf("variables: %#v %#v %v", vars, runtime, err)
	}
	if _, err = VariablesFromModels(models, wid, "missing", ""); err == nil {
		t.Fatal("missing folder accepted")
	}
}
