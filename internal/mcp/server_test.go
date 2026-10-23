package mcp

import (
	"context"
	"encoding/json/v2"
	"errors"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"yeek/internal/engine"
)

// fakeHost keeps models in memory, as Yaak's tests' hosts do.
type fakeHost struct {
	workspaces  []Workspace
	environment string
	models      map[string]engine.Object
	sent        []string
	toasts      []Toast
	next        int
}

func newFakeHost(workspaces ...Workspace) *fakeHost {
	return &fakeHost{workspaces: workspaces, models: map[string]engine.Object{}}
}

func (h *fakeHost) ListWorkspaces() ([]Workspace, error)       { return h.workspaces, nil }
func (h *fakeHost) ActiveEnvironmentID(string) (string, error) { return h.environment, nil }
func (h *fakeHost) ListHTTPRequests(w string) ([]engine.Object, error) {
	return h.list("http_request", w), nil
}
func (h *fakeHost) ListFolders(w string) ([]engine.Object, error) { return h.list("folder", w), nil }

func (h *fakeHost) list(kind, workspace string) []engine.Object {
	var out []engine.Object
	for _, id := range slices.Sorted(maps.Keys(h.models)) {
		if m := h.models[id]; m["model"] == kind && m["workspaceId"] == workspace {
			out = append(out, m)
		}
	}
	return out
}

func (h *fakeHost) GetHTTPRequest(id string) (engine.Object, error) {
	if m := h.models[id]; m["model"] == "http_request" {
		return m, nil
	}
	return nil, nil
}

func (h *fakeHost) upsert(m engine.Object, prefix string) (engine.Object, error) {
	m = maps.Clone(m)
	if m["id"] == "" {
		h.next++
		m["id"] = prefix + string(rune('0'+h.next))
	}
	h.models[m["id"].(string)] = m
	return m, nil
}

func (h *fakeHost) UpsertHTTPRequest(m engine.Object) (engine.Object, error) {
	return h.upsert(m, "rq_")
}
func (h *fakeHost) UpsertFolder(m engine.Object) (engine.Object, error) { return h.upsert(m, "fl_") }

func (h *fakeHost) delete(id, kind string) (engine.Object, error) {
	m := h.models[id]
	if m["model"] != kind {
		return nil, errors.New("missing")
	}
	delete(h.models, id)
	return m, nil
}

func (h *fakeHost) DeleteHTTPRequest(id string) (engine.Object, error) {
	return h.delete(id, "http_request")
}
func (h *fakeHost) DeleteFolder(id string) (engine.Object, error) { return h.delete(id, "folder") }

func (h *fakeHost) SendHTTPRequest(_ context.Context, workspace string, request engine.Object, environment string) (engine.Object, error) {
	h.sent = append(h.sent, workspace+" "+request["id"].(string)+" "+environment)
	return engine.Object{"model": "http_response", "requestId": request["id"], "status": 200, "bodyPath": "/secret/place"}, nil
}

func (h *fakeHost) ShowToast(t Toast) error { h.toasts = append(h.toasts, t); return nil }

