package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

const nativeGraphQLSchema = `type Query {user(id: ID!): User} type User {name: String!, friend: User}`

func TestGraphQLNativeCompletionAndUndo(t *testing.T) {
	a, p := templateTestApp()
	g, err := engine.LoadGraphQLSchema([]byte(nativeGraphQLSchema))
	if err != nil {
		t.Fatal(err)
	}
	d := &Draft{ID: a.active, Kind: "http_request", BodyType: "graphql", Model: engine.Object{"body": engine.Object{"disableAutoIntrospect": true}}}
	a.drafts = map[string]*Draft{a.active: d}
	a.graphQLState(a.active).index = g
	tt := ui.NewTester(func(c *ui.Context) { a.nativeEditor(c, p, &d.Query, "GraphQL query", "graphql", false) }, 650, 350)
	if err = tt.Click("GraphQL query"); err != nil {
		t.Fatal(err)
	}
	tt.Type(`{user(id:"日本"){na`)
	if _, ok := tt.Find("Complete GraphQL name"); !ok {
		t.Fatal(tt.Texts())
	}
	tt.Key(0, ui.KeyTab)
	if d.Query != `{user(id:"日本"){name` {
		t.Fatalf("completion=%q", d.Query)
	}
	tt.Command("undo")
	if d.Query != `{user(id:"日本"){na` {
		t.Fatalf("completion undo=%q", d.Query)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(`{user(id:${[ bas`)
	if _, ok := tt.Find("Complete base_url"); !ok {
		t.Fatalf("template suggestions unavailable inside GraphQL: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if d.Query != `{user(id:${[ base_url ]}` {
		t.Fatalf("template provider=%q", d.Query)
	}
}

func TestGraphQLNativeArgumentsMouseAndDiagnostics(t *testing.T) {
	a, p := templateTestApp()
	g, err := engine.LoadGraphQLSchema([]byte(nativeGraphQLSchema))
	if err != nil {
		t.Fatal(err)
	}
	d := &Draft{ID: a.active, Kind: "http_request", BodyType: "graphql", Model: engine.Object{"body": engine.Object{"disableAutoIntrospect": true}}}
	a.drafts = map[string]*Draft{a.active: d}
	a.graphQLState(a.active).index = g
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			a.graphQLQueryHeader(c, p, d)
			a.nativeEditor(c, p, &d.Query, "GraphQL query", "graphql", false)
			a.graphQLProblems(c, p, d)
		})
	}, 700, 450)
	if err = tt.Click("GraphQL query"); err != nil {
		t.Fatal(err)
	}
	tt.Type(`{user(i`)
	if err = tt.Click("Complete GraphQL id"); err != nil {
		t.Fatal(err)
	}
	if d.Query != `{user(id: ` || !tt.Focused("GraphQL query") {
		t.Fatal(d.Query)
	}
	d.Query = `{user(id:"1"){unknown}}`
	tt.Frame()
	if !tt.HasText("Cannot query field") {
		t.Fatalf("validation not visible: %v", tt.Texts())
	}
	marks := a.graphQLMarks("GraphQL query", p)
	if len(marks) == 0 || string([]rune(d.Query)[marks[0].Start:marks[0].End]) != "unknown" {
		t.Fatal(marks)
	}
	issue := a.graphQLState(a.active).diagnostics[0]
	a.editorDocument("GraphQL query").state.GoTo(issue.Line, issue.Column)
	tt.Frame()
	if a.editorDocument("GraphQL query").state.Caret != marks[0].Start {
		t.Fatal("diagnostic navigation lost its source location")
	}
}

func TestGraphQLSchemaFileRestoreAndOperationSelection(t *testing.T) {
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	w, err := e.Save(t.Context(), engine.Object{"model": "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schema.graphql")
	if err = os.WriteFile(path, []byte(nativeGraphQLSchema), 0600); err != nil {
		t.Fatal(err)
	}
	query := `query First{user(id:"1"){name}} query Second{user(id:"2"){name}}`
	r, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": s(w, "id"), "bodyType": "graphql", "body": engine.Object{"query": query, "variables": "{}", "schemaFilePath": path, "disableAutoIntrospect": true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.LoadGraphQLSchemaFile(t.Context(), s(r, "id"), path, "fixture"); err != nil {
		t.Fatal(err)
	}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.cancel)
	a.testMode = true
	a.openRequest(s(r, "id"))
	d := a.drafts[a.active]
	d.Tab = 0
	tt := ui.NewTester(a.View, 1360, 860)
	if a.graphQLIndex(d) == nil {
		t.Fatal("file schema was not restored from storage")
	}
	if d.OperationName != "First" {
		t.Fatal("first named operation was not selected")
	}
	if err = tt.Click("GraphQL operation: First"); err != nil {
		t.Fatal(err)
	}
	if err = tt.ChooseMenuItem("Second"); err != nil {
		t.Fatal(err)
	}
	if s(o(d.object(), "body"), "operationName") != "Second" {
		t.Fatal("operation was not persisted in the request body")
	}
	if err = tt.Click("Format"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Query, "\n") || d.OperationName != "Second" {
		t.Fatal("format lost the selected operation")
	}
	a.editorDocument("GraphQL query").state.Command("undo")
	tt.Frame()
	if d.Query != query {
		t.Fatalf("format could not be undone: %q", d.Query)
	}
	if utf8.RuneCountInString(d.Query) == 0 {
		t.Fatal("lost query")
	}
}

func TestGraphQLOperationDefaultFollowsAnonymousQuery(t *testing.T) {
	a, p := templateTestApp()
	d := &Draft{ID: a.active, Query: "{__typename}", Variables: "{}", Model: engine.Object{"body": engine.Object{}}}
	tt := ui.NewTester(func(c *ui.Context) { a.graphQLQueryHeader(c, p, d) }, 700, 80)
	if d.OperationExplicit || o(d.object(), "body")["operationName"] != nil {
		t.Fatal("anonymous query incorrectly saved an explicit empty operation")
	}
	d.Query = "query Next{__typename}"
	tt.Frame()
	if d.OperationName != "Next" {
		t.Fatal("named operation was not selected after anonymous query")
	}
	d.OperationName = ""
	d.OperationExplicit = true
	d.Query = "query Other{__typename}"
	tt.Frame()
	if d.OperationName != "" {
		t.Fatal("explicit unspecified operation was overwritten")
	}
}
