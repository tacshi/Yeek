package mcp

import (
	"context"
	"time"

	"yeek/internal/engine"
)

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Toast struct {
	Message, Icon, Color string
	Timeout              time.Duration
}

type Host interface {
	ListWorkspaces() ([]Workspace, error)
	ActiveEnvironmentID(workspaceID string) (string, error)

	ListHTTPRequests(workspaceID string) ([]engine.Object, error)
	GetHTTPRequest(id string) (engine.Object, error)
	UpsertHTTPRequest(request engine.Object) (engine.Object, error)
	DeleteHTTPRequest(id string) (engine.Object, error)

	ListFolders(workspaceID string) ([]engine.Object, error)
	UpsertFolder(folder engine.Object) (engine.Object, error)
	DeleteFolder(id string) (engine.Object, error)

	SendHTTPRequest(ctx context.Context, workspaceID string, request engine.Object, environmentID string) (engine.Object, error)

	ShowToast(Toast) error
}
