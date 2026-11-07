package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yeek/internal/engine"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestCommandName(t *testing.T) {
	for app, want := range map[string]string{"Yeek": "yeek", "Yeek Debug": "yeek-debug"} {
		if got := CommandName(app); got != want {
			t.Errorf("CommandName(%q) = %q, want %q", app, got, want)
		}
	}
}

func TestInvoked(t *testing.T) {
	exe, _ := os.Executable()
	self := filepath.Base(exe)
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{self}, false},
		{[]string{self, "mcp"}, true},
		{[]string{self, "--data-dir", "/tmp/x", "mcp"}, true},
		{[]string{self, "--data-dir=/tmp/x", "mcp", "--environment", "ev"}, true},
		{[]string{self, "workspace.yeek"}, false},
		{[]string{self, "--version"}, true},
		{[]string{self, "--data-dir", "mcp"}, false},
		{[]string{"/usr/local/bin/yeek"}, true},
		{[]string{"/usr/local/bin/yeek", "--help"}, true},
	} {
		if got := Invoked(tc.args, "yeek"); got != tc.want {
			t.Errorf("Invoked(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

func TestMainHelpVersionAndUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	o := Options{Name: "yeek", Version: "1.2.3", Out: &out, Err: &errOut}
	if code := Main([]string{"--version"}, o); code != 0 || out.String() != "yeek 1.2.3\n" {
		t.Fatalf("version: %d %q", code, out.String())
	}
	out.Reset()
	if code := Main(nil, o); code != 0 || !strings.Contains(out.String(), "mcp ") {
		t.Fatalf("help: %d %q", code, out.String())
	}
	out.Reset()
	if code := Main([]string{"--help"}, o); code != 0 || !strings.Contains(out.String(), "Usage: yeek") {
		t.Fatalf("--help: %d %q", code, out.String())
	}
	if code := Main([]string{"nope"}, o); code != 1 || !strings.Contains(errOut.String(), `unknown command "nope"`) {
		t.Fatalf("unknown: %d %q", code, errOut.String())
	}
}

type session struct {
	t       *testing.T
	client  *sdk.ClientSession
	done    chan int
	stdin   io.WriteCloser
	stderr  *lockedBuffer
	stopped bool
}

func startMCP(t *testing.T, dir string, args ...string) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	stderr := &lockedBuffer{}
	done := make(chan int, 1)
	go func() {
		code := Main(append([]string{"--data-dir", dir, "mcp"}, args...), Options{Name: "yeek", Version: "test", In: inR, Out: outW, Err: stderr})
		_ = outW.Close()
		done <- code
	}()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1.0"}, nil).Connect(t.Context(), &sdk.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	s := &session{t: t, client: client, done: done, stdin: inW, stderr: stderr}
	t.Cleanup(s.stop)
	return s
}

func (s *session) stop() {
	if s.stopped {
		return
	}
	s.stopped = true
	_ = s.client.Close()
	_ = s.stdin.Close()
	if code := <-s.done; code != 0 {
		s.t.Errorf("mcp exited %d: %s", code, s.stderr.String())
	}
}

func (s *session) call(name string, args map[string]any) (string, bool) {
	s.t.Helper()
	res, err := s.client.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.t.Fatal(err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	return text.String(), res.IsError
}

func (s *session) ok(name string, args map[string]any) string {
	s.t.Helper()
	text, isErr := s.call(name, args)
	if isErr {
		s.t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

func (s *session) json(name string, args map[string]any) map[string]any {
	s.t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s.ok(name, args)), &v); err != nil {
		s.t.Fatal(err)
	}
	return v
}

type seeded struct {
	workspace, request, base, a, b string
	markers                        chan string
}

func seed(t *testing.T, dir string) seeded {
	t.Helper()
	markers := make(chan string, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		markers <- r.Header.Get("X-Environment")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	e, err := engine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	save := func(m engine.Object) string {
		t.Helper()
		saved, err := e.Save(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		return saved["id"].(string)
	}
	vars := func(v string) []any {
		return []any{engine.Object{"name": "marker", "value": v, "enabled": true}}
	}
	workspace := save(engine.Object{"model": "workspace", "name": "Test"})
	envs, err := e.Store.List(t.Context(), "environment", workspace)
	if err != nil || len(envs) == 0 {
		t.Fatal("no base environment", err)
	}
	base := envs[0]
	base["variables"] = vars("global")
	save(base)
	sub := func(name string) string {
		return save(engine.Object{"model": "environment", "workspaceId": workspace, "parentModel": "environment", "parentId": base["id"], "name": name, "variables": vars(name)})
	}
	return seeded{
		workspace: workspace,
		base:      base["id"].(string),
		a:         sub("a"),
		b:         sub("b"),
		request: save(engine.Object{"model": "http_request", "workspaceId": workspace, "name": "Echo", "method": "GET", "url": upstream.URL,
			"headers": []any{engine.Object{"name": "X-Environment", "value": "${[ marker ]}", "enabled": true}}}),
		markers: markers,
	}
}

func TestMCPServesEveryWorkspaceOverStdio(t *testing.T) {
	dir := t.TempDir()
	fx := seed(t, dir)
	s := startMCP(t, dir, "--environment", fx.a)

	tools, err := s.client.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 15 {
		t.Fatalf("tools: %v %d", err, len(tools.Tools))
	}
	if got := s.ok("get_workspace_id", nil); got != fx.workspace {
		t.Fatalf("workspace %q", got)
	}
	if got := s.ok("get_environment_id", nil); got != fx.a {
		t.Fatalf("environment %q", got)
	}

	if r := s.json("send_http_request", map[string]any{"id": fx.request}); r["status"] != float64(200) {
		t.Fatalf("send: %v", r)
	} else if _, ok := r["bodyPath"]; ok {
		t.Fatalf("bodyPath leaked: %v", r)
	}
	if got := <-fx.markers; got != "a" {
		t.Fatalf("active environment sent %q", got)
	}
	s.ok("send_http_request", map[string]any{"id": fx.request, "workspaceId": fx.workspace, "environmentId": fx.b})
	if got := <-fx.markers; got != "b" {
		t.Fatalf("given environment sent %q", got)
	}
	if text, isErr := s.call("send_http_request", map[string]any{"id": fx.request, "environmentId": "ev_missing"}); !isErr || text != "Environment with ID ev_missing not found" {
		t.Fatalf("missing environment: %v %q", isErr, text)
	}
	if text, isErr := s.call("send_http_request", map[string]any{"id": "rq_missing"}); !isErr || text != "HTTP request with ID rq_missing not found" {
		t.Fatalf("missing request: %v %q", isErr, text)
	}

	created := s.json("create_http_request", map[string]any{"name": "Created", "url": "https://example.com/items"})
	id, _ := created["id"].(string)
	if !strings.HasPrefix(id, "rq_") || created["workspaceId"] != fx.workspace {
		t.Fatalf("created %v", created)
	}
	if got := s.ok("delete_http_request", map[string]any{"id": id}); got != "Deleted: Created ("+id+")" {
		t.Fatalf("delete %q", got)
	}

	if got := s.ok("show_toast", map[string]any{"message": "Hello"}); got != `✓ Toast shown: "Hello"` {
		t.Fatalf("toast %q", got)
	}
	s.stop()
	if !strings.Contains(s.stderr.String(), "[mcp] Hello") {
		t.Fatalf("stderr %q", s.stderr.String())
	}

	e, err := engine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close() }()
	responses, _ := e.Store.List(t.Context(), "http_response", fx.workspace)
	if len(responses) != 2 {
		t.Fatalf("responses %d", len(responses))
	}
}

func TestMCPWithoutAnEnvironmentUsesTheBase(t *testing.T) {
	dir := t.TempDir()
	fx := seed(t, dir)
	s := startMCP(t, dir)
	if got := s.ok("get_environment_id", nil); got != "No environment selected" {
		t.Fatalf("environment %q", got)
	}
	s.ok("send_http_request", map[string]any{"id": fx.request})
	if got := <-fx.markers; got != "global" {
		t.Fatalf("sent %q", got)
	}
}

func TestMCPAsksWhichWorkspaceWhenThereAreMany(t *testing.T) {
	dir := t.TempDir()
	fx := seed(t, dir)
	e, err := engine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Save(t.Context(), engine.Object{"model": "workspace", "name": "Other"}); err != nil {
		t.Fatal(err)
	}
	_ = e.Close()
	s := startMCP(t, dir)
	if text, isErr := s.call("list_http_requests", nil); !isErr || !strings.Contains(text, "Test (ID: "+fx.workspace+")") {
		t.Fatalf("%v %q", isErr, text)
	}
	if text := s.ok("list_workspaces", nil); !strings.Contains(text, `"Other"`) || !strings.Contains(text, `"Test"`) {
		t.Fatalf("workspaces %q", text)
	}
}
