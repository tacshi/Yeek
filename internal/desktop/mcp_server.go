package desktop

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"yeek/internal/engine"
	"yeek/internal/mcp"
)

// The MCP server's bearer token lives in key_values, as Yaak keeps it.
const (
	mcpTokenNamespace = "no_sync"
	mcpTokenKey       = "mcp_server_token" // #nosec G101 -- the name of the key_value that holds the token, not a token.
)

// windowSnapshot is what an MCP tool reads of a window: the workspace it
// shows and the environment and cookie jar a send from it uses. The window
// publishes one each frame, for tools that run off its thread.
type windowSnapshot struct {
	workspace, environment, cookieJar string
}

func (a *App) publishSnapshot() {
	a.snapshot.Store(&windowSnapshot{workspace: a.workspace, environment: a.environment, cookieJar: a.cookieJar})
}

// open reports whether the window still runs, with what it shows.
func (a *App) open() (windowSnapshot, bool) {
	snapshot := a.snapshot.Load()
	if a.ctx.Err() != nil || snapshot == nil {
		return windowSnapshot{}, false
	}
	return *snapshot, true
}

// MCPServer is Yaak's mcp_ext: Yeek's tools at mcp.DefaultAddr, served while
// the mcpServerEnabled setting is on, behind a bearer token kept in
// key_values.
type MCPServer struct {
	Engine *engine.Engine
	// Windows are the app's windows, open and closed.
	Windows func() []*App
	// Addr is where to listen; mcp.DefaultAddr when empty.
	Addr string

	mu     sync.Mutex
	server *mcp.HTTPServer
}

// Run keeps the server running while the setting is on, until ctx is done.
func (m *MCPServer) Run(ctx context.Context) {
	changes, unsubscribe := m.Engine.Subscribe()
	m.sync(ctx)
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				m.stop()
				return
			case batch := <-changes:
				if batch == nil || slices.ContainsFunc(batch, func(change engine.Object) bool { return s(o(change, "model"), "model") == "settings" }) {
					m.sync(ctx)
				}
			}
		}
	}()
}

// Running is the address the server listens on, or "".
func (m *MCPServer) Running() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server == nil {
		return ""
	}
	return m.server.Addr
}

// sync starts or stops the server to match the setting.
func (m *MCPServer) sync(ctx context.Context) {
	settings, err := m.Engine.Store.Get(ctx, "default")
	enabled := err == nil && b(settings, "mcpServerEnabled")
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case enabled && m.server == nil:
		token, err := m.token(ctx)
		var server *mcp.HTTPServer
		if err == nil {
			server, err = mcp.Serve(mcpHost{m}, appVersion(), cmp.Or(m.Addr, mcp.DefaultAddr), token)
		}
		if err != nil {
			message := fmt.Sprintf("Failed to start the MCP server on port %s: %v", portOf(cmp.Or(m.Addr, mcp.DefaultAddr)), err)
			m.toastAll(func(a *App) { a.showToast("", message, "danger", 10*time.Second) })
			return
		}
		m.server = server
	case !enabled && m.server != nil:
		m.server.Stop()
		m.server = nil
	}
}

func (m *MCPServer) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		m.server.Stop()
		m.server = nil
	}
}

// token is the server's token, made the first time it starts.
func (m *MCPServer) token(ctx context.Context) (string, error) {
	if token := mcpToken(m.list(ctx, "key_value")); token != "" {
		return token, nil
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	value, _ := json.Marshal(token)
	_, err := m.Engine.Save(ctx, engine.Object{"model": "key_value", "namespace": mcpTokenNamespace, "key": mcpTokenKey, "value": string(value)})
	return token, err
}

// mcpToken is the token among key_values, or "".
func mcpToken(keyValues []engine.Object) string {
	for _, kv := range keyValues {
		if s(kv, "namespace") == mcpTokenNamespace && s(kv, "key") == mcpTokenKey {
			var token string
			_ = json.Unmarshal([]byte(s(kv, "value")), &token)
			return token
		}
	}
	return ""
}

func (m *MCPServer) list(ctx context.Context, kind string) []engine.Object {
	models, _ := m.Engine.Store.List(ctx, kind, "")
	return models
}

// toastAll shows a toast in every open window, as Yaak's show_toast event
// reaches them all.
func (m *MCPServer) toastAll(show func(*App)) {
	for _, a := range m.Windows() {
		if _, ok := a.open(); !ok {
			continue
		}
		if a.Window == nil {
			show(a)
			continue
		}
		a.Window.Update(func() { show(a) })
	}
}

// window is the open window showing the workspace.
func (m *MCPServer) window(workspace string) (*App, windowSnapshot) {
	for _, a := range m.Windows() {
		if snapshot, ok := a.open(); ok && snapshot.workspace == workspace {
			return a, snapshot
		}
	}
	return nil, windowSnapshot{}
}

func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return addr
}

