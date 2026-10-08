package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

const breadcrumbFixture = `{
  "data": [
    {
      "title": {
        "id": "4506473",
        "guaranteeStatus": "Guarantee",
        "a.b c": [1, 2]
      }
    }
  ],
  "total": 1
}`

func caretAfter(t *testing.T, text, marker string) int {
	t.Helper()
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("marker %q missing", marker)
	}
	return len([]rune(text[:i+len(marker)]))
}

func TestJSONPathAtCaret(t *testing.T) {
	o := newJSONOutline(breadcrumbFixture)
	for _, tc := range []struct{ name, marker, want string }{
		{"end of member line", `"Guarantee",`, `$.data[0].title.guaranteeStatus`},
		{"inside key", `"guaran`, `$.data[0].title.guaranteeStatus`},
		{"inside value", `"45064`, `$.data[0].title.id`},
		{"array element", `[1, 2`, `$.data[0].title["a.b c"][1]`},
		{"object opener", `"title": {`, `$.data[0].title`},
		{"array opener", `"data": [`, `$.data`},
		{"top-level member", `"total": 1`, `$.total`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := o.pathAt(breadcrumbFixture, caretAfter(t, breadcrumbFixture, tc.marker))
			if got := jsonPathString(path, len(path)); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	if path := o.pathAt(breadcrumbFixture, 0); len(path) != 0 || path == nil {
		t.Fatalf("root caret = %v", path)
	}
	if path := newJSONOutline("not json").pathAt("not json", 3); len(path) != 0 {
		t.Fatalf("plain text path = %v", path)
	}
	if path := newJSONOutline("").pathAt("", 0); path != nil {
		t.Fatal("empty text has a path")
	}
}

func TestJSONPathUnicodeAndTruncation(t *testing.T) {
	text := "{\n  \"名前\": \"漢字\",\n  \"next\": [tr"
	o := newJSONOutline(text)
	path := o.pathAt(text, caretAfter(t, text, `"漢字",`))
	if got := jsonPathString(path, len(path)); got != `$["名前"]` {
		t.Fatal(got)
	}
	path = o.pathAt(text, caretAfter(t, text, `"next"`))
	if got := jsonPathString(path, len(path)); got != `$.next` {
		t.Fatal(got)
	}
}

func TestParseJSONPath(t *testing.T) {
	for _, expr := range []string{`$`, `$.data[0].title`, `$["a.b c"][2].x`, `$.$id`, `$["$"]`} {
		path, ok := parseJSONPath(expr)
		if !ok {
			t.Fatalf("%s not parsed", expr)
		}
		if got := jsonPathString(path, len(path)); got != expr {
			t.Fatalf("round trip %s = %s", expr, got)
		}
	}
	for _, expr := range []string{`$.items[*].id`, `$..id`, `data.id`, `$[?(@.a)]`, `$.a[`} {
		if _, ok := parseJSONPath(expr); ok {
			t.Fatalf("%s should not be a plain path", expr)
		}
	}
	if got := jsonPathString([]pathSegment{{key: "a"}, {index: 3, isIndex: true}}, 1); got != "$.a" {
		t.Fatal(got)
	}
}

func TestResponsePrettyPrintKeepsOrderAndDigits(t *testing.T) {
	a, e := cookieApp(t)
	m, _ := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Order"})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	d := a.drafts[a.active]
	response := engine.Object{"model": "http_response", "id": "rs_order", "requestId": d.ID, "state": "closed", "status": float64(200), "headers": []any{engine.Object{"name": "Content-Type", "value": "application/json"}}}
	a.applyModel(response)
	a.bodies["rs_order"] = `{"zeta":1,"alpha":12345678901234567890,"path":"a\/b é"}`
	ui.NewTester(a.View, 1100, 700)
	want := "{\n  \"zeta\": 1,\n  \"alpha\": 12345678901234567890,\n  \"path\": \"a/b é\"\n}"
	if d.PrettyBody != want {
		t.Fatalf("pretty body:\n%s", d.PrettyBody)
	}
}

func TestFormatRequestBodyKeepsOrderAndDigits(t *testing.T) {
	a, d, _ := composerApp(t, "application/json", engine.Object{"text": `{"zeta":1,"alpha":12345678901234567890,"nested":{"b":2,"a":1}}`})
	a.formatRequestBody(d)
	ui.NewTester(a.View, 1100, 700)
	want := "{\n  \"zeta\": 1,\n  \"alpha\": 12345678901234567890,\n  \"nested\": {\n    \"b\": 2,\n    \"a\": 1\n  }\n}"
	if d.Body != want {
		t.Fatalf("formatted body:\n%s", d.Body)
	}
}
