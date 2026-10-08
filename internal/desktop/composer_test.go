package desktop

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func composerApp(t *testing.T, bodyType string, body engine.Object) (*App, *Draft, *ui.Tester) {
	t.Helper()
	a, e := cookieApp(t)
	model, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Composer", "url": "http://127.0.0.1:1", "bodyType": bodyType, "body": body})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(model)
	a.openRequest(s(model, "id"))
	d := a.drafts[a.active]
	d.Tab = 0
	return a, d, ui.NewTester(a.View, 1360, 860)
}

func TestMultipartFileModeAndMetadataSurviveEditing(t *testing.T) {
	a, d, tt := composerApp(t, "multipart/form-data", engine.Object{"form": []any{engine.Object{"name": "upload", "type": "file", "file": "", "filename": "custom.bin", "contentType": "application/octet-stream", "extra": "preserved"}}})
	if !d.Form[0].FileMode || !tt.HasText("File") {
		t.Fatal("empty imported file rendered as text")
	}
	if err := tt.Click("File path 1"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() { tt.Type("/tmp/complete-upload.txt"); tt.Key(ui.Cmd, ui.KeyS) })
	saved, err := a.Engine.Store.Get(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	row := oslice(o(saved, "body"), "form")[0]
	if !engine.IsFileFormField(row) || s(row, "file") != "/tmp/complete-upload.txt" || s(row, "filename") != "custom.bin" || s(row, "extra") != "preserved" {
		t.Fatal(row)
	}
	if err = tt.Click("Actions for Name 1"); err != nil {
		t.Fatal(err)
	}
	if err = tt.ChooseMenuItem("Field Options…"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("Multipart filename"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("renamed.txt")
	if err = tt.Click("Multipart Content-Type"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Batch(func() { tt.Type("text/plain"); tt.Key(ui.Cmd, ui.KeyEnter) })
	if a.dialogOpen || a.running[d.ID] {
		t.Fatal("save field sent a request or did not close", a.partEditor.error)
	}
	saved, _ = a.Engine.Store.Get(t.Context(), d.ID)
	row = oslice(o(saved, "body"), "form")[0]
	if s(row, "filename") != "renamed.txt" || s(row, "contentType") != "text/plain" {
		t.Fatal(row)
	}
	d = newDraft(saved)
	if !d.Form[0].FileMode || d.Form[0].File != "/tmp/complete-upload.txt" {
		t.Fatal(d.Form)
	}
}

func TestMultipartTextOptionsAndModeSwitch(t *testing.T) {
	a, d, tt := composerApp(t, "multipart/form-data", engine.Object{"form": []any{engine.Object{"name": "data", "value": "before"}}})
	if err := tt.Click("Actions for Name 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Field Options…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Multipart value"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("line one\nline two")
	if err := tt.Click("Save Field"); err != nil {
		t.Fatal(err)
	}
	if d.Form[0].Value != "line one\nline two" {
		t.Fatal(d.Form)
	}
	if err := tt.Click("Field type 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("File"); err != nil {
		t.Fatal(err)
	}
	if !d.Form[0].FileMode {
		t.Fatal("file selection not applied")
	}
	a.saveActive()
	saved, _ := a.Engine.Store.Get(t.Context(), d.ID)
	if !engine.IsFileFormField(oslice(o(saved, "body"), "form")[0]) {
		t.Fatal("empty file kind was not persisted")
	}
	if err := tt.Click("Field type 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Text"); err != nil {
		t.Fatal(err)
	}
	if d.Form[0].FileMode || d.Form[0].File != "" || d.Form[0].Value != "line one\nline two" {
		t.Fatal(d.Form)
	}
}

func TestCustomHTTPMethodAndValidation(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	if err := tt.Click("HTTP method"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Custom…"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Custom HTTP method"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("BAD METHOD")
	if err := tt.Click("Save Method"); err != nil {
		t.Fatal(err)
	}
	if !a.dialogOpen || a.methodError == "" {
		t.Fatal("invalid method accepted")
	}
	if err := tt.Click("Custom HTTP method"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Batch(func() { tt.Type("PROPFIND"); tt.Key(ui.Cmd, ui.KeyEnter) })
	if a.dialogOpen || d.Method != "PROPFIND" || a.running[d.ID] {
		t.Fatal(d.Method, a.methodError)
	}
	saved, err := a.Engine.Store.Get(t.Context(), d.ID)
	if err != nil || s(saved, "method") != "PROPFIND" {
		t.Fatal(saved, err)
	}
}

func TestBinaryPathAndContentTypeSuggestion(t *testing.T) {
	a, d, tt := composerApp(t, "binary", engine.Object{})
	if err := tt.Click("Request body file"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() { tt.Type("/tmp/request.json"); tt.Key(ui.Cmd, ui.KeyS) })
	saved, _ := a.Engine.Store.Get(t.Context(), d.ID)
	if s(o(saved, "body"), "filePath") != "/tmp/request.json" {
		t.Fatal(saved)
	}
	if !tt.HasText("Set Content-Type to application/json?") {
		t.Fatal("missing MIME suggestion")
	}
	if err := tt.Click("Ignore"); err != nil {
		t.Fatal(err)
	}
	if !a.binaryMIMEIgnored(d) {
		t.Fatal("ignore not saved")
	}
	reopened, err := New(a.Engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.cancel)
	if !reopened.binaryMIMEIgnored(newDraft(saved)) {
		t.Fatal("ignore did not persist")
	}
	if err = tt.Click("Request body file"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("/tmp/other.json")
	if err = tt.Click("Set Header"); err != nil {
		t.Fatal(err)
	}
	saved, _ = a.Engine.Store.Get(t.Context(), d.ID)
	if headers := oslice(saved, "headers"); len(headers) != 1 || s(headers[0], "value") != "application/json" || !b(headers[0], "enabled") {
		t.Fatal(headers)
	}
	if err = tt.Click("Clear body file"); err != nil {
		t.Fatal(err)
	}
	if d.FilePath != "" {
		t.Fatal("body file was not cleared")
	}
}

func TestBodyTypeConversionPreservesDataAndHeaders(t *testing.T) {
	a, d, tt := composerApp(t, "application/x-www-form-urlencoded", engine.Object{"form": []any{engine.Object{"name": "tag", "value": "one"}, engine.Object{"name": "tag", "value": "two"}}})
	if err := tt.Click("Url Encoded"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("JSON"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("JSON") || tt.HasText("Url Encoded") {
		t.Fatal("body tab label did not follow the body type", tt.Texts())
	}
	var value engine.Object
	if json.Unmarshal([]byte(d.Body), &value) != nil || len(value["tag"].([]any)) != 2 {
		t.Fatal(d.Body)
	}
	if d.Headers[0].Name != "Content-Type" || d.Headers[0].Value != "application/json" {
		t.Fatal(d.Headers)
	}
	d.Body = `{"query":"query Example { field }","variables":{"id":3},"operationName":"Example"}`
	a.changeBodyType(d, "graphql")
	if d.Method != "POST" || d.Query != "query Example { field }" || d.OperationName != "Example" || !strings.Contains(d.Variables, `"id": 3`) {
		t.Fatal(d)
	}
	a.changeBodyType(d, "application/json")
	if !strings.Contains(d.Body, `"query": "query Example { field }"`) {
		t.Fatal(d.Body)
	}
	a.changeBodyType(d, "")
	if len(o(d.object(), "body")) != 0 || len(d.Headers) != 0 {
		t.Fatal(d.object())
	}
}

func TestBodyFormattingPreservesUndo(t *testing.T) {
	_, d, tt := composerApp(t, "application/json", engine.Object{"text": `{"name":"before"}`})
	if err := tt.Click("Format"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.Contains(d.Body, "\n") {
		t.Fatal("body not formatted", d.Body)
	}
	if err := tt.Click("Request body"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if d.Body != `{"name":"before"}` {
		t.Fatal("format did not preserve undo", d.Body)
	}
}