// mcpHost is the app's side of the tools: the workspaces open in its
// windows, and requests sent as the window showing them sends them.
type mcpHost struct{ m *MCPServer }

func (h mcpHost) ctx() context.Context { return context.Background() }

func (h mcpHost) ListWorkspaces() ([]mcp.Workspace, error) {
	var workspaces []mcp.Workspace
	for _, a := range h.m.Windows() {
		snapshot, ok := a.open()
		if !ok || snapshot.workspace == "" || slices.ContainsFunc(workspaces, func(w mcp.Workspace) bool { return w.ID == snapshot.workspace }) {
			continue
		}
		w, err := h.m.Engine.Store.Get(h.ctx(), snapshot.workspace)
		if err != nil {
			continue
		}
		workspaces = append(workspaces, mcp.Workspace{ID: snapshot.workspace, Name: s(w, "name")})
	}
	return workspaces, nil
}

func (h mcpHost) ActiveEnvironmentID(workspace string) (string, error) {
	_, snapshot := h.m.window(workspace)
	return snapshot.environment, nil
}

func (h mcpHost) ListHTTPRequests(workspace string) ([]engine.Object, error) {
	return h.m.Engine.Store.List(h.ctx(), "http_request", workspace)
}

func (h mcpHost) ListFolders(workspace string) ([]engine.Object, error) {
	return h.m.Engine.Store.List(h.ctx(), "folder", workspace)
}

func (h mcpHost) GetHTTPRequest(id string) (engine.Object, error) {
	m, err := h.m.Engine.Store.Get(h.ctx(), id)
	if err != nil || s(m, "model") != "http_request" {
		return nil, nil
	}
	return m, nil
}

func (h mcpHost) upsert(m engine.Object, kind string) (engine.Object, error) {
	m["model"] = kind
	if s(m, "id") == "" {
		delete(m, "id")
	}
	saved, err := h.m.Engine.Save(h.ctx(), m)
	if err != nil {
		return nil, fmt.Errorf("Failed to upsert %s: %w", map[string]string{"http_request": "HTTP request", "folder": "folder"}[kind], err) //nolint:staticcheck // Yaak's message, shown as is
	}
	return saved, nil
}

func (h mcpHost) UpsertHTTPRequest(m engine.Object) (engine.Object, error) {
	return h.upsert(m, "http_request")
}

func (h mcpHost) UpsertFolder(m engine.Object) (engine.Object, error) { return h.upsert(m, "folder") }

func (h mcpHost) delete(id, kind string) (engine.Object, error) {
	label := map[string]string{"http_request": "HTTP request", "folder": "folder"}[kind]
	m, err := h.m.Engine.Store.Get(h.ctx(), id)
	if err == nil && s(m, "model") != kind {
		err = errors.New("not found")
	}
	if err == nil {
		err = h.m.Engine.Delete(h.ctx(), id)
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to delete %s: %w", label, err) //nolint:staticcheck // Yaak's message, shown as is
	}
	return m, nil
}

func (h mcpHost) DeleteHTTPRequest(id string) (engine.Object, error) {
	return h.delete(id, "http_request")
}

func (h mcpHost) DeleteFolder(id string) (engine.Object, error) { return h.delete(id, "folder") }

func (h mcpHost) SendHTTPRequest(ctx context.Context, workspace string, request engine.Object, environment string) (engine.Object, error) {
	a, snapshot := h.m.window(workspace)
	if a == nil {
		return nil, fmt.Errorf("No window is open for workspace %s", workspace) //nolint:staticcheck // Yaak's message, shown as is
	}
	if environment == "" {
		environment = snapshot.environment
	}
	return h.m.Engine.SendHTTP(a.authorizationContext(ctx), s(request, "id"), engine.SendOptions{EnvironmentID: environment, CookieJarID: snapshot.cookieJar})
}

// ShowToast shows the toast in every window. Yeek's toasts take their icon
// from their color, so the icon asked for goes unused.
func (h mcpHost) ShowToast(t mcp.Toast) error {
	h.m.toastAll(func(a *App) { a.showToast("", t.Message, t.Color, t.Timeout) })
	return nil
}
