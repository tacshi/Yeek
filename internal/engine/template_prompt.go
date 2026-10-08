package engine

import (
	"cmp"
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// TemplatePrompt asks the user for a value while a request is rendered.
type TemplatePrompt struct {
	Title, Label, Placeholder, Default string
	Password                           bool
}

// TemplatePrompter returns the entered value, or an error when the prompt is cancelled.
type TemplatePrompter func(context.Context, TemplatePrompt) (string, error)

type templatePrompterKey struct{}

// ErrPromptCancelled is returned when the user dismisses a prompt.text dialog.
var ErrPromptCancelled = errors.New("prompt cancelled")

func WithTemplatePrompter(ctx context.Context, prompter TemplatePrompter) context.Context {
	return context.WithValue(ctx, templatePrompterKey{}, prompter)
}

type promptEntry struct {
	value   string
	expires time.Time
}

// promptCache keeps remembered answers in memory only, so entered secrets never reach disk.
type promptCache struct {
	mu      sync.Mutex
	entries map[string]promptEntry
}

func (c *promptCache) get(key string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return "", false
	}
	if !entry.expires.IsZero() && !now.Before(entry.expires) {
		delete(c.entries, key)
		return "", false
	}
	return entry.value, true
}

func (c *promptCache) set(key, value string, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]promptEntry{}
	}
	c.entries[key] = promptEntry{value, expires}
}

// ForgetPrompts clears remembered prompt.text answers.
func (e *Engine) ForgetPrompts() {
	e.prompts.mu.Lock()
	defer e.prompts.mu.Unlock()
	clear(e.prompts.entries)
}

func (e *Engine) promptTemplate(ctx context.Context, workspace string, args map[string]string) (string, error) {
	prompt := TemplatePrompt{
		Title:       cmp.Or(args["title"], "Enter Value"),
		Label:       cmp.Or(args["label"], "Value"),
		Placeholder: args["placeholder"],
		Default:     args["defaultValue"],
		Password:    args["password"] == "true",
	}
	store := cmp.Or(args["store"], "none")
	key := workspace + "\x00" + cmp.Or(args["key"], prompt.Title+"\x00"+prompt.Label)
	var ttl time.Duration
	switch store {
	case "none", "session":
	case "ttl":
		seconds, err := strconv.Atoi(args["ttl"])
		if err != nil || seconds <= 0 {
			return "", errors.New("prompt.text cache duration must be a positive number of seconds")
		}
		ttl = time.Duration(seconds) * time.Second
	default:
		return "", errors.New("prompt.text store must be none, session, or ttl")
	}
	now := time.Now()
	if store != "none" {
		if value, ok := e.prompts.get(key, now); ok {
			return value, nil
		}
	}
	prompter, _ := ctx.Value(templatePrompterKey{}).(TemplatePrompter)
	if prompter == nil {
		if prompt.Default != "" {
			return prompt.Default, nil
		}
		return "", errors.New("prompt.text needs an open Yeek window")
	}
	value, err := prompter(ctx, prompt)
	if err != nil {
		return "", err
	}
	switch store {
	case "session":
		e.prompts.set(key, value, time.Time{})
	case "ttl":
		e.prompts.set(key, value, time.Now().Add(ttl))
	}
	return value, nil
}
