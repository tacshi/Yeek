package desktop

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestTemplateCompletionRanges(t *testing.T) {
	for _, tt := range []struct {
		source            string
		caret, start, end int
		prefix            string
		ok                bool
	}{
		{"日本 bas tail", 6, 3, 6, "bas", true},
		{"${[ bas ]}/path", 7, 0, 10, "bas", true},
		{"${[ uuid.v4() ]}", 11, 0, 16, "uuid.v4", true},
		{"${[ bas", 7, 0, 7, "bas", true},
		{"${[ ", 4, 0, 4, "", true},
		{"${[ fn(value=oth", 15, 0, 0, "", false},
		{"${[ fn(value=other) ]}", 18, 0, 0, "", false},
		{"", 0, 0, 0, "", false},
		{"https://example.com/json", 23, 0, 0, "", false},
		{"application/json", 16, 0, 0, "", false},
	} {
		start, end, prefix, ok := completionRange(tt.source, tt.caret, false)
		if ok != tt.ok || ok && (start != tt.start || end != tt.end || prefix != tt.prefix) {
			t.Errorf("%q: %d,%d %q %v", tt.source, start, end, prefix, ok)
		}
	}
	source := `${[ regex.match(input="]}", regex="x") ]} / ${[ host ]}`
	tags := templateTags(source)
	if len(tags) != 2 || string([]rune(source)[tags[0].Start:tags[0].End]) != `${[ regex.match(input="]}", regex="x") ]}` {
		t.Fatalf("quoted delimiters: %+v", tags)
	}
}

func templateTestApp() (*App, colors) {
	return &App{active: "request", workspace: "workspace", settings: engine.Object{}, models: map[string]engine.Object{
		"env": {"id": "env", "model": "environment", "workspaceId": "workspace", "parentModel": "workspace", "variables": []any{engine.Object{"name": "base_url", "value": "https://example.com"}, engine.Object{"name": "base_token", "value": "token"}}},
	}}, colors{background: ui.Hex("#ffffff"), text: ui.Hex("#222222"), border: ui.Hex("#dddddd"), accent: ui.Hex("#6633ff"), muted: ui.Hex("#777777")}
}

func TestTemplateNativeAcceptanceNavigationAndSubmit(t *testing.T) {
	a, p := templateTestApp()
	value, next := "", ""
	submitted := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			entry := a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32)
			if entry.Submitted() {
				submitted++
			}
			ui.TextInput(c, &next).Label("Next").FillWidth()
		})
		a.templateDialog(c, p)
	}, 620, 450)
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Ctrl, ui.KeySpace)
	tt.Frame()
	if _, ok := tt.Find("Complete base_token"); !ok {
		t.Fatal("explicit completion did not stay open in an empty field")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Type("日本 ${[ base")
	if _, ok := tt.Find("Complete base_token"); !ok {
		t.Fatal("no completion popup")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if value != "日本 ${[ base_url ]}" || submitted != 0 {
		t.Fatalf("accepted %q, submitted %d", value, submitted)
	}
	tt.Command("undo")
	if value != "日本 ${[ base" {
		t.Fatalf("completion undo: %q", value)
	}
	tt.Key(0, ui.KeyEscape)
	if _, ok := tt.Find("Complete base_token"); ok {
		t.Fatal("Escape did not close popup")
	}
	tt.Key(0, ui.KeyEnter)
	if submitted != 1 {
		t.Fatal("Enter no longer submits URL")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Next") {
		t.Fatal("single line Tab should move focus")
	}
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://example.com/json")
	tt.Key(0, ui.KeyEnter)
	if submitted != 2 || a.templateForm != nil {
		t.Fatal("a URL path was mistaken for a function completion")
	}
}

func TestTemplateNativeFunctionFormAndUndo(t *testing.T) {
	a, p := templateTestApp()
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 700, 600)
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Type("base64.enc")
	tt.Key(0, ui.KeyTab)
	if a.templateForm == nil {
		t.Fatal("no function form")
	}
	if err := tt.Click("Value argument"); err != nil {
		t.Fatal(err)
	}
	tt.Type("日本\ntext")
	if err := tt.Click("Insert"); err != nil {
		t.Fatal(err)
	}
	expression, err := engine.ParseTemplateExpression(value)
	if err != nil || expression.Value != "base64.encode" || expression.Arguments["value"].Value != "日本\ntext" {
		t.Fatalf("inserted %q %v", value, err)
	}
	if !tt.Focused("URL") {
		t.Fatal("focus not restored after insertion")
	}
	tt.Command("undo")
	if value != "base64.enc" {
		t.Fatalf("form undo: %q", value)
	}
}

