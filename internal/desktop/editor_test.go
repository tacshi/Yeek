package desktop

import (
	"strings"
	"testing"
	"unicode/utf8"
	"yeek/internal/engine"

	"github.com/egoist/mygo/yeekui"
)

func TestUnwrappedEditorKeepsCaretVisible(t *testing.T) {
	value := ""
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() {
			ui.TextAreaBase(c, &value).Label("Code").Fill().Font("monospace").FontSize(12).NoWrap().AutoFocus()
		})
	}, 280, 150)
	tt.Type(strings.Repeat("abcdefghij", 30))
	bounds, ok := tt.Find("Code")
	if !ok {
		t.Fatal("editor not found")
	}
	caret, ok := tt.TextCaret()
	if !ok || caret.X < bounds.X || caret.X+caret.W > bounds.X+bounds.W+1 {
		t.Fatalf("caret is outside the editor: caret=%+v bounds=%+v", caret, bounds)
	}
}

func TestNativeCodeEditingIndentCompositionAndUndo(t *testing.T) {
	value := ""
	var state ui.CodeEditorState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{Wrap: true, LineNumbers: true, AutoIndent: true, CloseBrackets: true}).Label("Code").Fill().AutoFocus()
		})
	}, 360, 180)
	tt.Type("{")
	tt.Key(0, ui.KeyEnter)
	if value != "{\n  \n}" || state.Line != 2 || state.Column != 3 {
		t.Fatalf("auto-indent: %q, state=%+v", value, state)
	}
	tt.Type("\"")
	before := value
	tt.Compose("日本", 2)
	if value != before {
		t.Fatalf("composition committed prematurely: %q", value)
	}
	tt.Type("日本")
	if value != "{\n  \"日本\"\n}" {
		t.Fatalf("composition: %q", value)
	}
	tt.Command("undo")
	if value != before {
		t.Fatalf("undo: %q expected %q", value, before)
	}
	tt.Command("redo")
	if !strings.Contains(value, "日本") {
		t.Fatalf("redo: %q", value)
	}
}

func TestNativeCodeTabAndBatchUndo(t *testing.T) {
	value := "α\nβ"
	var state ui.CodeEditorState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{Wrap: true, TabSize: 2}).Label("Code").Fill().AutoFocus()
	}, 360, 180)
	state.Select(0, 3)
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	if value != "  α\n  β" || !tt.Focused("Code") {
		t.Fatalf("indent/focus: %q focused=%v", value, tt.Focused("Code"))
	}
	tt.Key(ui.Shift, ui.KeyTab)
	if value != "α\nβ" {
		t.Fatalf("outdent: %q", value)
	}
	state.Apply([]ui.TextEdit{{Start: 0, End: 1, Text: "first"}, {Start: 2, End: 3, Text: "second"}})
	tt.Frame()
	if value != "first\nsecond" {
		t.Fatalf("batch replacement: %q", value)
	}
	tt.Command("undo")
	if value != "α\nβ" {
		t.Fatalf("batch did not undo atomically: %q", value)
	}
}

func TestNativeCodePreservesDocumentHistory(t *testing.T) {
	values := []string{"one", "two"}
	states := []ui.CodeEditorState{{}, {}}
	active := 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Key(active).Fill().Children(func() {
			ui.CodeEditor(c, &values[active], &states[active], ui.CodeEditorOptions{Wrap: true}).Label("Code").Fill().AutoFocus()
		})
	}, 360, 180)
	tt.Type("!")
	active = 1
	tt.Frame()
	tt.Type("?")
	active = 0
	tt.Frame()
	tt.Command("undo")
	if values[0] != "one" || values[1] != "two?" {
		t.Fatalf("document undo leaked: %q", values)
	}
}

