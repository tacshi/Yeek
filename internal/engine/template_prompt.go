package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type TemplatePrompt struct {
	Title, Label, Placeholder, Default string
	Password                           bool
}

type TemplatePrompter func(context.Context, TemplatePrompt) (string, error)

type templatePrompterKey struct{}

var ErrPromptCancelled = errors.New("prompt cancelled")

func WithTemplatePrompter(ctx context.Context, prompter TemplatePrompter) context.Context {
	return context.WithValue(ctx, templatePrompterKey{}, prompter)
}

const (
	promptStoreNone    = "none"
	promptStoreExpire  = "expire"
	promptStoreForever = "forever"
)

const promptNamespace = "plugin:@yeek/template-function-prompt"

type savedPrompt struct {
	Value     string `json:"value"`
	CreatedAt int64  `json:"createdAt"`
}

var slugRemove = regexp.MustCompile(`[^\w\s$*_+~.()'"!\-:@]+`)

func slugify(s string) string {
	s = slugRemove.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
	return strings.Join(strings.Fields(s), "-")
}

func promptKey(args map[string]string) (string, error) {
	if args["key"] == "" && args["label"] == "" {
		return "", errors.New("A label or key is required when storing values") //nolint:staticcheck
	}
	var parts []string
	for _, part := range []string{args["namespace"], cmp.Or(args["key"], args["label"])} {
		if part != "" {
			parts = append(parts, slugify(part))
		}
	}
	return strings.Join(parts, "."), nil
}

func PromptKey(args map[string]string) (string, error) { return promptKey(args) }

func promptStore(args map[string]string) string {
	switch store := cmp.Or(args["store"], promptStoreNone); store {
	case "session":
		return promptStoreForever
	case "ttl":
		return promptStoreExpire
	default:
		return store
	}
}

func (e *Engine) storedPrompt(ctx context.Context, key string) (Object, savedPrompt, bool) {
	models, err := e.Store.Find(ctx, "key_value", "key", key)
	if err != nil {
		return nil, savedPrompt{}, false
	}
	for _, namespace := range []string{promptNamespace, legacyPromptNamespace} {
		for _, m := range models {
			if str(m, "namespace") != namespace {
				continue
			}
			var saved savedPrompt
			if json.Unmarshal([]byte(str(m, "value")), &saved) == nil {
				return m, saved, true
			}
		}
	}
	return nil, savedPrompt{}, false
}

func (e *Engine) maybeGetValue(ctx context.Context, args map[string]string, store, key string) (string, bool) {
	if store == promptStoreNone {
		return "", false
	}
	m, saved, ok := e.storedPrompt(ctx, key)
	if !ok {
		return "", false
	}
	if store == promptStoreForever {
		return saved.Value, true
	}
	ttl, _ := strconv.Atoi(args["ttl"])
	if age := time.Since(time.UnixMilli(saved.CreatedAt)); age.Seconds() > float64(ttl) {
		_ = e.Store.Delete(ctx, str(m, "id"), Object{"type": "background"})
		return "", false
	}
	return saved.Value, true
}

func (e *Engine) promptTemplate(ctx context.Context, args map[string]string) (string, error) {
	prompt := TemplatePrompt{
		Title:       cmp.Or(args["title"], "Enter Value"),
		Label:       cmp.Or(args["label"], "Value"),
		Placeholder: args["placeholder"],
		Default:     args["defaultValue"],
		Password:    args["password"] == "true",
	}
	store := promptStore(args)
	switch store {
	case promptStoreNone, promptStoreExpire, promptStoreForever:
	default:
		return "", errors.New("prompt.text store must be none, expire, or forever")
	}
	key := ""
	if store != promptStoreNone {
		if args["namespace"] == "" {
			return "", errors.New("Namespace is required when storing values") //nolint:staticcheck
		}
		var err error
		if key, err = promptKey(args); err != nil {
			return "", err
		}
		if value, ok := e.maybeGetValue(ctx, args, store, key); ok {
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
	if store != promptStoreNone {
		data, _ := json.Marshal(savedPrompt{Value: value, CreatedAt: time.Now().UnixMilli()})
		if _, err := e.Save(ctx, Object{"model": "key_value", "namespace": promptNamespace, "key": key, "value": string(data)}); err != nil {
			return "", err
		}
	}
	return value, nil
}
