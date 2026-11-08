package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"yeek/internal/engine"
	"yeek/internal/mcp"
)

type Host struct {
	Engine        *engine.Engine
	EnvironmentID string
	CookieJarID   string
	Log           io.Writer
}

var _ mcp.Host = (*Host)(nil)

func str(m engine.Object, key string) string {
	v, _ := m[key].(string)
	return v
}

func (h *Host) store() mcp.EngineHost { return mcp.EngineHost{Engine: h.Engine} }

func (h *Host) ListWorkspaces() ([]mcp.Workspace, error) {
	models, err := h.Engine.Store.List(context.Background(), "workspace", "")
	if err != nil {
		return nil, err
	}
	workspaces := make([]mcp.Workspace, 0, len(models))
	for _, w := range models {
		workspaces = append(workspaces, mcp.Workspace{ID: str(w, "id"), Name: str(w, "name")})
	}
	return workspaces, nil
}

func (h *Host) environment(workspace, id string) (string, error) {
	m, err := h.Engine.Store.Get(context.Background(), id)
	if err != nil || str(m, "model") != "environment" || str(m, "workspaceId") != workspace {
		return "", fmt.Errorf("Environment with ID %s not found", id) //nolint:staticcheck
	}
	return id, nil
}

func (h *Host) ActiveEnvironmentID(workspace string) (string, error) {
	if h.EnvironmentID == "" {
		return "", nil
	}
	id, err := h.environment(workspace, h.EnvironmentID)
	if err != nil {
		return "", nil
	}
	return id, nil
}

func (h *Host) ListHTTPRequests(workspace string) ([]engine.Object, error) {
	return h.store().ListHTTPRequests(workspace)
}

func (h *Host) GetHTTPRequest(id string) (engine.Object, error) { return h.store().GetHTTPRequest(id) }

func (h *Host) UpsertHTTPRequest(m engine.Object) (engine.Object, error) {
	return h.store().UpsertHTTPRequest(m)
}

func (h *Host) DeleteHTTPRequest(id string) (engine.Object, error) {
	return h.store().DeleteHTTPRequest(id)
}

func (h *Host) ListFolders(workspace string) ([]engine.Object, error) {
	return h.store().ListFolders(workspace)
}

func (h *Host) UpsertFolder(m engine.Object) (engine.Object, error) { return h.store().UpsertFolder(m) }

func (h *Host) DeleteFolder(id string) (engine.Object, error) { return h.store().DeleteFolder(id) }

func (h *Host) SendHTTPRequest(ctx context.Context, workspace string, request engine.Object, environment string) (engine.Object, error) {
	owner := str(request, "workspaceId")
	var err error
	if environment != "" {
		if owner != workspace {
			return nil, errors.New("HTTP request does not belong to the selected workspace") //nolint:staticcheck
		}
		if environment, err = h.environment(owner, environment); err != nil {
			return nil, err
		}
	} else if environment, err = h.ActiveEnvironmentID(owner); err != nil {
		return nil, err
	}
	jar := h.CookieJarID
	if jar == "" {
		jars, err := h.Engine.Store.List(ctx, "cookie_jar", owner)
		if err != nil {
			return nil, fmt.Errorf("Failed to list cookie jars: %w", err) //nolint:staticcheck
		}
		oldest := ""
		for _, j := range jars {
			if jar == "" || str(j, "createdAt") < oldest {
				jar, oldest = str(j, "id"), str(j, "createdAt")
			}
		}
	}
	return h.Engine.SendHTTP(ctx, str(request, "id"), engine.SendOptions{EnvironmentID: environment, CookieJarID: jar})
}

func (h *Host) ShowToast(t mcp.Toast) error {
	if h.Log != nil {
		_, _ = fmt.Fprintf(h.Log, "[mcp] %s\n", t.Message)
	}
	return nil
}
