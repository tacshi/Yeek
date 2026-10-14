package desktop

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
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
	source := `${[ regex.match(input=']}', regex='x') ]} / ${[ host ]}`
	tags := templateTags(source)
	if len(tags) != 2 || string([]rune(source)[tags[0].Start:tags[0].End]) != `${[ regex.match(input=']}', regex='x') ]}` {
		t.Fatalf("quoted delimiters: %+v", tags)
	}
}

// Yaak's templateTags.test.ts (#710).
func TestTemplateTagsMatchYaak(t *testing.T) {
	type tag struct {
		start, end int
		inner      string
	}
	find := func(text string) []tag {
		out := []tag{}
		r := []rune(text)
		for _, m := range templateTags(text) {
			out = append(out, tag{m.Start, m.End, string(r[m.Start+3 : m.End-2])})
		}
		return out
	}
	for _, c := range []struct {
		name, text string
		want       []tag
	}{
		{"simple", "${[ my_var ]}", []tag{{0, 13, " my_var "}}},
		{"surrounded by text", `a="${[ my_var ]}"`, []tag{{3, 16, " my_var "}}},
		{"empty", "${[]}", []tag{{0, 5, ""}}},
		{"quoted ]}", "${[ fn(a='x]}y') ]}", []tag{{0, 19, " fn(a='x]}y') "}}},
		{"escaped quote", `${[ fn(a='it\'s ]}') ]}`, []tag{{0, 23, ` fn(a='it\'s ]}') `}}},
		{"b64 string", "${[ b64'Zm9v' ]}", []tag{{0, 16, " b64'Zm9v' "}}},
		{"unterminated quote", "${[ fn(a='unterminated ]} rest", []tag{{0, 25, " fn(a='unterminated "}}},
		{"two tags", "one ${[ a ]} two ${[ b ]} three", []tag{{4, 12, " a "}, {17, 25, " b "}}},
		{"adjacent", "${[ a ]} ${[ b ]}", []tag{{0, 8, " a "}, {9, 17, " b "}}},
		{"two quoting ]}", "${[ a(x='1]}') ]} and ${[ b(y='2]}') ]}", []tag{{0, 17, " a(x='1]}') "}, {22, 39, " b(y='2]}') "}}},
		{"unclosed", "${[ no close", []tag{}},
		{"no tags", `{"a": "${b}", "c": 1}`, []tag{}},
		{"nested opener", "${[ a ${[ b ]}", []tag{{0, 14, " a ${[ b "}}},
	} {
		if got := find(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}

	// Linear on Yaak's adversarial inputs.
	start := time.Now()
	quotes := "${[" + strings.Repeat("'", 20000) + " ]}"
	if got := find(quotes); len(got) != 1 || got[0].end != len([]rune(quotes)) {
		t.Fatalf("quotes: %d tags", len(got))
	}
	if got := find("${[" + strings.Repeat(`\'`, 20000) + " ]}"); len(got) != 1 {
		t.Fatalf("unpairable quotes: %d tags", len(got))
	}
	if got := find(strings.Repeat("${[", 20000)); len(got) != 0 {
		t.Fatalf("openers: %d tags", len(got))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v", elapsed)
	}

	// Never panics, and always reports real bounds.
	alphabet := []rune{'$', '{', '[', ']', '}', '\'', '\\', 'a', 'b'}
	rng := rand.New(rand.NewPCG(0x5eed, 0)) // #nosec G404 -- a seeded generator, so that the cases repeat.
	for range 2000 {
		var b strings.Builder
		for range rng.IntN(31) {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}
		text := b.String()
		previous := 0
		for _, m := range find(text) {
			r := []rune(text)
			if string(r[m.start:m.start+3]) != "${[" || string(r[m.end-2:m.end]) != "]}" || m.start < previous {
				t.Fatalf("%q: %+v", text, m)
			}
			previous = m.end
		}
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
	if err := tt.Click("Save"); err != nil {
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
	// As in Yaak's form, a nested function is text holding its tag.
	if err != nil || got.Arguments["request"].Value != "${[ ctx.request() ]}" || got.Arguments["custom"].Kind != "boolean" {
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

// Yaak's TemplateTagWidget: a tag is a chip that opens its function's
// dialog when clicked, takes the default cursor, and that the caret steps
// over and Backspace takes whole.
func TestTemplateChipsOpenTheirDialog(t *testing.T) {
	a, p := templateTestApp()
	value := `${[ base64.encode(value="日本\ntext", encoding="base64") ]}`
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 700, 600)
	tt.Frame()
	box, ok := tt.Find("URL")
	if !ok {
		t.Fatal(tt.Texts())
	}
	tt.Move(box.X+40, box.Y+box.H/2)
	tt.Frame()
	if tt.Cursor() != ui.CursorDefault {
		t.Fatalf("cursor over a chip: %v", tt.Cursor())
	}
	tt.ClickAt(box.X+40, box.Y+box.H/2)
	tt.Frame()
	if a.templateForm == nil || a.templateForm.name != "base64.encode" || a.templateForm.args["value"].text != "日本\ntext" {
		t.Fatalf("existing tag did not open: %#v", a.templateForm)
	}
	a.templateForm = nil

	doc := a.templateDoc("url")
	value = "ab${[ ctx.workspace() ]}cd"
	doc.state.Select(2, 2)
	doc.state.Focus()
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if doc.state.Caret != 24 {
		t.Fatalf("caret did not step over the chip: %d", doc.state.Caret)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if doc.state.Caret != 2 {
		t.Fatalf("caret did not step back over the chip: %d", doc.state.Caret)
	}
	doc.state.Select(24, 24)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if value != "abcd" {
		t.Fatalf("Backspace left %q", value)
	}
}

// A tag that Yaak's chips don't find, or that spans lines, shows as such.
func TestTemplateTagKindsMatchYaak(t *testing.T) {
	a, _ := templateTestApp()
	tags := a.templateTagKinds("${[ missing ]} ${[ base64.encode(value='x') ]} ${[ ctx.workspace() ]}")
	var got []string
	for _, tag := range tags {
		got = append(got, fmt.Sprint(tag.name, ":", tag.kind, ":", tag.chip))
	}
	want := []string{"missing:invalid:true", "base64.encode:function:true", "ctx.workspace:function:true"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if tags := a.templateTagKinds("${[ missing(a=1,\nb=2) ]}"); len(tags) != 1 || tags[0].chip {
		t.Fatalf("a tag across lines is a chip: %v", tags)
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
	if err = tt.Click("Save"); err != nil {
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
	if len(rows) != 1 || !strings.Contains(rows[0].Value, `value='alpha'`) {
		t.Fatalf("environment insertion not saved: %#v", rows)
	}
}

func TestTemplateSpansKeepUnicodeRanges(t *testing.T) {
	text := `{"日本":"${[ base_url ]}/path"}`
	a, p := templateTestApp()
	spans := templateSyntax(syntaxSpans(text, "json", p), a.templateTagKinds(text), p)
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

func TestPromptTextFormStorageFields(t *testing.T) {
	a, _, _ := treeApp(t)
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		p := a.theme(c)
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 700, 800)
	a.openTemplateForm(a.templateDoc("url"), "", 0, 0, &engine.TemplateExpression{Kind: "function", Value: "prompt.text"})
	tt.Frame()
	if !tt.HasText("Store Input") || tt.HasText("Namespace") || tt.HasText("Prompt Title") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Label argument"); err != nil {
		t.Fatal(err)
	}
	tt.Type("API Key")
	if err := tt.Click("Never"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Forever"); err != nil {
		t.Fatal(tt.Texts())
	}
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Namespace") || !tt.HasText("Key (defaults to Label)") || tt.HasText("TTL (seconds)") {
		t.Fatal(tt.Texts())
	}
	if !tt.HasText("Value will be saved under: " + slugifyForTest(a.workspace) + ".api-key") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Prompt Title") || !tt.HasText("Mask Value") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	expression, err := engine.ParseTemplateExpression(value)
	if err != nil || expression.Arguments["store"].Value != "forever" || expression.Arguments["namespace"].Value != "${[ctx.workspace()]}" {
		t.Fatalf("inserted %q %v", value, err)
	}
}

func slugifyForTest(s string) string { return strings.ToLower(s) }

func TestOnePasswordFormHidesListsUntilSignedIn(t *testing.T) {
	a, _, _ := treeApp(t)
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		p := a.theme(c)
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 700, 800)
	a.openTemplateForm(a.templateDoc("url"), "", 0, 0, &engine.TemplateExpression{Kind: "function", Value: "1password.item"})
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Authentication Method") || !tt.HasText("Service Account") || !tt.HasText("Token") || !strings.Contains(strings.Join(tt.Texts(), " "), "Developer > Service Accounts") {
		t.Fatal(tt.Texts())
	}
	for _, hidden := range []string{"Vault", "Item", "Field"} {
		if slices.Contains(tt.Texts(), hidden) {
			t.Fatalf("%s shown without a client: %v", hidden, tt.Texts())
		}
	}
	if err := tt.Click("Service Account"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Desktop App"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Account Name") {
		t.Fatal(tt.Texts())
	}
}

// Yaak's TemplateFunctionDialog: name(…) and the description above the
// form, a live Rendered Preview, and Save.
func TestTemplateFunctionDialogPreviewsLive(t *testing.T) {
	a, _, _ := treeApp(t)
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		p := a.theme(c)
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 900, 900)
	a.openTemplateForm(a.templateDoc("url"), "", 0, 0, &engine.TemplateExpression{Kind: "function", Value: "base64.encode", Arguments: map[string]engine.TemplateExpression{"value": {Kind: "string", Value: "hi"}}})
	tt.Frame()
	tt.Frame()
	for _, text := range []string{"base64.encode(…)", "Rendered Preview", "aGk= ", "Save"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	if tt.HasText("Insert") || tt.HasText("Cancel") || tt.HasText("Change") {
		t.Fatalf("Yeek's old buttons: %v", tt.Texts())
	}
	if err := tt.Click("Value argument"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("hey")
	tt.Frame()
	tt.Frame()
	if !tt.HasText("aGV5 ") {
		t.Fatalf("preview not live: %v", tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if expression, err := engine.ParseTemplateExpression(value); err != nil || expression.Arguments["value"].Value != "hey" {
		t.Fatalf("saved %q %v", value, err)
	}
}

// Yaak's onClickVariable: a variable's tag opens the environment that has it.
func TestEditTemplateOnAVariableOpensItsEnvironment(t *testing.T) {
	a, e := cookieApp(t)
	base, _ := a.environmentsBreakdown()
	base["variables"] = []any{engine.Object{"name": "token", "value": "x", "enabled": true}}
	saved, _ := e.Save(t.Context(), base)
	a.applyModel(saved)
	a.editVariable("token")
	if a.dialog != "environments" || a.dialogID != s(base, "id") {
		t.Fatalf("dialog %s %s", a.dialog, a.dialogID)
	}
	a.dialogOpen = false
	a.editVariable("missing")
	if a.dialog != "environments" || len(a.envDraft) == 0 || a.envDraft[len(a.envDraft)-1].Name != "missing" {
		t.Fatalf("missing variable: %s %v", a.dialog, a.envDraft)
	}
}

// Yaak's secure() dialog: one password Value, and Reveal Encryption Key.
func TestSecureTemplateDialog(t *testing.T) {
	a, _, _ := treeApp(t)
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		p := a.theme(c)
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 900, 900)
	a.openTemplateForm(a.templateDoc("url"), "", 0, 0, &engine.TemplateExpression{Kind: "function", Value: "secure"})
	tt.Frame()
	if !tt.HasText("Value") || !tt.HasText("Reveal Encryption Key") || !tt.HasText("Save") || tt.HasText("Value argument mode") {
		t.Fatalf("%v", tt.Texts())
	}
	if err := tt.Click("Reveal Encryption Key"); err != nil {
		t.Fatal(err)
	}
	if a.templateForm != nil || a.dialog != "workspace_encryption" {
		t.Fatalf("dialog %s", a.dialog)
	}
}

// Yaak's DynamicForm: an argument is text that may hold tags, rendered
// with it; there's no value or expression mode.
func TestTemplateArgumentsTakeTags(t *testing.T) {
	a, _, _ := treeApp(t)
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		p := a.theme(c)
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.templateDialog(c, p)
	}, 900, 900)
	a.openTemplateForm(a.templateDoc("url"), "", 0, 0, &engine.TemplateExpression{Kind: "function", Value: "base64.encode"})
	tt.Frame()
	if _, found := tt.Find("value argument mode"); found {
		t.Fatal("argument mode switch")
	}
	f := a.templateForm
	f.args["value"].text = "${[ ctx.workspace() ]}"
	f.revision++
	tt.Frame()
	tt.Frame()
	want := base64.StdEncoding.EncodeToString([]byte(a.workspace)) + " "
	if !tt.HasText(want) {
		t.Fatalf("nested tag not rendered: %v", tt.Texts())
	}
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, "value='${[ ctx.workspace() ]}'") {
		t.Fatalf("saved %q", value)
	}
}

// Yaak's withEncryptionEnabled: a secure() chip opens Workspace Encryption
// first when this machine has no key, then its dialog once it's set up,
// back over the dialog the chip was in. Closing it gives up.
func TestSecureChipSetsUpEncryptionFirst(t *testing.T) {
	a, _, _ := treeApp(t)
	value := "${[ secure() ]}"
	tt := ui.NewTester(func(c *ui.Context) {
		p := a.theme(c)
		ui.Column(c).Fill().Children(func() { a.templateInput(c, p, &value, "url", "URL", "", false).FillWidth().Height(32) })
		a.dialogs(c, p)
		a.templateDialog(c, p)
	}, 1100, 800)
	a.prompt("cookies", "Cookies", "", "")
	a.dialogOpen = false
	tt.Frame()
	box, _ := tt.Find("URL")
	click := func() {
		tt.ClickAt(box.X+30, box.Y+box.H/2)
		tt.Frame()
	}
	click()
	if !a.dialogOpen || a.dialog != "workspace_encryption" || a.templateForm != nil {
		t.Fatalf("dialog %q open %v form %v", a.dialog, a.dialogOpen, a.templateForm)
	}
	a.dialogOpen = false
	tt.Frame()
	if a.encryptionGate != nil || a.templateForm != nil || a.dialogOpen {
		t.Fatal("closing Workspace Encryption ran what waited")
	}

	click()
	var meta engine.Object
	for _, m := range a.list("workspace_meta") {
		if s(m, "workspaceId") == a.workspace {
			meta = deepCopy(m)
		}
	}
	if meta == nil {
		t.Fatal("no workspace_meta")
	}
	meta["encryptionKey"] = engine.Object{"encryptedKey": "x"}
	a.applyModel(meta)
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.templateForm == nil || a.templateForm.name != "secure" || a.dialogOpen {
		t.Fatalf("form %v dialog %q open %v", a.templateForm, a.dialog, a.dialogOpen)
	}
	ran := false
	a.withEncryptionEnabled(a.workspace, func() { ran = true })
	if !ran || a.encryptionGate != nil {
		t.Fatal("a set-up workspace asked again")
	}
}

// Encrypt Variables goes through the same gate, and comes back to the
// environment editor whether encryption was set up or not.
func TestEncryptVariablesSetsUpEncryptionFirst(t *testing.T) {
	a, _, _ := treeApp(t)
	tt := ui.NewTester(a.View, 1100, 800)
	a.openEnvironments()
	id := a.dialogID
	tt.Frame()
	a.encryptEnvironment(id)
	tt.Frame()
	if a.dialog != "workspace_encryption" || !a.dialogOpen {
		t.Fatalf("dialog %q", a.dialog)
	}
	a.dialogOpen = false
	tt.Frame()
	if a.dialog != "environments" || !a.dialogOpen || a.dialogID != id || a.encryptionGate != nil {
		t.Fatalf("closing went to %q %v", a.dialog, a.dialogOpen)
	}
	a.encryptEnvironment(id)
	tt.Frame()
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.dialog != "environments" || !a.dialogOpen || a.dialogID != id {
		t.Fatalf("Done went to %q %v", a.dialog, a.dialogOpen)
	}
}
