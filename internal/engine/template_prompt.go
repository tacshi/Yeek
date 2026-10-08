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

// How prompt.text stores what was entered, as Yaak's prompt plugin does.
const (
	promptStoreNone    = "none"
	promptStoreExpire  = "expire"
	promptStoreForever = "forever"
)

// promptNamespace is where prompt.text keeps stored values, as Yaak's
// plugin store keeps them for its prompt plugin.
const promptNamespace = "plugin:@yaak/template-function-prompt"

type savedPrompt struct {
	Value     string `json:"value"`
	CreatedAt int64  `json:"createdAt"`
}

var slugRemove = regexp.MustCompile(`[^\w\s$*_+~.()'"!\-:@]+`)

// slugify is the npm slugify package as Yaak calls it: lower case and
// trimmed, with runs of spaces as dashes.
func slugify(s string) string {
	s = slugRemove.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
	return strings.Join(strings.Fields(s), "-")
}

// promptKey is Yaak's buildKey: the namespace and the key, else the label.
func promptKey(args map[string]string) (string, error) {
	if args["key"] == "" && args["label"] == "" {
		return "", errors.New("A label or key is required when storing values") //nolint:staticcheck // Yaak's wording, shown as is.
	}
	var parts []string
	for _, part := range []string{args["namespace"], cmp.Or(args["key"], args["label"])} {
		if part != "" {
			parts = append(parts, slugify(part))
		}
	}
	return strings.Join(parts, "."), nil
}

// PromptKey is where prompt.text stores a value, for its form's banner.
func PromptKey(args map[string]string) (string, error) { return promptKey(args) }

func promptStore(args map[string]string) string {
	switch store := cmp.Or(args["store"], promptStoreNone); store {
	case "session": // Yeek's earlier names
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
	for _, m := range models {
		if str(m, "namespace") != promptNamespace {
			continue
		}
		var saved savedPrompt
		if json.Unmarshal([]byte(str(m, "value")), &saved) == nil {
			return m, saved, true
		}
	}
	return nil, savedPrompt{}, false
}

// maybeGetValue is Yaak's: a stored value, unless it expired.
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
			return "", errors.New("Namespace is required when storing values") //nolint:staticcheck // Yaak's wording, shown as is.
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
