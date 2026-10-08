package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
)

type ImportInput struct{ Kind, Origin, Content, SourceWorkspaceID string }
type ImportDestination struct{ WorkspaceID, FolderID string }
type ImportWarning struct{ Title, Detail string }
type ImportItem struct {
	ID, SourceKey, SourceID, Kind, Name, ParentID, Action, Resolution string
	Selected                                                          bool
	ChangedFields                                                     []string
	Before, After                                                     Object
	expected                                                          string
}
type ImportDocument struct {
	Format, Origin, Label string
	Models                []Object
	Warnings              []ImportWarning
	SourceWorkspaceID     string
}
type ImportPlan struct {
	Destination     ImportDestination
	Items           []*ImportItem
	Warnings        []ImportWarning
	Sources         []*importPlanSource
	expectedSources map[string]string
	committed       bool
	seal            string
	mu              sync.Mutex
}
type importPlanSource struct {
	ID, Format, Origin, Label, WorkspaceID, SourceWorkspaceID string
	bindings                                                  map[string]Object
	incoming                                                  map[string]*ImportItem
}

func importable(kind string) bool {
	return slices.Contains([]string{"workspace", "folder", "environment", "http_request", "grpc_request", "websocket_request"}, kind)
}
func importHash(value any) string {
	data, _ := json.Marshal(value, json.Deterministic(true))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func importContent(model Object) Object {
	if model == nil {
		return nil
	}
	value, _ := normalizedImport(model)
	value = clone(value)
	for _, key := range []string{"id", "createdAt", "updatedAt", "_importKey"} {
		delete(value, key)
	}
	for _, key := range []string{"headers", "urlParameters", "metadata", "variables"} {
		stripImportRowIDs(array(value, key))
	}
	stripImportRowIDs(array(obj(value, "body"), "form"))
	for _, row := range objects(array(obj(value, "body"), "form")) {
		if IsFileFormField(row) {
			row["type"] = "file"
			delete(row, "value")
			delete(row, "isFile")
		}
	}
	return compactImportValue(value).(map[string]any)
}
func stripImportRowIDs(rows []any) {
	for _, row := range objects(rows) {
		delete(row, "id")
		if _, ok := row["enabled"]; !ok {
			row["enabled"] = true
		}
	}
}
func compactImportValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			child = compactImportValue(child)
			empty := child == nil
			switch c := child.(type) {
			case string:
				empty = c == ""
			case map[string]any:
				empty = len(c) == 0
			case []any:
				empty = len(c) == 0
			}
			if empty {
				delete(v, key)
			} else {
				v[key] = child
			}
		}
	case []any:
		for i := range v {
			v[i] = compactImportValue(v[i])
		}
	}
	return value
}
func importContentHash(model Object) string { return "v1:" + importHash(importContent(model)) }
func normalizedImport(model Object) (Object, error) {
	if !importable(str(model, "model")) {
		return nil, fmt.Errorf("%s cannot be imported as a collection resource", str(model, "model"))
	}
	defaults, err := defaultModel(str(model, "model"))
	if err != nil {
		return nil, err
	}
	maps.Copy(defaults, model)
	delete(defaults, "createdAt")
	delete(defaults, "updatedAt")
	delete(defaults, "_importKey")
	return defaults, nil
}
func changedImportFields(a, b Object) []string {
	a, b = importContent(a), importContent(b)
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	result := []string{}
	for k := range keys {
		if !reflect.DeepEqual(a[k], b[k]) {
			result = append(result, k)
		}
	}
	slices.Sort(result)
	return result
}
func importSourceKey(model Object) string { return str(model, "model") + ":" + str(model, "id") }

