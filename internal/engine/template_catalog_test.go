package engine

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
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

// Yaak renders every function in a preview, the prompt aside; response
// functions send as their behavior says, "always" as "smart".
func TestTemplatePreviewRendersLikeYaak(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	wid := str(w, "id")
	path := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(path, []byte("from file"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := e.PreviewTemplate(t.Context(), "${[ fs.readFile(path='"+path+"') ]}", wid, "", "", ""); err != nil || got != "from file" {
		t.Fatalf("file preview %q %v", got, err)
	}
	if got, err := e.PreviewTemplate(t.Context(), "${[ prompt.text(label='Token') ]}", wid, "", "", ""); err != nil || got != "" {
		t.Fatalf("a prompt previews as nothing: %q %v", got, err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte("saved")) }))
	defer server.Close()
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": server.URL})
	id := str(r, "id")
	preview := func(behavior, ttl string) string {
		got, err := e.PreviewTemplate(t.Context(), `${[ response.body.raw(request='`+id+`', behavior='`+behavior+`', ttl='`+ttl+`') ]}`, wid, "", "", "")
		if err != nil {
			t.Fatal(behavior, err)
		}
		return got
	}
	if got := preview("never", ""); got != "" || calls.Load() != 0 {
		t.Fatalf("never: %q %d", got, calls.Load())
	}
	if got := preview("smart", ""); got != "saved" || calls.Load() != 1 {
		t.Fatalf("smart without a response sends: %q %d", got, calls.Load())
	}
	if got := preview("smart", ""); got != "saved" || calls.Load() != 1 {
		t.Fatalf("smart with one doesn't: %q %d", got, calls.Load())
	}
	if got := preview("always", ""); got != "saved" || calls.Load() != 1 {
		t.Fatalf("always previews as smart: %q %d", got, calls.Load())
	}
	if got := preview("ttl", "0"); got != "saved" || calls.Load() != 1 {
		t.Fatalf("a ttl of 0 never expires: %q %d", got, calls.Load())
	}
	// Sending, "always" sends every time.
	if got, err := e.Render(t.Context(), `${[ response.body.raw(request='`+id+`', behavior='always') ]}`, wid, "", ""); err != nil || got != "saved" || calls.Load() != 2 {
		t.Fatalf("always: %q %v %d", got, err, calls.Load())
	}
}

func TestResponseExpiredMatchesYaak(t *testing.T) {
	old := Object{"createdAt": time.Now().UTC().Add(-time.Minute).Format("2006-01-02T15:04:05.000000000")}
	for _, c := range []struct {
		response Object
		ttl      string
		want     bool
	}{{nil, "0", true}, {old, "0", false}, {old, "", false}, {old, "30", true}, {old, "30.9", true}, {old, "120", false}} {
		if got := responseExpired(c.response, c.ttl); got != c.want {
			t.Errorf("%v %q: %v", c.response != nil, c.ttl, got)
		}
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
