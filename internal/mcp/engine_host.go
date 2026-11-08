package mcp

import (
	"context"
	"errors"
	"fmt"

	"yeek/internal/engine"
)

type EngineHost struct{ Engine *engine.Engine }

var modelLabels = map[string]string{"http_request": "HTTP request", "folder": "folder"}

func modelKind(m engine.Object) string {
	kind, _ := m["model"].(string)
	return kind
}

func (h EngineHost) ListHTTPRequests(workspace string) ([]engine.Object, error) {
	return h.Engine.Store.List(context.Background(), "http_request", workspace)
}

func (h EngineHost) ListFolders(workspace string) ([]engine.Object, error) {
	return h.Engine.Store.List(context.Background(), "folder", workspace)
}

func (h EngineHost) GetHTTPRequest(id string) (engine.Object, error) {
	m, err := h.Engine.Store.Get(context.Background(), id)
	if err != nil || modelKind(m) != "http_request" {
		return nil, nil
	}
	return m, nil
}

func (h EngineHost) upsert(m engine.Object, kind string) (engine.Object, error) {
	m["model"] = kind
	if id, _ := m["id"].(string); id == "" {
		delete(m, "id")
	}
	saved, err := h.Engine.Save(context.Background(), m)
	if err != nil {
		return nil, fmt.Errorf("Failed to upsert %s: %w", modelLabels[kind], err) //nolint:staticcheck
	}
	return saved, nil
}

func (h EngineHost) UpsertHTTPRequest(m engine.Object) (engine.Object, error) {
	return h.upsert(m, "http_request")
}

func (h EngineHost) UpsertFolder(m engine.Object) (engine.Object, error) {
	return h.upsert(m, "folder")
}

func (h EngineHost) delete(id, kind string) (engine.Object, error) {
	ctx := context.Background()
	m, err := h.Engine.Store.Get(ctx, id)
	if err == nil && modelKind(m) != kind {
		err = errors.New("not found")
	}
	if err == nil {
		err = h.Engine.Delete(ctx, id)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to delete %s: %w", modelLabels[kind], err) //nolint:staticcheck
	}
	return m, nil
}

func (h EngineHost) DeleteHTTPRequest(id string) (engine.Object, error) {
	return h.delete(id, "http_request")
}

func (h EngineHost) DeleteFolder(id string) (engine.Object, error) { return h.delete(id, "folder") }