// PlanImport reads and normalizes input without changing application data.
func (e *Engine) PlanImport(ctx context.Context, inputs []ImportInput, destination ImportDestination) (*ImportPlan, error) {
	documents := []ImportDocument{}
	for _, input := range inputs {
		document, err := e.ReadImport(ctx, input, destination.WorkspaceID)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return e.planImportDocuments(ctx, documents, destination)
}
func (e *Engine) planImportDocuments(ctx context.Context, documents []ImportDocument, destination ImportDestination) (*ImportPlan, error) {
	if len(documents) == 0 {
		return nil, errors.New("choose a collection source")
	}
	if err := e.validateImportDestination(ctx, destination); err != nil {
		return nil, err
	}
	plan := &ImportPlan{Destination: destination, expectedSources: map[string]string{}}
	all, err := e.Store.List(ctx, "", "")
	if err != nil {
		return nil, err
	}
	current := map[string]Object{}
	for _, model := range all {
		current[str(model, "id")] = model
	}
	for _, document := range documents {
		for _, prior := range plan.Sources {
			if document.Origin != "" && prior.Origin == document.Origin && (document.SourceWorkspaceID == "" || document.SourceWorkspaceID == prior.SourceWorkspaceID) {
				return nil, errors.New("the same import source was selected twice")
			}
		}
		plan.Warnings = append(plan.Warnings, document.Warnings...)
		originals := map[string]Object{}
		workspaceIDs := []string{}
		for i, model := range document.Models {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, err := json.Marshal(model); err != nil {
				return nil, fmt.Errorf("collection resource is not valid JSON: %w", err)
			}
			if !importable(str(model, "model")) {
				return nil, fmt.Errorf("unsupported imported resource %q", str(model, "model"))
			}
			if str(model, "id") == "" {
				model = clone(model)
				model["id"] = fmt.Sprintf("source_%d", i)
				document.Models[i] = model
			}
			id := str(model, "id")
			if originals[id] != nil {
				return nil, fmt.Errorf("collection has duplicate resource ID %q", id)
			}
			originals[id] = model
			if str(model, "model") == "workspace" {
				workspaceIDs = append(workspaceIDs, id)
			}
		}
		if len(workspaceIDs) == 0 {
			root := "source_workspace"
			for originals[root] != nil {
				root += "_"
			}
			model := Object{"model": "workspace", "id": root, "name": document.Label}
			document.Models = append([]Object{model}, document.Models...)
			originals[root] = model
			workspaceIDs = append(workspaceIDs, root)
		}
		sources := map[string]*importPlanSource{}
		ids := map[string]string{}
		for _, old := range workspaceIDs {
			wid := destination.WorkspaceID
			if wid == "" {
				wid = newID("workspace")
			}
			ids[old] = wid
			source := &importPlanSource{ID: newID("import_source"), Format: document.Format, Origin: document.Origin, Label: document.Label, WorkspaceID: wid, SourceWorkspaceID: old, bindings: map[string]Object{}, incoming: map[string]*ImportItem{}}
			if destination.WorkspaceID != "" && document.Origin != "" {
				for _, candidate := range all {
					if str(candidate, "model") == "import_source" && str(candidate, "workspaceId") == wid && str(candidate, "origin") == document.Origin && str(candidate, "importer") == document.Format && str(candidate, "sourceWorkspaceId") == old {
						source.ID = str(candidate, "id")
						plan.expectedSources[source.ID] = importHash(candidate)
						break
					}
				}
				for _, binding := range all {
					if str(binding, "model") == "import_source_resource" && str(binding, "importSourceId") == source.ID {
						source.bindings[str(binding, "sourceKey")] = binding
						plan.expectedSources[str(binding, "id")] = importHash(binding)
					}
				}
			}
			plan.Sources = append(plan.Sources, source)
			sources[old] = source

		}
		for _, model := range document.Models {
			old := str(model, "id")
			if str(model, "model") == "workspace" {
				continue
			}
			wid := str(model, "workspaceId")
			if wid == "" {
				wid = workspaceIDs[0]
			}
			source := sources[wid]
			if source == nil {
				return nil, fmt.Errorf("resource %q has no source workspace", str(model, "name"))
			}
			key := importSourceKey(model)
			binding := source.bindings[key]
			id := str(binding, "modelId")
			if id == "" {
				id = newID(str(model, "model"))
			}
			ids[old] = id
		}
		for _, original := range document.Models {
			kind := str(original, "model")
			wid := str(original, "workspaceId")
			if kind == "workspace" {
				wid = str(original, "id")
			}
			if wid == "" {
				wid = workspaceIDs[0]
			}
			source := sources[wid]
			key := importSourceKey(original)
			binding := source.bindings[key]
			model := rewriteTemplateReferences(original, ids).(map[string]any)
			model["id"] = ids[str(original, "id")]
			if kind != "workspace" {
				model["workspaceId"] = source.WorkspaceID
			}
			for _, key := range []string{"folderId", "parentId"} {
				if parent := str(model, key); parent != "" {
					if mapped, ok := ids[parent]; ok {
						model[key] = mapped
					} else {
						return nil, fmt.Errorf("resource %q references missing parent %q", str(model, "name"), parent)
					}
				}
			}
			if destination.FolderID != "" && kind != "environment" && kind != "workspace" && str(model, "folderId") == "" {
				model["folderId"] = destination.FolderID
			}
			if kind == "environment" && str(model, "parentModel") == "workspace" && destination.WorkspaceID != "" && !boolean(binding, "baseEnvironment") {
				model["parentModel"] = "environment"
				model["parentId"] = nil
				model["name"] = str(model, "name") + " (Imported)"
				plan.Warnings = append(plan.Warnings, ImportWarning{Title: "Base environment kept separate", Detail: str(model, "name")})
			}
			model, err = normalizedImport(model)
			if err != nil {
				return nil, err
			}
			before := current[str(model, "id")]
			if before != nil && ((kind != "workspace" && str(before, "workspaceId") != source.WorkspaceID) || str(before, "model") != kind) {
				return nil, errors.New("linked import resource belongs to another workspace or type")
			}
			item := &ImportItem{ID: str(model, "id"), SourceID: source.ID, SourceKey: key, Kind: kind, Name: str(model, "name"), ParentID: importItemParent(model), After: model, Before: clone(before), Selected: true, Action: "create", expected: importHash(before)}
			if binding != nil && str(binding, "modelId") == "" {
				item.Action = "ignored"
				item.Selected = false
			}
			if before != nil {
				incomingHash, currentHash := importContentHash(model), importContentHash(before)
				baseline := str(binding, "contentHash")
				item.ChangedFields = changedImportFields(before, model)
				switch {
				case incomingHash == currentHash:
					item.Action = "unchanged"
					item.Selected = false
				case baseline == incomingHash:
					item.Action = "keep_local"
					item.Selected = false
				case baseline == currentHash:
					item.Action = "update"
				default:
					item.Action = "conflict"
					item.Resolution = "keep_mine"
				}
			} else if binding != nil && str(binding, "modelId") != "" {
				item.Action = "ignored"
				item.Selected = false
			}
			source.incoming[key] = item
			plan.Items = append(plan.Items, item)
		}
	}
	for _, source := range plan.Sources {
		for key, binding := range source.bindings {
			if source.incoming[key] != nil {
				continue
			}
			model := current[str(binding, "modelId")]
			if model == nil {
				continue
			}
			plan.Items = append(plan.Items, &ImportItem{ID: str(model, "id"), SourceID: source.ID, SourceKey: key, Kind: str(model, "model"), Name: str(model, "name"), ParentID: importItemParent(model), Action: "delete", Before: clone(model), expected: importHash(model)})
		}
	}
	// Preview local descendants too; a folder deletion must never hide its cascade.
	deleting := map[string]bool{}
	known := map[string]bool{}
	for _, item := range plan.Items {
		known[item.ID] = true
		if item.Action == "delete" {
			deleting[item.ID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, model := range all {
			if !importable(str(model, "model")) || known[str(model, "id")] || !deleting[importParent(model)] {
				continue
			}
			item := &ImportItem{ID: str(model, "id"), Kind: str(model, "model"), Name: str(model, "name"), ParentID: importItemParent(model), Action: "delete", Before: clone(model), expected: importHash(model)}
			plan.Items = append(plan.Items, item)
			known[item.ID] = true
			deleting[item.ID] = true
			changed = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, item := range plan.Items {
			if item.SourceID != "" {
				continue
			}
			for _, parent := range plan.Items {
				if parent.ID == item.ParentID && parent.SourceID != "" {
					item.SourceID = parent.SourceID
					changed = true
					break
				}
			}
		}
	}
	if err = validateImportParents(plan, current); err != nil {
		return nil, err
	}
	slices.SortStableFunc(plan.Items, func(a, b *ImportItem) int {
		if a.Kind == "folder" && b.Kind != "folder" {
			return -1
		}
		if b.Kind == "folder" && a.Kind != "folder" {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	plan.seal = plan.signature()
	return plan, nil
}

func importParent(model Object) string {
	if str(model, "model") == "environment" {
		return str(model, "parentId")
	}
	return str(model, "folderId")
}
func importItemParent(model Object) string {
	if parent := importParent(model); parent != "" {
		return parent
	}
	return str(model, "workspaceId")
}
func (i *ImportItem) Key() string { return i.SourceID + ":" + i.SourceKey + ":" + i.ID }
func (p *ImportPlan) NewWorkspaces() []Object {
	result := []Object{}
	for _, item := range p.Items {
		if item.Kind == "workspace" && item.Action == "create" && item.Selected {
			result = append(result, clone(item.After))
		}
	}
	return result
}

// Only checkbox and conflict-resolution choices may change after previewing.
func (p *ImportPlan) signature() string {
	items := make([]ImportItem, len(p.Items))
	for i, item := range p.Items {
		if item == nil {
			return ""
		}
		items[i] = *item
		items[i].Selected = false
		items[i].Resolution = ""
	}
	return importHash([]any{p.Destination, items, p.Sources})
}

func (e *Engine) validateImportDestination(ctx context.Context, d ImportDestination) error {
	if d.WorkspaceID == "" {
		if d.FolderID != "" {
			return errors.New("a folder destination needs a workspace")
		}
		return nil
	}
	w, err := e.Store.Get(ctx, d.WorkspaceID)
	if err != nil || str(w, "model") != "workspace" {
		return errors.New("destination workspace no longer exists")
	}
	if d.FolderID != "" {
		folder, err := e.Store.Get(ctx, d.FolderID)
		if err != nil || str(folder, "model") != "folder" || str(folder, "workspaceId") != d.WorkspaceID {
			return errors.New("destination folder must belong to the selected workspace")
		}
	}
	return nil
}
func validateImportParents(plan *ImportPlan, current map[string]Object) error {
	models := maps.Clone(current)
	for _, item := range plan.Items {
		if item.After != nil {
			models[item.ID] = item.After
		}
	}
	check := []Object{}
	for _, item := range plan.Items {
		if item.After != nil && item.Kind != "workspace" {
			check = append(check, item.After)
		}
	}
	return validateImportModels(models, check)
}
func validateImportModels(models map[string]Object, check []Object) error {
	for _, model := range check {
		workspace := str(model, "workspaceId")
		if str(models[workspace], "model") != "workspace" {
			return fmt.Errorf("%s has no destination workspace", str(model, "name"))
		}
		seen := map[string]bool{str(model, "id"): true}
		for child := model; importParent(child) != ""; {
			id := importParent(child)
			parent := models[id]
			expected := "folder"
			if str(child, "model") == "environment" {
				expected = str(child, "parentModel")
			}
			if parent == nil || str(parent, "model") != expected || (expected == "workspace" && id != workspace) || (expected != "workspace" && str(parent, "workspaceId") != workspace) {
				return fmt.Errorf("%s has an invalid or unselected parent", str(model, "name"))
			}
			if seen[id] {
				return errors.New("collection has circular parent references")
			}
			seen[id] = true
			child = parent
		}
		if str(model, "model") == "environment" {
			parentKind := str(model, "parentModel")
			if !slices.Contains([]string{"workspace", "environment", "folder"}, parentKind) || (parentKind == "folder" && importParent(model) == "") {
				return fmt.Errorf("%s has an invalid environment parent", str(model, "name"))
			}
		}
	}
	return nil
}

func itemApplies(item *ImportItem) bool {
	if item.Action == "unchanged" {
		return false
	}
	if item.Action == "conflict" {
		return item.Selected && item.Resolution == "take_source"
	}
	return item.Selected
}

// CommitImport applies exactly the previewed resources in one transaction.
// Changed destination data invalidates the preview instead of being overwritten.
func (e *Engine) CommitImport(ctx context.Context, plan *ImportPlan) ([]Object, error) {
	if plan == nil {
		return nil, errors.New("prepare a fresh import preview")
	}
	plan.mu.Lock()
	defer plan.mu.Unlock()
	if plan.committed || plan.seal == "" || plan.seal != plan.signature() {
		return nil, errors.New("prepare a fresh import preview")
	}
	output := []Object{}
	err := e.Store.Write(ctx, Object{"type": "import"}, func(tx *modelTx) error {
		all, err := tx.all(ctx)
		if err != nil {
			return err
		}
		current := map[string]Object{}
		for _, model := range all {
			current[str(model, "id")] = model
		}
		if d := plan.Destination; d.WorkspaceID != "" {
			w := current[d.WorkspaceID]
			if str(w, "model") != "workspace" {
				return errors.New("destination workspace was deleted")
			}
			if d.FolderID != "" {
				f := current[d.FolderID]
				if str(f, "model") != "folder" || str(f, "workspaceId") != d.WorkspaceID {
					return errors.New("destination folder was deleted or moved")
				}
			}
		}
		for id, expected := range plan.expectedSources {
			if importHash(current[id]) != expected {
				return errors.New("import source changed; refresh the preview")
			}
		}
		for _, item := range plan.Items {
			if importHash(current[item.ID]) != item.expected {
				return fmt.Errorf("%s changed; refresh the import preview", item.Name)
			}
		}
		selected := map[string]*ImportItem{}
		for _, item := range plan.Items {
			if itemApplies(item) {
				if selected[item.ID] != nil {
					return fmt.Errorf("multiple sources change %s; select one source for those settings", item.Name)
				}
				selected[item.ID] = item
			}
		}
		final := maps.Clone(current)
		for _, item := range selected {
			if item.Action == "delete" {
				delete(final, item.ID)
			} else {
				final[item.ID] = item.After
			}
		}
		check := []Object{}
		targetWorkspaces := map[string]bool{}
		for _, source := range plan.Sources {
			targetWorkspaces[source.WorkspaceID] = true
		}
		for _, model := range final {
			if targetWorkspaces[str(model, "workspaceId")] && importable(str(model, "model")) && str(model, "model") != "workspace" {
				check = append(check, model)
			}
		}
		if err = validateImportModels(final, check); err != nil {
			return err
		}
		for _, source := range plan.Sources {
			if source.Origin == "" {
				continue
			}
			for _, model := range all {
				if str(model, "model") == "import_source" && str(model, "workspaceId") == source.WorkspaceID && str(model, "origin") == source.Origin && str(model, "importer") == source.Format && str(model, "sourceWorkspaceId") == source.SourceWorkspaceID && str(model, "id") != source.ID {
					return errors.New("source was imported elsewhere; refresh the preview")
				}
			}
		}
		pending := []*ImportItem{}
		for _, item := range plan.Items {
			if itemApplies(item) && item.Action != "delete" {
				pending = append(pending, item)
			}
		}
		for len(pending) > 0 {
			remaining := []*ImportItem{}
			for _, item := range pending {
				ready := true
				for _, key := range []string{"workspaceId", "folderId", "parentId"} {
					if id := str(item.After, key); id != "" && current[id] == nil {
						ready = false
					}
				}
				if !ready {
					remaining = append(remaining, item)
					continue
				}
				patch := clone(item.After)
				for key := range current[item.ID] {
					if _, present := patch[key]; !present && key != "createdAt" && key != "updatedAt" {
						patch[key] = nil
					}
				}
				saved, err := tx.upsert(ctx, patch)
				if err != nil {
					return err
				}
				current[item.ID] = saved
				output = append(output, saved)
			}
			if len(remaining) == len(pending) {
				return errors.New("collection has missing or circular parents")
			}
			pending = remaining
		}
		for _, item := range plan.Items {
			if item.Action == "delete" && itemApplies(item) && current[item.ID] != nil {
				ancestorDeletes := false
				for id := importParent(current[item.ID]); id != ""; id = importParent(current[id]) {
					if parent := selected[id]; parent != nil && parent.Action == "delete" {
						ancestorDeletes = true
						break
					}
				}
				if ancestorDeletes {
					continue
				}
				if err := tx.delete(ctx, item.ID); err != nil {
					return err
				}
			}
		}
		for _, source := range plan.Sources {
			if source.Origin == "" || current[source.WorkspaceID] == nil {
				continue
			}
			model, err := tx.upsert(ctx, Object{"model": "import_source", "id": source.ID, "workspaceId": source.WorkspaceID, "importer": source.Format, "origin": source.Origin, "originLabel": source.Label, "sourceWorkspaceId": source.SourceWorkspaceID, "destinationFolderId": nilIfBlank(plan.Destination.FolderID), "lastImportedAt": now()})
			if err != nil {
				return err
			}
			output = append(output, model)
			for key, item := range source.incoming {
				binding := source.bindings[key]
				id := str(binding, "id")
				if id == "" {
					id = newID("import_source_resource")
				}
				modelID := item.ID
				if item.Action == "create" || item.Action == "ignored" {
					if !itemApplies(item) {
						modelID = ""
					}
				}
				_, err = tx.upsert(ctx, Object{"model": "import_source_resource", "id": id, "workspaceId": source.WorkspaceID, "importSourceId": source.ID, "sourceKey": key, "modelType": item.Kind, "modelId": nilIfBlank(modelID), "contentHash": importContentHash(item.After), "baseEnvironment": str(item.After, "parentModel") == "workspace"})
				if err != nil {
					return err
				}
			}
			for _, item := range plan.Items {
				if item.SourceID == source.ID && item.Action == "delete" && itemApplies(item) {
					if binding := source.bindings[item.SourceKey]; binding != nil {
						if err = tx.delete(ctx, str(binding, "id")); err != nil {
							return err
						}
					}
				}
			}
		}
		for _, source := range plan.Sources {
			if current[source.WorkspaceID] == nil {
				continue
			}
			if err = tx.ensureWorkspace(ctx, source.WorkspaceID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	plan.committed = true
	return output, nil
}
