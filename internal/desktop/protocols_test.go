package desktop

import (
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/egoist/mygo/yeekui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"yeek/internal/engine"
)

func TestGRPCRequestLikeYaak(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	a, e, _ := treeApp(t)
	saved, err := e.Save(t.Context(), engine.Object{"model": "grpc_request", "workspaceId": a.workspace, "url": "http://" + listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(saved)
	id := s(saved, "id")
	a.openRequest(id)
	tt := ui.NewTester(a.View, 1360, 860)
	tt.Frame()
	d := a.drafts[id]
	// The schema is reflected without asking, and its first method chosen.
	if d.Service == "" || d.RPCMethod == "" || !tt.HasText("Schema Detected") || tt.HasText("Params") || !tt.HasText("Metadata") {
		t.Fatal(d.Service, d.RPCMethod, tt.Texts())
	}
	if err := tt.Click("gRPC method"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(tt.Menu(), "Health/Check") {
		t.Fatal(tt.Menu())
	}
	if err := tt.ChooseMenuItem("Health/Check"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if requestName(engine.Object{"model": "grpc_request", "url": "x", "service": d.Service, "method": d.RPCMethod}) != "Health/Check" || !tt.HasText("Send") {
		t.Fatal(d.Service, d.RPCMethod)
	}
	// The message completes the method's input fields, and checks them.
	if err := tt.Click("Message body"); err != nil {
		t.Fatal(err)
	}
	tt.Type("{")
	tt.Key(ui.Ctrl, ui.KeySpace)
	tt.Frame()
	if !tt.HasText("Complete service") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Complete service"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(d.Message, `{"service": `) {
		t.Fatalf("%q", d.Message)
	}
	d.Message = `{"nope": 1}`
	tt.Frame()
	if !tt.HasText(`1:2  Property "nope" is not expected`) {
		t.Fatal(tt.Texts())
	}
	d.Message = "{}"
	a.save(d)
	conn, err := e.StartGRPC(t.Context(), id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m, _ := e.Store.Get(t.Context(), s(conn, "id")); s(m, "state") == "closed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	all, _ := e.Store.List(t.Context(), "", a.workspace)
	for _, m := range all {
		a.applyModel(m)
	}
	tt.Frame()
	tt.Frame()
	// A unary call shows the message received.
	if !tt.HasText("Messages") || !tt.HasText("Message Received") || !strings.Contains(strings.Join(tt.Texts(), " "), "SERVING") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Close event panel"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("Message Received") {
		t.Fatal("panel stayed open")
	}
	if err := tt.Click("Show connection history"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Contains(menu, "Clear Connection") || !slices.Contains(menu, "History") {
		t.Fatal(menu)
	}
}

func TestNewRequestNamesAndCreateMenu(t *testing.T) {
	for m, want := range map[string]engine.Object{
		"gRPC Request":      {"model": "grpc_request", "url": ""},
		"WebSocket Request": {"model": "websocket_request", "url": ""},
		"GraphQL Request":   {"model": "http_request", "url": "", "bodyType": "graphql"},
		"HTTP Request":      {"model": "http_request", "url": ""},
		"api.dev/users":     {"model": "http_request", "url": "https://api.dev/users"},
		"BASE/users":        {"model": "http_request", "url": "${[ BASE ]}/users"},
		"Folder":            {"model": "folder", "name": "Folder"},
	} {
		if got := requestName(want); got != m {
			t.Errorf("requestName(%v) = %q, want %q", want, got, m)
		}
	}
	a, _, _ := treeApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Add Resource"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Equal(menu, []string{"HTTP", "GraphQL", "gRPC", "WebSocket", "-", "Folder"}) {
		t.Fatal(menu)
	}
}

func TestWebSocketRequestLikeYaak(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err = conn.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	a, e, _ := treeApp(t)
	saved, err := e.Save(t.Context(), engine.Object{"model": "websocket_request", "workspaceId": a.workspace, "url": "ws" + strings.TrimPrefix(server.URL, "http")})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(saved)
	id := s(saved, "id")
	a.openRequest(id)
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("Connect") || tt.HasText("Send Message") || !tt.HasText("Params") {
		t.Fatal(tt.Texts())
	}
	conn, err := e.ConnectWebSocket(t.Context(), id, engine.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.SendWebSocket(t.Context(), s(conn, "id"), `{"hi":1}`, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, _ := e.Store.Find(t.Context(), "websocket_event", "connectionId", s(conn, "id"))
		if slices.ContainsFunc(events, func(m engine.Object) bool { return b(m, "isServer") && s(m, "messageType") == "text" }) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	all, _ := e.Store.List(t.Context(), "", a.workspace)
	for _, m := range all {
		a.applyModel(m)
	}
	a.running[id] = true
	tt.Frame()
	if !tt.HasText("CONNECTED") || !tt.HasText("Send Message") || !tt.HasText("Close connection") || !tt.HasText("Connected to server") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click(`{"hi":1}`); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Message Sent") && !tt.HasText("Message Received") || !tt.HasText("Show Hexdump") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Show Hexdump"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Show Message") {
		t.Fatal(tt.Texts())
	}
	_ = e.CloseWebSocket(s(conn, "id"))
}