func connect(t *testing.T, host Host) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := NewServer(host, "test").Connect(t.Context(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// call is the text a tool gives, failing the test when its error state
// is not wantError.
func call(t *testing.T, c *mcp.ClientSession, name string, args map[string]any, wantError bool) string {
	t.Helper()
	res, err := c.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError != wantError {
		t.Fatalf("%s: error %v: %s", name, res.IsError, b.String())
	}
	return b.String()
}

func decodeObject(t *testing.T, text string) engine.Object {
	t.Helper()
	var m engine.Object
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return m
}

func TestToolsActOnTheOpenWorkspace(t *testing.T) {
	host := newFakeHost(Workspace{ID: "wk_test", Name: "Seed Workspace"})
	c := connect(t, host)

	tools, err := c.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 15 {
		t.Fatalf("%d tools", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Name == "create_http_request" {
			schema, _ := json.Marshal(tool.InputSchema)
			for _, field := range []string{`"url"`, `"bodyType"`, `"headers"`, `"workspaceId"`, "Body content object"} {
				if !strings.Contains(string(schema), field) {
					t.Errorf("create_http_request schema lacks %s: %s", field, schema)
				}
			}
		}
	}

	if got := call(t, c, "list_workspaces", nil, false); got != "[\n  {\n    \"id\": \"wk_test\",\n    \"name\": \"Seed Workspace\"\n  }\n]" {
		t.Errorf("list_workspaces = %s", got)
	}
	if got := call(t, c, "get_workspace_id", nil, false); got != "wk_test" {
		t.Errorf("get_workspace_id = %s", got)
	}
	if got := call(t, c, "get_environment_id", nil, false); got != "No environment selected" {
		t.Errorf("get_environment_id = %s", got)
	}

	created := decodeObject(t, call(t, c, "create_http_request", map[string]any{"url": "https://example.com", "headers": []any{map[string]any{"name": "X-A", "value": "1"}}}, false))
	id, _ := created["id"].(string)
	if created["method"] != "GET" || created["name"] != "" || created["workspaceId"] != "wk_test" || id == "" {
		t.Fatalf("created %v", created)
	}
	if header := created["headers"].([]any)[0].(map[string]any); header["enabled"] != true {
		t.Errorf("header %v", header)
	}
	updated := decodeObject(t, call(t, c, "update_http_request", map[string]any{"id": id, "method": "POST", "headers": []any{}}, false))
	if updated["id"] != id || updated["method"] != "POST" || updated["url"] != "https://example.com" || len(updated["headers"].([]any)) != 0 {
		t.Errorf("updated %v", updated)
	}
	if got := decodeObject(t, call(t, c, "get_http_request", map[string]any{"id": id}, false)); got["method"] != "POST" {
		t.Errorf("get_http_request %v", got)
	}
	if got := call(t, c, "get_http_request", map[string]any{"id": "rq_missing"}, false); got != "null" {
		t.Errorf("missing request = %s", got)
	}
	if list := call(t, c, "list_http_requests", nil, false); !strings.Contains(list, id) {
		t.Errorf("list_http_requests = %s", list)
	}

	host.environment = "ev_active"
	response := decodeObject(t, call(t, c, "send_http_request", map[string]any{"id": id, "environmentId": "ev_chosen"}, false))
	if response["status"] != float64(200) || response["requestId"] != id || response["bodyPath"] != nil {
		t.Errorf("response %v", response)
	}
	if host.sent[0] != "wk_test "+id+" ev_chosen" {
		t.Errorf("sent %v", host.sent)
	}
	if got := call(t, c, "send_http_request", map[string]any{"id": "rq_missing"}, true); got != "HTTP request with ID rq_missing not found" {
		t.Errorf("send missing = %s", got)
	}

	folder := decodeObject(t, call(t, c, "create_folder", map[string]any{"name": "Folder", "sortPriority": 3}, false))
	folderID := folder["id"].(string)
	if folder["model"] != "folder" || folder["workspaceId"] != "wk_test" {
		t.Errorf("folder %v", folder)
	}
	if got := decodeObject(t, call(t, c, "update_folder", map[string]any{"id": folderID, "name": "Renamed"}, false)); got["name"] != "Renamed" || got["sortPriority"] != float64(3) {
		t.Errorf("updated folder %v", got)
	}
	if got := decodeObject(t, call(t, c, "get_folder", map[string]any{"id": folderID}, false)); got["name"] != "Renamed" {
		t.Errorf("get_folder %v", got)
	}
	if got := call(t, c, "update_folder", map[string]any{"id": "fl_missing"}, true); got != "Folder with ID fl_missing not found" {
		t.Errorf("update missing folder = %s", got)
	}
	if got := call(t, c, "delete_folder", map[string]any{"id": folderID}, false); got != "Deleted: Renamed ("+folderID+")" {
		t.Errorf("delete_folder = %s", got)
	}
	if got := call(t, c, "delete_http_request", map[string]any{"id": id}, false); got != "Deleted:  ("+id+")" {
		t.Errorf("delete_http_request = %s", got)
	}
	call(t, c, "delete_http_request", map[string]any{"id": id}, true)

	if got := call(t, c, "show_toast", map[string]any{"message": "Hi", "color": "success"}, false); got != "✓ Toast shown: \"Hi\"" {
		t.Errorf("show_toast = %s", got)
	}
	if host.toasts[0].Color != "success" || host.toasts[0].Timeout.Milliseconds() != 5000 {
		t.Errorf("toast %+v", host.toasts[0])
	}
	call(t, c, "show_toast", map[string]any{"message": "Hi", "color": "purple"}, true)
}

func TestToolsAskWhichWorkspaceWhenSeveralAreOpen(t *testing.T) {
	c := connect(t, newFakeHost(Workspace{ID: "wk_a", Name: "A"}, Workspace{ID: "wk_b", Name: "B"}))
	got := call(t, c, "list_http_requests", nil, true)
	if !strings.HasPrefix(got, "Multiple workspaces are open.") || !strings.Contains(got, "1. A (ID: wk_a)\n2. B (ID: wk_b)") {
		t.Errorf("%s", got)
	}
	if got := call(t, c, "list_http_requests", map[string]any{"workspaceId": "wk_b"}, false); got != "[]" {
		t.Errorf("%s", got)
	}
	got = call(t, c, "list_folders", map[string]any{"workspaceId": "wk_c"}, true)
	if got != "Workspace with ID \"wk_c\" not found.\n\nAvailable workspaces:\n- A (ID: wk_a)\n- B (ID: wk_b)" {
		t.Errorf("%s", got)
	}
	if got := call(t, connect(t, newFakeHost()), "get_workspace_id", nil, true); got != "No workspace is open" {
		t.Errorf("%s", got)
	}
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`

// Ported from Yaak's crates/yaak-mcp/tests/http.rs.
func TestHTTPRequiresTheBearerToken(t *testing.T) {
	server, err := Serve(newFakeHost(), "test", "127.0.0.1:0", "secret")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + server.Addr + Path
	post := func(url, auth string) *http.Response {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(initialize))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		return res
	}
	for _, auth := range []string{"", "Bearer wrong", "Bearer secre", "secret"} {
		res := post(url, auth)
		if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%q: %d %v", auth, res.StatusCode, res.Header)
		}
	}
	res := post(url, "Bearer secret")
	if res.StatusCode != http.StatusOK || res.Header.Get("Mcp-Session-Id") == "" {
		t.Fatalf("%d %v", res.StatusCode, res.Header)
	}
	var body strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		body.Write(buf[:n])
		if err != nil || strings.Contains(body.String(), "yeek-mcp-server") {
			break
		}
	}
	if !strings.Contains(body.String(), "yeek-mcp-server") {
		t.Errorf("%s", body.String())
	}
	if res := post("http://"+server.Addr+"/other", "Bearer secret"); res.StatusCode != http.StatusNotFound {
		t.Errorf("other path: %d", res.StatusCode)
	}

	server.Stop()
	if conn, err := net.Dial("tcp", server.Addr); err == nil {
		_ = conn.Close()
		t.Error("still listening")
	}
}
