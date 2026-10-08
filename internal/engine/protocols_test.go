package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	testpb "google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/reflection"
)

func TestWebSocketMessagesAndClose(t *testing.T) {
	e := testEngine(t)
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
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "websocket_request", "workspaceId": str(w, "id"), "url": "ws" + strings.TrimPrefix(server.URL, "http")})
	conn, err := e.ConnectWebSocket(t.Context(), str(r, "id"), SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id := str(conn, "id")
	if err = e.SendWebSocket(t.Context(), id, "hello", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		events, _ := e.Store.Find(t.Context(), "websocket_event", "connectionId", id)
		for _, event := range events {
			if boolean(event, "isServer") && str(event, "messageType") == "text" {
				return len(array(event, "message")) == 5
			}
		}
		return false
	})
	if err = e.CloseWebSocket(id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { m, _ := e.Store.Get(t.Context(), id); return str(m, "state") == "closed" })
}
func TestGRPCReflectionAndUnary(t *testing.T) {
	e := testEngine(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "grpc_request", "workspaceId": str(w, "id"), "url": "http://" + listener.Addr().String(), "service": "grpc.health.v1.Health", "method": "Check", "message": "{}"})
	id := str(r, "id")
	services, err := e.ReflectGRPC(t.Context(), id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range services {
		if s.Name == "grpc.health.v1.Health" {
			found = true
		}
	}
	if !found {
		t.Fatal(services)
	}
	conn, err := e.StartGRPC(t.Context(), id, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { m, _ := e.Store.Get(t.Context(), str(conn, "id")); return str(m, "state") == "closed" })
	events, err := e.Store.Find(t.Context(), "grpc_event", "connectionId", str(conn, "id"))
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, event := range events {
		if str(event, "eventType") == "server_message" && strings.Contains(str(event, "content"), "SERVING") {
			found = true
		}
	}
	if !found {
		t.Fatal(events)
	}
}
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		if condition() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("condition did not become true")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type streamFixture struct {
	testpb.UnimplementedTestServiceServer
}

func (streamFixture) StreamingOutputCall(_ *testpb.StreamingOutputCallRequest, stream grpc.ServerStreamingServer[testpb.StreamingOutputCallResponse]) error {
	for range 2 {
		if err := stream.Send(&testpb.StreamingOutputCallResponse{Payload: &testpb.Payload{Body: []byte("echo")}}); err != nil {
			return err
		}
	}
	return nil
}
func (streamFixture) StreamingInputCall(stream grpc.ClientStreamingServer[testpb.StreamingInputCallRequest, testpb.StreamingInputCallResponse]) error {
	var count int32
	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return stream.SendAndClose(&testpb.StreamingInputCallResponse{AggregatedPayloadSize: count})
		}
		if err != nil {
			return err
		}
		count++
	}
}
func (streamFixture) FullDuplexCall(stream grpc.BidiStreamingServer[testpb.StreamingOutputCallRequest, testpb.StreamingOutputCallResponse]) error {
	for {
		m, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = stream.Send(&testpb.StreamingOutputCallResponse{Payload: m.Payload}); err != nil {
			return err
		}
	}
}
func TestGRPCStreamVariants(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	testpb.RegisterTestServiceServer(server, streamFixture{})
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	for _, method := range []string{"StreamingOutputCall", "StreamingInputCall", "FullDuplexCall"} {
		t.Run(method, func(t *testing.T) {
			e := testEngine(t)
			w := saveTest(t, e, Object{"model": "workspace"})
			r := saveTest(t, e, Object{"model": "grpc_request", "workspaceId": str(w, "id"), "url": "http://" + listener.Addr().String(), "service": "grpc.testing.TestService", "method": method, "message": `{"payload":{"body":"YQ=="}}`})
			conn, err := e.StartGRPC(t.Context(), str(r, "id"), "", nil)
			if err != nil {
				t.Fatal(err)
			}
			id := str(conn, "id")
			if method != "StreamingOutputCall" {
				if err = e.SendGRPCMessage(t.Context(), id, `{"payload":{"body":"Yg=="}}`); err != nil {
					t.Fatal(err)
				}
				if err = e.FinishGRPC(id); err != nil {
					t.Fatal(err)
				}
			}
			waitFor(t, func() bool { m, _ := e.Store.Get(t.Context(), id); return str(m, "state") == "closed" })
			events, err := e.Store.Find(t.Context(), "grpc_event", "connectionId", id)
			if err != nil {
				t.Fatal(err)
			}
			received := 0
			for _, event := range events {
				if str(event, "eventType") == "server_message" {
					received++
				}
			}
			expected := 2
			if method == "StreamingInputCall" {
				expected = 1
			}
			if received != expected {
				t.Fatalf("got %d response messages, expected %d: %v", received, expected, events)
			}
			if len(events) == 0 || str(events[0], "eventType") != "connection_start" || str(events[len(events)-1], "eventType") != "connection_end" {
				t.Fatalf("events out of order: %v", events)
			}
		})
	}
}
