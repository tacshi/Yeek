package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	onepassword "github.com/1password/onepassword-sdk-go"
)

// onePasswordCache is the 1Password clients, and a minute of their
// answers, as Yaak's 1Password plugin keeps them to avoid rate limits.
type onePasswordCache struct {
	mu      sync.Mutex
	clients map[string]*onepassword.Client
	entries map[string]onePasswordEntry
}

type onePasswordEntry struct {
	data    any
	expires time.Time
}

const onePasswordTTL = time.Minute

func (c *onePasswordCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.data, true
}

func (c *onePasswordCache) set(key string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]onePasswordEntry{}
	}
	c.entries[key] = onePasswordEntry{data, time.Now().Add(onePasswordTTL)}
}

var onePassword onePasswordCache

// onePasswordClient is Yaak's op(): a client for a service account token,
// or for an account of the desktop app.
func onePasswordClient(ctx context.Context, values map[string]string) (*onepassword.Client, string, error) {
	var option onepassword.ClientOption
	var source string
	if strings.Contains(values["token"], "${[") {
		values = maps.Clone(values)
		values["token"] = "" // a variable that is not set
	}
	switch values["authMethod"] {
	case "desktop":
		if values["token"] == "" {
			return nil, "", errors.New("Missing account name") //nolint:staticcheck // Yaak's wording, shown as is.
		}
		source, option = "desktop:"+values["token"], onepassword.WithDesktopAppIntegration(values["token"])
	case "token", "":
		if values["token"] == "" {
			return nil, "", errors.New("Missing service token") //nolint:staticcheck // Yaak's wording, shown as is.
		}
		source, option = "token:"+values["token"], onepassword.WithServiceAccountToken(values["token"])
	default:
		return nil, "", errors.New("Invalid authentication method") //nolint:staticcheck // Yaak's wording, shown as is.
	}
	sum := sha256.Sum256([]byte(source))
	hash := hex.EncodeToString(sum[:])
	onePassword.mu.Lock()
	client := onePassword.clients[hash]
	onePassword.mu.Unlock()
	if client != nil {
		return client, hash, nil
	}
	client, err := onepassword.NewClient(ctx, option, onepassword.WithIntegrationInfo("Yeek 1Password", "v1.0.0"))
	if err != nil {
		return nil, "", err
	}
	onePassword.mu.Lock()
	defer onePassword.mu.Unlock()
	if onePassword.clients == nil {
		onePassword.clients = map[string]*onepassword.Client{}
	}
	onePassword.clients[hash] = client
	return client, hash, nil
}

func cached[T any](key string, load func() (T, error)) (T, error) {
	if data, ok := onePassword.get(key); ok {
		if value, ok := data.(T); ok {
			return value, nil
		}
	}
	value, err := load()
	if err == nil {
		onePassword.set(key, value)
	}
	return value, err
}

// onePasswordOptions lists the vaults, the vault's items or the item's
// fields, for the form of 1password.item.
func onePasswordOptions(ctx context.Context, field string, values map[string]string) (options, labels []string, err error) {
	client, hash, err := onePasswordClient(ctx, values)
	if err != nil {
		return nil, nil, err
	}
	vault, item := values["vault"], values["item"]
	switch field {
	case "vault":
		vaults, err := cached(hash+":vaults", func() ([]onepassword.VaultOverview, error) { return client.Vaults().List(ctx) })
		for _, v := range vaults {
			options, labels = append(options, v.ID), append(labels, fmt.Sprintf("%s (%d Items)", v.Title, v.ActiveItemCount))
		}
		return options, labels, err
	case "item":
		if vault == "" {
			return nil, nil, errors.New("no vault")
		}
		items, err := cached(hash+":items:"+vault, func() ([]onepassword.ItemOverview, error) { return client.Items().List(ctx, vault) })
		for _, i := range items {
			options, labels = append(options, i.ID), append(labels, fmt.Sprintf("%s %s", i.Title, i.Category))
		}
		return options, labels, err
	case "field":
		if vault == "" || item == "" {
			return nil, nil, errors.New("no item")
		}
		got, err := cached(hash+":item:"+vault+":"+item, func() (onepassword.Item, error) { return client.Items().Get(ctx, vault, item) })
		for _, f := range got.Fields {
			options, labels = append(options, f.ID), append(labels, f.Title)
		}
		return options, labels, err
	}
	return nil, nil, fmt.Errorf("1password.item has no field %q", field)
}

// onePasswordValue is Yaak's getValue: the field's secret.
func onePasswordValue(ctx context.Context, values map[string]string) (string, error) {
	client, hash, err := onePasswordClient(ctx, values)
	if err != nil {
		return "", err
	}
	vault, item, field := values["vault"], values["item"], values["field"]
	switch {
	case vault == "":
		return "", errors.New("No vault specified") //nolint:staticcheck // Yaak's wording, shown as is.
	case item == "":
		return "", errors.New("No item specified") //nolint:staticcheck // Yaak's wording, shown as is.
	case field == "":
		return "", errors.New("No field specified") //nolint:staticcheck // Yaak's wording, shown as is.
	}
	return cached(hash+":item:"+vault+":"+item+":"+field, func() (string, error) {
		return client.Secrets().Resolve(ctx, "op://"+vault+"/"+item+"/"+field)
	})
}

// onePasswordDefinition is the 1password.item of Yaak's 1Password plugin.
func onePasswordDefinition() TemplateDefinition {
	dynamic := func(name string) func(context.Context, map[string]string) ([]string, []string, error) {
		return func(ctx context.Context, values map[string]string) ([]string, []string, error) {
			return onePasswordOptions(ctx, name, values)
		}
	}
	return TemplateDefinition{
		Name:        "1password.item",
		Description: "Get a secret",
		Preview:     true,
		Fields: []TemplateField{
			{Name: "authMethod", Label: "Authentication Method", Kind: "select", Default: "token", Options: []string{"token", "desktop"}, OptionLabels: []string{"Service Account", "Desktop App"}},
			{Name: "token", Label: "Token", Kind: "text", Default: "${[1PASSWORD_TOKEN]}", Describe: func(v map[string]string) (string, string, bool, bool) {
				switch v["authMethod"] {
				case "desktop":
					return "Account Name", `Account name can be taken from the sidebar of the 1Password App. Make sure you're on the BETA version of the 1Password app and have "Integrate with other apps" enabled in Settings > Developer.`, false, true
				case "token", "":
					return "Token", "Token can be generated from the 1Password website by visiting Developer > Service Accounts", true, true
				}
				return "", "", false, false
			}},
			{Name: "vault", Label: "Vault", Kind: "select", Dynamic: dynamic("vault")},
			{Name: "item", Label: "Item", Kind: "select", Dynamic: dynamic("item")},
			{Name: "field", Label: "Field", Kind: "select", Dynamic: dynamic("field")},
		},
	}
}
