package mcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// DefaultAddr is where the app serves its tools, as Yaak does.
	DefaultAddr = "127.0.0.1:64343"
	// Path is the server's one path.
	Path = "/mcp"
)

// HTTPServer is a running server; Stop stops it.
type HTTPServer struct {
	// Addr is the address it listens on.
	Addr string
	srv  *http.Server
}

// Serve serves the tools on addr at Path, answering only requests that
// carry Authorization: Bearer <token>.
func Serve(host Host, version, addr, token string) (*HTTPServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	server := NewServer(host, version)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute})
	expected := []byte("Bearer " + token)
	route := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Path {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Missing or invalid bearer token", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})
	s := &HTTPServer{Addr: ln.Addr().String(), srv: &http.Server{Handler: route, ReadHeaderTimeout: 10 * time.Second}}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = ln.Close()
		}
	}()
	return s, nil
}

// Stop stops serving, closing the streams still open after a second.
func (s *HTTPServer) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		_ = s.srv.Close()
	}
}
