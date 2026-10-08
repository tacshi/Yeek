package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestValuePromptDialogAnswersAndCancels(t *testing.T) {
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	a.testMode = true
	tt := ui.NewTester(a.View, 1100, 760)
	request := &valuePrompt{TemplatePrompt: engine.TemplatePrompt{Title: "Login", Label: "Password", Default: "x", Password: true}, value: "x", open: true, reply: make(chan valuePromptResult, 1)}
	a.valuePrompts = append(a.valuePrompts, request)
	tt.Frame()
	if !tt.HasText("Login") {
		t.Fatalf("prompt not shown: %v", tt.Texts())
	}
	tt.Type("yz")
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	if result := <-request.reply; result.err != nil || result.value != "xyz" {
		t.Fatalf("result = %+v", result)
	}
	if len(a.valuePrompts) != 0 {
		t.Fatal("prompt still queued")
	}

	cancelled := &valuePrompt{TemplatePrompt: engine.TemplatePrompt{Title: "Second", Label: "Code"}, open: true, reply: make(chan valuePromptResult, 1)}
	a.valuePrompts = append(a.valuePrompts, cancelled)
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if result := <-cancelled.reply; !errors.Is(result.err, engine.ErrPromptCancelled) {
		t.Fatalf("result = %+v", result)
	}
}
