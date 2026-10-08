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

	// Forever: stored under the namespace, which renders its own tags, and the label.
	remembered := `${[ prompt.text(label='OTP Code', store='forever', namespace='${[ctx.workspace()]}') ]}`
	answer = "123456"
	for range 2 {
		if got, err := e.Render(ctx, remembered, wid, "", ""); err != nil || got != "123456" {
			t.Fatal(got, err)
		}
	}
	if len(asked) != 3 {
		t.Fatalf("stored prompt asked %d times", len(asked)-2)
	}
	if _, saved, ok := e.storedPrompt(t.Context(), slugify(wid)+".otp-code"); !ok || saved.Value != "123456" {
		t.Fatal("value not stored under its key", saved)
	}

	// Expire: a value older than its TTL is asked for again.
	expiring := `${[ prompt.text(key='Session Token', store='expire', namespace='ns', ttl='0') ]}`
	answer = "a"
	if got, _ := e.Render(ctx, expiring, wid, "", ""); got != "a" {
		t.Fatal(got)
	}
	m, _, _ := e.storedPrompt(t.Context(), "ns.session-token")
	m["value"] = `{"value":"a","createdAt":1}`
	if _, err := e.Save(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	answer = "b"
	if got, _ := e.Render(ctx, expiring, wid, "", ""); got != "b" {
		t.Fatalf("expired value reused: %q", got)
	}

	if _, err := e.Render(ctx, `${[ prompt.text(store='forever', namespace='ns') ]}`, wid, "", ""); err == nil || !strings.Contains(err.Error(), "A label or key is required") {
		t.Fatal("stored prompt without a key", err)
	}
	if _, err := e.Render(ctx, `${[ prompt.text(label='x', store='forever') ]}`, wid, "", ""); err == nil || !strings.Contains(err.Error(), "Namespace is required") {
		t.Fatal("stored prompt without a namespace", err)
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
