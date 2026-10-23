package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"yeek/locales"
)

var resourceKinds = map[string]string{"workspaces": "workspace", "environments": "environment", "folders": "folder", "httpRequests": "http_request", "grpcRequests": "grpc_request", "websocketRequests": "websocket_request"}

func (e *Engine) Import(ctx context.Context, data []byte, workspace string) ([]Object, error) {
	plan, err := e.PlanImport(ctx, []ImportInput{{Kind: "text", Content: string(data)}}, ImportDestination{WorkspaceID: workspace})
	if err != nil {
		return nil, err
	}
	return e.CommitImport(ctx, plan)
}
func ParseImport(data []byte) ([]Object, error) {
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "meta {") || strings.HasPrefix(text, "meta{") || strings.HasPrefix(text, "vars {") {
		return parseBruno(text)
	}
	if strings.HasPrefix(text, "curl ") {
		requests, err := ConvertCurl(text)
		if err != nil {
			return nil, err
		}
		result := []Object{{"model": "workspace", "id": "import_workspace", "name": "Curl Import"}}
		for i, request := range requests {
			request["id"] = fmt.Sprintf("curl_request_%d", i)
			request["workspaceId"] = "import_workspace"
			result = append(result, request)
		}
		return result, nil
	}
	var root Object
	if err := json.Unmarshal(data, &root); err != nil {
		if err = yaml.Unmarshal(data, &root); err != nil {
			return nil, unsupportedImport{fmt.Errorf("%s: %w", locales.ReadCollection(), err)}
		}
	}
	if resources := obj(root, "resources"); len(resources) > 0 {
		migrateNativeImport(resources)
		result := []Object{}
		for _, collection := range slices.Sorted(maps.Keys(resourceKinds)) {
			kind := resourceKinds[collection]
			for _, m := range objects(array(resources, collection)) {
				m["model"] = kind
				result = append(result, m)
			}
		}
		if len(result) > 0 {
			return result, nil
		}
	}
	_, v5 := root["collection"].([]any)
	_, v4 := root["resources"].([]any)
	if v5 || v4 {
		models, err := importInsomnia(data)
		if err != nil {
			return nil, err
		}
		if models != nil {
			return models, nil
		}
	}
	if _, ok := root["values"]; ok && str(root, "_postman_variable_scope") != "" {
		vars := []any{}
		for _, value := range objects(array(root, "values")) {
			vars = append(vars, Object{"name": str(value, "key"), "value": str(value, "value"), "enabled": enabled(value)})
		}
		return []Object{{"model": "workspace", "id": "environment_workspace", "name": str(root, "name")}, {"model": "environment", "id": "environment_import", "workspaceId": "environment_workspace", "parentId": "environment_workspace", "parentModel": "workspace", "variables": vars, "name": "Global Variables"}}, nil
	}
	if _, ok := root["item"]; ok {
		return parsePostman(root), nil
	}
	if root["openapi"] != nil || root["swagger"] != nil {
		return parseOpenAPI(root)
	}
	if str(root, "model") != "" {
		return []Object{root}, nil
	}
	return nil, unsupportedImport{errors.New(locales.ChooseAYeekPostmanInsomniaOr())}
}
func migrateNativeImport(resources Object) {
	if requests, exists := resources["requests"]; exists && resources["httpRequests"] == nil {
		resources["httpRequests"] = requests
	}
	for _, workspace := range objects(array(resources, "workspaces")) {
		variables, exists := workspace["variables"]
		if !exists {
			continue
		}
		id := str(workspace, "id")
		for _, environment := range objects(array(resources, "environments")) {
			if str(environment, "workspaceId") == id && str(environment, "parentModel") == "" {
				environment["parentModel"], environment["parentId"] = "environment", nil
			}
		}
		resources["environments"] = append(array(resources, "environments"), Object{"model": "environment", "id": stableImportID("environment", legacyBaseEnvironmentSeed+id), "workspaceId": id, "parentModel": "workspace", "parentId": id, "name": "Global Variables", "variables": variables})
		delete(workspace, "variables")
	}
	for _, environment := range objects(array(resources, "environments")) {
		if str(environment, "parentModel") == "" {
			base, hasBase := environment["base"].(bool)
			if legacy, exists := environment["environmentId"]; exists {
				base, hasBase = legacy == nil || legacy == "", true
			}
			if hasBase {
				kind := "environment"
				if base {
					kind = "workspace"
				}
				environment["parentModel"], environment["parentId"] = kind, nil
			}
		}
		delete(environment, "base")
		delete(environment, "environmentId")
	}
}
func nilIfBlank(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func MergeModel(base, patch Object) Object {
	result := clone(base)
	maps.Copy(result, patch)
	return result
}

type unsupportedImport struct{ error }

func (e unsupportedImport) Unwrap() error { return e.error }