func TestNativeCodeHitTestingAfterHorizontalScroll(t *testing.T) {
	value := ""
	var state ui.CodeEditorState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{LineNumbers: true}).Label("Code").Fill().AutoFocus()
	}, 280, 150)
	tt.Type(strings.Repeat("abcdefghij", 30))
	state.Select(290, 290)
	tt.Frame()
	caret, ok := tt.TextCaret()
	if !ok {
		t.Fatal("no caret")
	}
	tt.ClickAt(caret.X, caret.Y+caret.H/2)
	if state.Caret != 290 {
		t.Fatalf("hit tested rune %d instead of 290 at %+v", state.Caret, caret)
	}
	tt.Compose("汉字", 2)
	caret, ok = tt.TextCaret()
	bounds, _ := tt.Find("Code")
	if !ok || caret.X < bounds.X || caret.X+caret.W > bounds.X+bounds.W+1 {
		t.Fatalf("composition caret outside view: %+v %+v", caret, bounds)
	}
}

func TestNativeEditorFindReplaceUnicode(t *testing.T) {
	a := &App{settings: engine.Object{"editorSoftWrap": true, "editorFontSize": 12}, active: "request"}
	value := "日本 alpha\n日本 beta"
	p := colors{text: ui.Hex("#222222"), muted: ui.Hex("#888888"), border: ui.Hex("#cccccc"), sidebar: ui.Hex("#ffffff"), accent: ui.Hex("#6633ff")}
	tt := ui.NewTester(func(c *ui.Context) { a.nativeEditor(c, p, &value, "Body", "json", false) }, 620, 250)
	if err := tt.Click("Body"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyF)
	if err := tt.Click("Find in Body"); err != nil {
		t.Fatal(err)
	}
	tt.Type("日本")
	doc := a.editorDocument("Body")
	if len(doc.matches) != 2 || doc.matches[1].Start != 9 {
		t.Fatalf("Unicode ranges: %+v", doc.matches)
	}
	doc.replace = true
	doc.replacement = "語"
	tt.Frame()
	if err := tt.Click("Replace All"); err != nil {
		t.Fatal(err)
	}
	if value != "語 alpha\n語 beta" {
		t.Fatalf("replace all: %q", value)
	}
	doc.state.Command("undo")
	tt.Frame()
	if value != "日本 alpha\n日本 beta" {
		t.Fatalf("replace undo: %q", value)
	}
}

func TestCodeReplacementTypingIsOneUndoStep(t *testing.T) {
	value := "日本"
	var state ui.CodeEditorState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{Wrap: true}).Fill().AutoFocus()
	}, 300, 140)
	state.Select(0, 2)
	tt.Frame()
	for _, r := range "native" {
		tt.Type(string(r))
	}
	tt.Command("undo")
	if value != "日本" {
		t.Fatalf("replacement typing split undo group: %q", value)
	}
}

func TestEditorFocusSurvivesFindAndGoToControls(t *testing.T) {
	a := &App{settings: engine.Object{"editorSoftWrap": false, "editorFontSize": 12}, active: "request"}
	value := "one\ntwo\nthree"
	p := colors{text: ui.Hex("#222222"), muted: ui.Hex("#888888"), border: ui.Hex("#cccccc"), sidebar: ui.Hex("#ffffff")}
	tt := ui.NewTester(func(c *ui.Context) { a.nativeEditor(c, p, &value, "Body", "text", false) }, 600, 250)
	if err := tt.Click("Body"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyF)
	if err := tt.Click("Close find"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("Body") {
		t.Fatal("closing find lost editor focus")
	}
	tt.Key(ui.Ctrl, ui.KeyG)
	if err := tt.Click("Line and column"); err != nil {
		t.Fatal(err)
	}
	tt.Type("2:2")
	if err := tt.Click("Go"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("Body") {
		t.Fatal("Go to Line lost editor focus")
	}
	tt.Type("!")
	if value != "one\nt!wo\nthree" {
		t.Fatalf("Go to Line edited wrong location: %q", value)
	}
}

func TestNativeFoldingPreservesDocumentAndSelection(t *testing.T) {
	value := "{\n  \"items\": [\n    1,\n    2\n  ]\n}\nend"
	original := value
	var state ui.CodeEditorState
	state.SetFolds(editorFolds(value, "json"))
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{Wrap: true, LineNumbers: true}).Label("Code").Fill().AutoFocus()
	}, 360, 240)
	state.FoldAll(true)
	tt.Frame()
	if value != original {
		t.Fatal("fold modified request payload")
	}
	state.GoTo(7, 1)
	tt.Frame()
	foldedCaret, _ := tt.TextCaret()
	if foldedCaret.Y > 70 {
		t.Fatalf("folded lines still occupy space: %+v", foldedCaret)
	}
	tt.Command("selectAll")
	tt.Command("copy")
	if tt.Clipboard() != original {
		t.Fatalf("copy omitted folded content: %q", tt.Clipboard())
	}
	state.GoTo(4, 3)
	tt.Frame()
	for _, fold := range state.Folds() {
		if fold.Collapsed {
			t.Fatalf("navigation did not reveal target: %+v", state.Folds())
		}
	}
	tt.Type("!")
	if !strings.Contains(value, "  !  2") {
		t.Fatalf("edited wrong position through fold: %q", value)
	}
}

