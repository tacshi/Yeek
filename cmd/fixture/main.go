// Fixture provides loopback endpoints for checking Yeek's native protocol views.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	port := flag.Int("port", 9840, "HTTP port")
	grpcPort := flag.Int("grpc-port", 9841, "gRPC port")
	tlsDir := flag.String("tls-dir", "", "Generate synthetic mTLS files in this directory and enable the TLS fixture")
	tlsPort := flag.Int("tls-port", 9842, "mTLS fixture port")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mux := http.NewServeMux()
	oauthFixture(mux)
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		parts := []map[string]any{}
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, err := io.ReadAll(part)
			_ = part.Close()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			parts = append(parts, map[string]any{"name": part.FormName(), "filename": part.FileName(), "contentType": part.Header.Get("Content-Type"), "bytes": len(data), "text": string(data[:min(len(data), 512)])})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.MarshalWrite(w, map[string]any{"method": r.Method, "contentType": r.Header.Get("Content-Type"), "parts": parts})
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		value := map[string]any{"service": "Yeek fixture", "method": r.Method, "query": r.URL.Query(), "headers": r.Header, "items": []any{map[string]any{"id": 1, "name": "Alpha"}, map[string]any{"id": 2, "name": "Beta"}}}
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			value["clientCertificate"] = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		data, _ := json.Marshal(value)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/cookies", func(w http.ResponseWriter, r *http.Request) {
		age := 3600
		if r.URL.Query().Get("clear") == "1" {
			age = -1
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture-session", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age}) // #nosec G124 -- localhost fixture exercises ordinary HTTP cookies.
		w.Header().Set("Content-Type", "application/json")
		_ = json.MarshalWrite(w, map[string]any{"cookie": r.Header.Get("Cookie")})
	})
	mux.HandleFunc("/cookies/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "redirect", Value: "fixture-redirect", Path: "/"}) // #nosec G124 -- localhost redirect-cookie fixture.
		http.Redirect(w, r, "/cookies", http.StatusFound)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			kind, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if err = conn.Write(r.Context(), kind, data); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		for i := 0; ; i++ {
			_, _ = fmt.Fprintf(w, "id: %d\nevent: tick\ndata: {\"tick\":%d}\n\n", i, i)
			flusher.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
	})
	mux.HandleFunc("/csv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = fmt.Fprint(w, "id,name\n1,Alpha\n2,Beta\n")
	})
	mux.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<!doctype html><h1>Yeek fixture</h1><p>A native HTML response preview.</p><p><strong>Bold text</strong> and <em>emphasis</em>.</p>")
	})
	mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		img := image.NewRGBA(image.Rect(0, 0, 240, 120))
		for y := 0; y < 120; y++ {
			for x := 0; x < 240; x++ {
				img.Set(x, y, color.RGBA{R: 120, G: 80, B: 220, A: 255})
			}
		}
		_ = png.Encode(w, img)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","description":"Fixture query fields","fields":[{"name":"message","description":"Returns a fixture message","args":[],"type":{"kind":"SCALAR","name":"String"}}]},{"kind":"SCALAR","name":"String","description":"Text value"}]}}}`)
	})
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	grpcListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *grpcPort))
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if *tlsDir != "" {
		config, err := fixtureTLSFiles(*tlsDir)
		if err != nil {
			log.Fatal(err)
		}
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *tlsPort))
		if err != nil {
			log.Fatal(err)
		}
		tlsServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		defer func() { _ = tlsServer.Close() }()
		go func() {
			if err := tlsServer.Serve(tls.NewListener(listener, config)); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Print(err)
			}
		}()
		fmt.Printf("mTLS: https://api.test:%d/json\nTLS files: %s (PFX passphrase: fixture-pass)\n", *tlsPort, *tlsDir)
	}
	rpc := grpc.NewServer()
	healthpb.RegisterHealthServer(rpc, health.NewServer())
	reflection.Register(rpc)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print(err)
		}
	}()
	go func() {
		if err := rpc.Serve(grpcListener); err != nil {
			log.Print(err)
		}
	}()
	fmt.Printf("HTTP: http://%s\nWebSocket: ws://%s/ws\ngRPC: http://%s\n", listener.Addr(), listener.Addr(), grpcListener.Addr())
	<-ctx.Done()
	rpc.Stop()
	_ = server.Close()
}
