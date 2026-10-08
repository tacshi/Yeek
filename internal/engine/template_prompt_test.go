package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPromptTemplate(t *testing.T) {
	e, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	w, err := e.Save(t.Context(), Object{"model": "workspace", "name": "Prompt"})
	if err != nil {
		t.Fatal(err)
	}
	wid := str(w, "id")
	var asked []TemplatePrompt
	answer := "first"
	ctx := WithTemplatePrompter(t.Context(), func(_ context.Context, p TemplatePrompt) (string, error) {
		asked = append(asked, p)
		return answer, nil
	})

	once := `${[ prompt.text(title='Token', label='API token', password='true') ]}`
	if got, err := e.Render(ctx, once, wid, "", ""); err != nil || got != "first" {
		t.Fatal(got, err)
	}
	if len(asked) != 1 || asked[0].Title != "Token" || asked[0].Label != "API token" || !asked[0].Password {
		t.Fatalf("prompt = %+v", asked)
	}
	answer = "second"
	if got, _ := e.Render(ctx, once, wid, "", ""); got != "second" || len(asked) != 2 {
		t.Fatalf("unstored prompt reused %q", got)
	}

	remembered := `${[ prompt.text(title='OTP', store='session') ]}`
	answer = "123456"
	for range 2 {
		if got, err := e.Render(ctx, remembered, wid, "", ""); err != nil || got != "123456" {
			t.Fatal(got, err)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("session prompt asked %d times", len(asked)-2)
	}
	e.ForgetPrompts()
	answer = "654321"
	if got, _ := e.Render(ctx, remembered, wid, "", ""); got != "654321" {
		t.Fatalf("forgotten prompt returned %q", got)
	}

	if _, err := e.Render(ctx, `${[ prompt.text(store='ttl', ttl='0') ]}`, wid, "", ""); err == nil {
		t.Fatal("invalid ttl accepted")
	}
	if got, err := e.Render(t.Context(), `${[ prompt.text(defaultValue='fallback') ]}`, wid, "", ""); err != nil || got != "fallback" {
		t.Fatal("no prompter should use the default", got, err)
	}
	if _, err := e.Render(t.Context(), `${[ prompt.text() ]}`, wid, "", ""); err == nil {
		t.Fatal("prompt without a window or default succeeded")
	}
	cancelled := WithTemplatePrompter(t.Context(), func(context.Context, TemplatePrompt) (string, error) { return "", ErrPromptCancelled })
	if _, err := e.Render(cancelled, once, wid, "", ""); !errors.Is(err, ErrPromptCancelled) {
		t.Fatal(err)
	}
	if _, err := e.PreviewTemplate(ctx, once, wid, "", "", ""); err == nil || !strings.Contains(err.Error(), "evaluated when the request is sent") {
		t.Fatal("preview evaluated prompt", err)
	}
}
