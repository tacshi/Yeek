package engine

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/zalando/go-keyring"
)

func (e *Engine) Variables(ctx context.Context, workspace, folder, environment string) (map[string]string, error) {
	envs, err := e.Store.List(ctx, "environment", workspace)
	if err != nil {
		return nil, err
	}
	return resolveVariables(envs, workspace, folder, environment, func(id string) (Object, error) { return e.Store.Get(ctx, id) })
}

// VariablesFromModels resolves the same inheritance as request execution from a
// UI snapshot, without database access or evaluation of variable values.
func VariablesFromModels(models []Object, workspace, folder, environment string) (map[string]string, error) {
	index := map[string]Object{}
	envs := []Object{}
	for _, m := range models {
		index[str(m, "id")] = m
		if str(m, "model") == "environment" && str(m, "workspaceId") == workspace {
			envs = append(envs, m)
		}
	}
	return resolveVariables(envs, workspace, folder, environment, func(id string) (Object, error) {
		if m := index[id]; m != nil {
			return m, nil
		}
		return nil, fmt.Errorf("environment or folder %q does not exist", id)
	})
}

func resolveVariables(envs []Object, workspace, folder, environment string, lookup func(string) (Object, error)) (map[string]string, error) {
	// Match the store's ordering even when the UI snapshot came from a map.
	slices.SortFunc(envs, func(a, b Object) int {
		if (a["sortPriority"] == nil) != (b["sortPriority"] == nil) {
			if a["sortPriority"] == nil {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(number(a, "sortPriority"), number(b, "sortPriority")), strings.Compare(str(a, "createdAt"), str(b, "createdAt")), strings.Compare(str(a, "id"), str(b, "id")))
	})
	result := map[string]string{}
	apply := func(m Object) {
		for _, v := range objects(array(m, "variables")) {
			if enabled(v) {
				result[str(v, "name")] = str(v, "value")
			}
		}
	}
	for _, m := range envs {
		if str(m, "parentModel") == "workspace" {
			apply(m)
		}
	}
	chain := []Object{}
	seen := map[string]bool{}
	for environment != "" {
		if seen[environment] {
			return nil, errors.New("environment inheritance contains a cycle")
		}
		seen[environment] = true
		m, err := lookup(environment)
		if err != nil {
			return nil, err
		}
		if str(m, "workspaceId") != workspace || str(m, "model") != "environment" || str(m, "parentModel") == "folder" {
			return nil, errors.New("select an environment in this workspace")
		}
		chain = append(chain, m)
		if str(m, "parentModel") != "environment" {
			break
		}
		environment = str(m, "parentId")
	}
	for i := len(chain) - 1; i >= 0; i-- {
		apply(chain[i])
	}
	folders := []string{}
	clear(seen)
	for folder != "" {
		if seen[folder] {
			return nil, errors.New("folder inheritance contains a cycle")
		}
		seen[folder] = true
		folders = append(folders, folder)
		m, err := lookup(folder)
		if err != nil {
			return nil, err
		}
		if str(m, "workspaceId") != workspace || str(m, "model") != "folder" {
			return nil, errors.New("select a folder in this workspace")
		}
		folder = str(m, "folderId")
	}
	for i := len(folders) - 1; i >= 0; i-- {
		for _, m := range envs {
			if str(m, "parentModel") == "folder" && str(m, "parentId") == folders[i] {
				apply(m)
			}
		}
	}
	return result, nil
}
func (e *Engine) Render(ctx context.Context, text, workspace, folder, environment string) (string, error) {
	vars, err := e.Variables(ctx, workspace, folder, environment)
	if err != nil {
		return "", err
	}
	return renderWith(text, vars, map[string]bool{}, 0, e.functions(ctx, workspace, environment))
}
func templateFunction(name string, args map[string]string) (string, error) {
	value := args["value"]
	if value == "" {
		value = args["input"]
	}
	if value == "" {
		value = args["text"]
	}
	switch name {
	case "uuid", "uuid.v4", "uuid.v7":
		if name == "uuid.v7" {
			return uuid.NewV7().String(), nil
		}
		return uuid.NewV4().String(), nil
	case "timestamp":
		switch args["format"] {
		case "unix", "seconds":
			return strconv.FormatInt(time.Now().Unix(), 10), nil
		case "milliseconds":
			return strconv.FormatInt(time.Now().UnixMilli(), 10), nil
		default:
			return time.Now().UTC().Format(time.RFC3339), nil
		}
	case "base64", "encode.base64":
		return base64.StdEncoding.EncodeToString([]byte(value)), nil
	case "decode.base64":
		b, err := base64.StdEncoding.DecodeString(value)
		return string(b), err
	case "encode.uri", "encode.url":
		return url.QueryEscape(value), nil
	case "decode.uri", "decode.url":
		return url.QueryUnescape(value)
	case "random":
		n := 16
		if v, err := strconv.Atoi(args["length"]); err == nil {
			n = max(1, min(v, 4096))
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		return hex.EncodeToString(buf)[:n], nil
	case "json":
		var data any
		if err := json.Unmarshal([]byte(value), &data); err != nil {
			return "", err
		}
		for _, key := range strings.Split(strings.TrimPrefix(args["path"], "$."), ".") {
			if key == "" {
				continue
			}
			switch node := data.(type) {
			case map[string]any:
				data = node[key]
			case []any:
				i, err := strconv.Atoi(key)
				if err != nil || i < 0 || i >= len(node) {
					return "", errors.New("JSON path does not exist")
				}
				data = node[i]
			default:
				return "", errors.New("JSON path does not exist")
			}
		}
		if s, ok := data.(string); ok {
			return s, nil
		}
		return jsonString(data), nil
	default:
		return extendedTemplateFunction(name, args)
	}
}

func (e *Engine) functions(ctx context.Context, workspace string, environmentIDs ...string) templateRunner {
	environment := ""
	if len(environmentIDs) > 0 {
		environment = environmentIDs[0]
	}
	return func(name string, args map[string]string) (string, error) {
		if isTemplatePreview(ctx) {
			if err := e.templatePreviewAllowed(name); err != nil {
				return "", err
			}
		}
		if strings.HasPrefix(name, "request.") || strings.HasPrefix(name, "response.") || name == "cookie.value" {
			return e.referenceTemplate(ctx, name, args, workspace, environment)
		}
		switch name {
		case "secure":
			return e.DecryptValue(ctx, workspace, args["value"])
		case "keychain", "keyring":
			return keyring.Get(args["service"], args["account"])
		case "prompt.text", "prompt":
			return e.promptTemplate(ctx, workspace, args)
		case "ctx.workspace":
			return workspace, nil
		case "ctx.environment":
			return environment, nil
		case "ctx.request":
			id, _ := ctx.Value(templateRequestKey{}).(string)
			return id, nil
		case "fs", "fs.read", "fs.readFile":
			data, err := os.ReadFile(args["path"])
			if err != nil {
				return "", err
			}
			value := string(data)
			switch args["encoding"] {
			case "base64":
				value = base64.StdEncoding.EncodeToString(data)
			case "hex":
				value = hex.EncodeToString(data)
			}
			if args["trim"] == "true" {
				value = strings.TrimSpace(value)
			}
			return value, nil
		case "response":
			id := args["requestId"]
			responses, err := e.Store.Find(ctx, "http_response", "requestId", id)
			if err != nil {
				return "", err
			}
			if len(responses) == 0 {
				return "", errors.New("request has no response")
			}
			response := responses[len(responses)-1]
			switch args["attribute"] {
			case "status":
				return fmt.Sprint(response["status"]), nil
			case "header":
				for _, h := range objects(array(response, "headers")) {
					if strings.EqualFold(str(h, "name"), args["name"]) {
						return str(h, "value"), nil
					}
				}
				return "", nil
			default:
				data, err := e.Body(str(response, "id"))
				if err != nil {
					return "", err
				}
				if args["filter"] != "" {
					return FilterResponse(string(data), args["filter"])
				}
				return string(data), nil
			}
		default:
			result, err := templateFunction(name, args)
			if err != nil && strings.HasPrefix(err.Error(), "unknown template function") {
				return e.pluginTemplate(ctx, name, args, workspace)
			}
			return result, err
		}
	}
}
