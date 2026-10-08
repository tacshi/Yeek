package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestResponseBreadcrumbFollowsCaretAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"title":{"id":"4506473","guaranteeStatus":"Guarantee"}}],"total":1}`))
	}))
	defer server.Close()
	a, e := cookieApp(t)
	m, _ := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Titles", "url": server.URL})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	r, _ := e.SendHTTP(t.Context(), s(m, "id"), engine.SendOptions{})
	a.applyModel(r)
	a.loadResponseBody(r)
	d := a.drafts[a.active]
	tt := ui.NewTester(a.View, 1360, 860)
	if tt.HasText("Clear filter") {
		t.Fatal("breadcrumb shown with the caret at the root")
	}
	pretty := d.PrettyBody
	offset := len([]rune(pretty[:strings.Index(pretty, `"Guarantee"`)+3]))
	a.editorDocument(responseBodyKey(r)).state.Select(offset, offset)
	tt.Frame()
	tt.Frame()
	for _, text := range []string{"data", "[0]", "title", "guaranteeStatus"} {
		if !tt.HasText(text) {
			t.Fatalf("breadcrumb missing %q: %v", text, tt.Texts())
		}
	}
	if err := tt.Click("Filter to title"); err != nil {
		t.Fatal(err)
	}
	if d.ResponseFilter != "$.data[0].title" {
		t.Fatalf("filter = %q", d.ResponseFilter)
	}
	tt.Frame()
	if err := tt.Click("Clear filter"); err != nil {
		t.Fatal(err)
	}
	if d.ResponseFilter != "" {
		t.Fatalf("filter not cleared: %q", d.ResponseFilter)
	}
}

func TestResponseEditorCaretAndFoldCursor(t *testing.T) {
	a, _ := cookieApp(t)
	m, _ := a.Engine.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Read only"})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	d := a.drafts[a.active]
	a.applyModel(engine.Object{"model": "http_response", "id": "rs_caret", "requestId": d.ID, "state": "closed", "status": float64(200), "headers": []any{engine.Object{"name": "Content-Type", "value": "application/json"}}})
	a.bodies["rs_caret"] = `{"data":{"id":1,"name":"Ada"}}`
	tt := ui.NewTester(a.View, 1360, 860)
	editor, ok := tt.Find("Response body")
	if !ok {
		t.Fatal("response editor missing")
	}
	// Line 1 opens a fold; its chevron sits 10 points left of the text, in the gutter.
	lineY := editor.Y + 10 + 12*1.75/2
	tt.Move(editor.X+28, lineY)
	if tt.Cursor() != ui.CursorPointer {
		t.Fatalf("cursor over fold chevron = %v", tt.Cursor())
	}
	// Pressing the chevron keeps the pointing hand.
	tt.Press(editor.X+28, lineY)
	if tt.Cursor() != ui.CursorPointer {
		t.Fatalf("cursor pressing fold chevron = %v", tt.Cursor())
	}
	tt.Release(editor.X+28, lineY)
	tt.ClickAt(editor.X+28, lineY) // the press folded the line: unfold it again
	tt.Move(editor.X+140, lineY)
	if tt.Cursor() != ui.CursorText {
		t.Fatalf("cursor over text = %v", tt.Cursor())
	}
	tt.ClickAt(editor.X+70, lineY+12*1.75)
	state := a.editorDocument("Response body:rs_caret").state
	if !state.Focused || state.Caret == 0 {
		t.Fatalf("click did not place a caret: %+v", state)
	}
}
