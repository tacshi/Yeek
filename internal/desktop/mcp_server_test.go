package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/yeekui"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yeek/internal/engine"
	"yeek/internal/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAgentsSettingsServeTheOpenWorkspace(t *testing.T) {
	a, e := cookieApp(t)
	received := make(chan http.Header, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		_, _ = w.Write([]byte(r.Header.Get("X-Environment")))
	}))
	defer upstream.Close()
	save := func(m engine.Object) engine.Object {
		t.Helper()
		m["workspaceId"] = a.workspace
		saved, err := e.Save(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		a.applyModel(saved)
		return saved
	}
	variables := func(value string) []any {
		return []any{engine.Object{"name": "marker", "value": value, "enabled": true}}
	}
	active := save(engine.Object{"model": "environment", "parentModel": "environment", "name": "Active", "variables": variables("active")})
	chosen := save(engine.Object{"model": "environment", "parentModel": "environment", "name": "Chosen", "variables": variables("chosen")})
	request := save(engine.Object{"model": "http_request", "name": "Marker", "url": upstream.URL, "headers": []any{engine.Object{"name": "X-Environment", "value": "${[ marker ]}", "enabled": true}}})
	a.environment = s(active, "id")

	server := &MCPServer{Engine: e, Windows: func() []*App { return []*App{a} }, Addr: "127.0.0.1:0"}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	server.Run(ctx)

	a.prompt("settings", "Settings", "", "")
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Agents"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Turn on the MCP server above to connect this way.") || tt.HasText("Token") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("MCP server enabled"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the server", func() bool { return server.Running() != "" })
	all, _ := e.Store.List(t.Context(), "key_value", "")
	token := mcpToken(all)
	if len(token) != 64 {
		t.Fatalf("token %q", token)
	}
	for _, m := range all {
		a.applyModel(m)
	}
	tt.Frame()
	want := `claude mcp add --transport http yeek http://127.0.0.1:64343/mcp --header "Authorization: Bearer ` + token + `"`
	if !tt.HasText(token) || !tt.HasText(want) {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Cursor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.agentClient() != "cursor" || !tt.HasText(`"Authorization": "Bearer `+token+`"`) {
		t.Fatal(a.agentClient(), tt.Texts())
	}

	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0"}, nil).Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint:   "http://" + server.Running() + mcp.Path,
		HTTPClient: &http.Client{Transport: bearer{token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	text := func(name string, args map[string]any) string {
		t.Helper()
		res, err := client.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		return res.Content[0].(*sdk.TextContent).Text
	}
	var workspaces []map[string]string
	_ = json.Unmarshal([]byte(text("list_workspaces", nil)), &workspaces)
	if len(workspaces) != 1 || workspaces[0]["id"] != a.workspace || workspaces[0]["name"] != "Cookie tests" {
		t.Fatalf("%v", workspaces)
	}
	if got := text("get_environment_id", nil); got != s(active, "id") {
		t.Errorf("environment %s", got)
	}
	for env, marker := range map[string]string{"": "active", s(chosen, "id"): "chosen"} {
		args := map[string]any{"id": s(request, "id")}
		if env != "" {
			args["environmentId"] = env
		}
		var response map[string]any
		_ = json.Unmarshal([]byte(text("send_http_request", args)), &response)
		if response["status"] != float64(200) || response["requestId"] != s(request, "id") || response["bodyPath"] != nil {
			t.Fatalf("%v", response)
		}
		if got := (<-received).Get("X-Environment"); got != marker {
			t.Errorf("sent with %q, want %q", got, marker)
		}
	}

	var created map[string]any
	_ = json.Unmarshal([]byte(text("create_http_request", map[string]any{"url": "https://example.com/new", "name": "From agent"})), &created)
	if m, err := e.Store.Get(t.Context(), created["id"].(string)); err != nil || s(m, "workspaceId") != a.workspace || s(m, "name") != "From agent" {
		t.Fatalf("%v %v", m, err)
	}

	a.saveSetting("mcpServerEnabled", false)
	waitFor(t, "the server to stop", func() bool { return server.Running() == "" })
	if !strings.HasPrefix(appAgentSetup("other", "u", "t").snippet, "{\n  \"mcpServers\"") {
		t.Error(appAgentSetup("other", "u", "t").snippet)
	}
}

func TestAppAgentSetup(t *testing.T) {
	url, token := "http://127.0.0.1:64343/mcp", "abc" // #nosec G101 -- a placeholder token for the snippets.
	for client, want := range map[string]agentSetup{
		"claude_code": {"Run in a terminal.", `claude mcp add --transport http yeek http://127.0.0.1:64343/mcp --header "Authorization: Bearer abc"`},
		"codex":       {"Add to ~/.codex/config.toml.", "[mcp_servers.yeek]\nurl = \"http://127.0.0.1:64343/mcp\"\nhttp_headers = { \"Authorization\" = \"Bearer abc\" }"},
		"cursor":      {"Add to ~/.cursor/mcp.json.", "{\n  \"mcpServers\": {\n    \"yeek\": {\n      \"headers\": {\n        \"Authorization\": \"Bearer abc\"\n      },\n      \"url\": \"http://127.0.0.1:64343/mcp\"\n    }\n  }\n}"},
		"vscode":      {"Add to your user mcp.json (MCP: Open User Configuration).", "{\n  \"servers\": {\n    \"yeek\": {\n      \"type\": \"http\",\n      \"url\": \"http://127.0.0.1:64343/mcp\",\n      \"headers\": {\n        \"Authorization\": \"Bearer abc\"\n      }\n    }\n  }\n}"},
		"other":       {"For clients that read an mcpServers config, through the mcp-remote bridge.", "{\n  \"mcpServers\": {\n    \"yeek\": {\n      \"command\": \"npx\",\n      \"args\": [\"-y\", \"mcp-remote\", \"http://127.0.0.1:64343/mcp\", \"--header\", \"Authorization: Bearer abc\"]\n    }\n  }\n}"},
	} {
		if got := appAgentSetup(client, url, token); got != want {
			t.Errorf("%s:\n%s\nwant\n%s", client, got.snippet, want.snippet)
		}
	}
}