func TestTemplateFunctionPreservesNestedAndUnknownArguments(t *testing.T) {
	source := `${[ response.body.path(request=ctx.request(), path="$.items", behavior="never", custom=true) ]}`
	expression, err := engine.ParseTemplateExpression(source)
	if err != nil {
		t.Fatal(err)
	}
	f := templateForm{definitions: (*engine.Engine)(nil).TemplateDefinitions()}
	f.selectExpression(expression)
	got, err := f.expression()
	if err != nil || got.Arguments["request"].Kind != "function" || got.Arguments["custom"].Kind != "boolean" {
		t.Fatalf("edited expression: %#v %v", got, err)
	}
	if !strings.Contains(engine.FormatTemplateTag(got), "ctx.request()") {
		t.Fatal("nested function was converted to a string")
	}
}

func TestTemplateCompletionInBodyAndMouseSelection(t *testing.T) {
	a, p := templateTestApp()
	value := ""
	tt := ui.NewTester(func(c *ui.Context) { a.nativeEditor(c, p, &value, "Body", "json", false) }, 600, 260)
	if err := tt.Click("Body"); err != nil {
		t.Fatal(err)
	}
	tt.Type("${[ base_")
	if err := tt.Click("Complete base_url"); err != nil {
		t.Fatal(err)
	}
	if value != "${[ base_url ]}" {
		t.Fatalf("mouse completion: %q", value)
	}
	doc := a.editorDocument("Body")
	if doc.state.Caret != utf8.RuneCountInString(value) {
		t.Fatalf("caret after completion: %d", doc.state.Caret)
	}
}

func TestTemplateNativeContextMenuEditsExistingTag(t *testing.T) {
	a, p := templateTestApp()
	value := `${[ base64.encode(value="日本\ntext", encoding="base64") ]}`
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 700, 600)
	if err := tt.Click("URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Shift, ui.KeyF10)
	if err := tt.ChooseMenuItem("Edit Template…"); err != nil {
		t.Fatal(err)
	}
	if a.templateForm == nil || a.templateForm.name != "base64.encode" || a.templateForm.args["value"].text != "日本\ntext" {
		t.Fatalf("existing tag did not open: %#v", a.templateForm)
	}
}

func TestTemplateFormInsideEnvironmentDialog(t *testing.T) {
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	w, err := e.Save(t.Context(), engine.Object{"model": "workspace", "name": "Test"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.Save(t.Context(), engine.Object{"model": "environment", "workspaceId": s(w, "id"), "parentModel": "workspace", "parentId": s(w, "id"), "variables": []any{engine.Object{"name": "value", "value": "original"}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.cancel)
	a.testMode = true
	a.openEnvironments()
	a.dialogID = s(env, "id")
	a.envDraft = kvRows(env, "variables")
	tt := ui.NewTester(a.View, 1100, 720)
	if err = tt.Click("Variable 1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("base64")
	tt.Key(0, ui.KeyTab)
	if a.templateForm != nil || !tt.Focused("Variable value 1") {
		t.Fatal("variable names must remain literal identifiers")
	}
	if err = tt.Click("Variable value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("base64.enc")
	tt.Key(0, ui.KeyEnter)
	if a.templateForm == nil || !a.dialogOpen {
		t.Fatal("nested template form did not open")
	}
	if !tt.Focused("Value argument") {
		t.Fatal("function input did not receive initial focus")
	}
	tt.Type("alpha")
	if err = tt.Click("Insert"); err != nil {
		t.Fatal(err)
	}
	if !a.dialogOpen || a.templateForm != nil {
		t.Fatal("inserting closed the parent environment dialog")
	}
	if !tt.Focused("Variable value 1") {
		t.Fatal("focus was not restored to environment value")
	}
	saved, err := e.Store.Get(t.Context(), s(env, "id"))
	if err != nil {
		t.Fatal(err)
	}
	rows := kvRows(saved, "variables")
	if len(rows) != 1 || !strings.Contains(rows[0].Value, `value="alpha"`) {
		t.Fatalf("environment insertion not saved: %#v", rows)
	}
}

func TestTemplateSpansKeepUnicodeRanges(t *testing.T) {
	text := `{"日本":"${[ base_url ]}/path"}`
	_, p := templateTestApp()
	spans := templateSyntax(syntaxSpans(text, "json", p), text, p.accent)
	tag := templateTags(text)[0]
	end := 0
	for _, span := range spans {
		if span.Start < end || span.End < span.Start || span.End > utf8.RuneCountInString(text) {
			t.Fatalf("invalid spans: %#v", spans)
		}
		if span.Start >= tag.Start && span.End <= tag.End && span.Color != p.accent {
			t.Fatalf("template range not colored: %#v", span)
		}
		end = span.End
	}
}
