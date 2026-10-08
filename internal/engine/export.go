package engine

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
)

type ExportOptions struct {
	WorkspaceIDs               []string
	IncludePrivateEnvironments bool
}

func (e *Engine) Export(ctx context.Context, workspace string, private bool) ([]byte, error) {
	return e.ExportWorkspaces(ctx, ExportOptions{WorkspaceIDs: []string{workspace}, IncludePrivateEnvironments: private})
}

func (e *Engine) ExportWorkspaces(ctx context.Context, options ExportOptions) ([]byte, error) {
	if len(options.WorkspaceIDs) == 0 {
		return nil, errors.New("select a workspace to export")
	}
	all, err := e.Store.List(ctx, "", "")
	if err != nil {
		return nil, err
	}
	models := map[string]Object{}
	for _, model := range all {
		models[str(model, "id")] = model
	}
	workspaces := map[string]bool{}
	for _, id := range options.WorkspaceIDs {
		if str(models[id], "model") != "workspace" {
			return nil, fmt.Errorf("workspace %q no longer exists", id)
		}
		workspaces[id] = true
	}
	resources := Object{}
	for collection, kind := range resourceKinds {
		items := []Object{}
		for _, model := range all {
			if str(model, "model") != kind || !workspaces[str(model, "id")] && !workspaces[str(model, "workspaceId")] {
				continue
			}
			if kind == "environment" && !options.IncludePrivateEnvironments && !boolean(model, "public") {
				continue
			}
			copy := clone(model)
			if kind == "environment" && !options.IncludePrivateEnvironments {
				// Keep public descendants usable without exporting a private ancestor's variables.
				seen := map[string]bool{str(model, "id"): true}
				for parentID := str(copy, "parentId"); str(copy, "parentModel") == "environment" && parentID != ""; parentID = str(copy, "parentId") {
					parent := models[parentID]
					if seen[parentID] || parent == nil {
						return nil, fmt.Errorf("%s has an invalid environment parent", str(model, "name"))
					}
					seen[parentID] = true
					if boolean(parent, "public") {
						break
					}
					copy["parentId"] = parent["parentId"]
					if str(parent, "parentModel") != "environment" {
						copy["parentId"] = nil
						break
					}
				}
			}
			items = append(items, copy)
		}
		slices.SortFunc(items, func(a, b Object) int {
			return cmp.Or(cmp.Compare(str(a, "workspaceId"), str(b, "workspaceId")), cmp.Compare(number(a, "sortPriority"), number(b, "sortPriority")), cmp.Compare(str(a, "name"), str(b, "name")), cmp.Compare(str(a, "id"), str(b, "id")))
		})
		resources[collection] = items
	}
	return json.Marshal(Object{"yaakSchema": 5, "yeekVersion": "0.1.0", "timestamp": now(), "resources": resources}, jsontext.WithIndent("  "), json.Deterministic(true))
}

func (e *Engine) ExportToFile(ctx context.Context, path string, options ExportOptions) error {
	data, err := e.ExportWorkspaces(ctx, options)
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}
