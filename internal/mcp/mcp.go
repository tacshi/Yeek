// Package mcp is Yeek's MCP server, Yaak's yaak-mcp crate: the tools,
// written once against Host, and the streamable HTTP transport the app
// serves them on.
package mcp

import (
	"context"
	"time"

	"yeek/internal/engine"
)

// Workspace is a workspace a tool may act on.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Toast is a notification for the app to show.
type Toast struct {
	Message, Icon, Color string
	Timeout              time.Duration
}

// Host is what the tools need from the process hosting them. Errors are
// shown to the MCP client as they are.
type Host interface {
	// ListWorkspaces is the workspaces open in the app's windows.
	ListWorkspaces() ([]Workspace, error)
	// ActiveEnvironmentID is the environment a send uses when the caller
	// names none, or "".
	ActiveEnvironmentID(workspaceID string) (string, error)

	ListHTTPRequests(workspaceID string) ([]engine.Object, error)
	// GetHTTPRequest is nil when there is no such request.
	GetHTTPRequest(id string) (engine.Object, error)
	UpsertHTTPRequest(request engine.Object) (engine.Object, error)
	DeleteHTTPRequest(id string) (engine.Object, error)

	ListFolders(workspaceID string) ([]engine.Object, error)
	UpsertFolder(folder engine.Object) (engine.Object, error)
	DeleteFolder(id string) (engine.Object, error)

	// SendHTTPRequest sends request from workspaceID, environmentID, when
	// not "", overriding the active environment for this send only.
	SendHTTPRequest(ctx context.Context, workspaceID string, request engine.Object, environmentID string) (engine.Object, error)

	ShowToast(Toast) error
}