func TestFoldParsingIgnoresStringsAndUpdatesRanges(t *testing.T) {
	source := "{\n  \"literal\": \"} ]\",\n  \"nested\": {\n    \"value\": 1\n  }\n}"
	folds := editorFolds(source, "json")
	if len(folds) != 2 {
		t.Fatalf("unexpected folds: %+v", folds)
	}
	var state ui.CodeEditorState
	state.SetFolds(folds)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &source, &state, ui.CodeEditorOptions{Wrap: true}).Fill().AutoFocus()
	}, 400, 200)
	state.ToggleFold(1)
	tt.Frame()
	state.Select(0, 0)
	state.Insert("\n")
	tt.Frame()
	state.SetFolds(editorFolds(source, "json"))
	if !state.Folds()[1].Collapsed || state.Folds()[1].Start != folds[1].Start+1 {
		t.Fatalf("fold failed to follow preceding edit: %+v", state.Folds())
	}
}

func TestCodePairsRespectStringContents(t *testing.T) {
	value := `{"value":""}`
	var state ui.CodeEditorState
	p := colors{text: ui.Hex("#222222"), orange: ui.Hex("#885500")}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{Wrap: true, CloseBrackets: true}).Syntax(syntaxSpans(value, "json", p)).Fill().AutoFocus()
	}, 320, 180)
	at := strings.Index(value, `""`) + 1
	state.Select(at, at)
	tt.Frame()
	tt.Type("[")
	if value != `{"value":"["}` {
		t.Fatalf("paired bracket inside a JSON string: %q", value)
	}
}

func TestFoldAndSyntaxOffsetsPreserveCRLF(t *testing.T) {
	source := "{\r\n  \"日本\": [\r\n    1\r\n  ]\r\n}"
	folds := editorFolds(source, "json")
	if len(folds) != 2 {
		t.Fatal(folds)
	}
	want := utf8.RuneCountInString(source[:strings.Index(source, "[")])
	if folds[1].Start != want {
		t.Fatalf("fold offsets normalized line endings: got %d want %d", folds[1].Start, want)
	}
	spans := syntaxSpans(source, "json", colors{})
	if len(spans) == 0 || spans[len(spans)-1].End < utf8.RuneCountInString(source) {
		t.Fatal("syntax ranges omitted CRLF positions")
	}
}

func TestReadOnlyCodeEditorCannotMutateResponse(t *testing.T) {
	value := "{\"id\":1}"
	var state ui.CodeEditorState
	tt := ui.NewTester(func(c *ui.Context) {
		ui.CodeEditor(c, &value, &state, ui.CodeEditorOptions{Wrap: true, ReadOnly: true, LineNumbers: true}).Label("Response").Fill().AutoFocus()
	}, 320, 180)
	tt.Command("selectAll")
	tt.Type("changed")
	tt.Key(0, ui.KeyBackspace)
	state.Replace(0, 1, "bad")
	tt.Frame()
	if value != "{\"id\":1}" {
		t.Fatal("read-only response was changed")
	}
	tt.Command("copy")
	if tt.Clipboard() != value {
		t.Fatal("read-only selection cannot be copied")
	}
}

func TestEditorSearchHandlesInvalidUTF8(t *testing.T) {
	doc := documentEditor{query: ".", regex: true}
	doc.updateMatches("a\xff")
	if len(doc.matches) != 2 || doc.matches[1].Start != 1 || doc.matches[1].End != 2 {
		t.Fatal(doc.matches)
	}
}
