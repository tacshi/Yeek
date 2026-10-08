package engine

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/youmark/pkcs8"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"software.sslmate.com/src/go-pkcs12"
)

type tlsFixture struct {
	root, client                 *x509.Certificate
	clientKey                    *ecdsa.PrivateKey
	server                       tls.Certificate
	ca, crt, key, pfx, encrypted string
	pool                         *x509.CertPool
}

func makeTLSFixture(t *testing.T) tlsFixture {
	t.Helper()
	dir := t.TempDir()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Yeek test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(serial int64, client bool) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Yeek test endpoint"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"first.test", "second.test", "proxy.test", "api.test", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
		if client {
			template.Subject.CommonName = "Yeek test client"
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, template, root, &key.PublicKey, rootKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	}
	_, _, serverPEM, serverKey := issue(2, false)
	server, err := tls.X509KeyPair(serverPEM, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	client, clientKey, clientPEM, keyPEM := issue(3, true)
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fixture := tlsFixture{root: root, client: client, clientKey: clientKey, server: server, pool: x509.NewCertPool()}
	fixture.pool.AddCert(root)
	fixture.ca = write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}))
	fixture.crt = write("client.pem", clientPEM)
	fixture.key = write("client.key", keyPEM)
	pfx, err := pkcs12.Modern.Encode(clientKey, client, []*x509.Certificate{root}, "fixture-pass")
	if err != nil {
		t.Fatal(err)
	}
	fixture.pfx = write("client.p12", pfx)
	encrypted, err := pkcs8.MarshalPrivateKey(clientKey, []byte("fixture-pass"), nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture.encrypted = write("encrypted.key", pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: encrypted}))
	return fixture
}
func fixtureTLS(t *testing.T, f tlsFixture, auth tls.ClientAuthType, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{f.server}, ClientCAs: f.pool, ClientAuth: auth}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
func endpointHost(t *testing.T, raw, host string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = net.JoinHostPort(host, u.Port())
	return u.String()
}
func networkWorkspace(t *testing.T, e *Engine, ca string) Object {
	t.Helper()
	overrides := []any{}
	for _, host := range []string{"first.test", "second.test", "proxy.test", "api.test", "rpc.test"} {
		overrides = append(overrides, Object{"hostname": host, "ipv4": []any{"127.0.0.1"}, "ipv6": []any{}, "enabled": true})
	}
	return saveTest(t, e, Object{"model": "workspace", "caFile": ca, "settingDnsOverrides": overrides})
}

func TestTLSCertificateFormatsAndHostScope(t *testing.T) {
	f := makeTLSFixture(t)
	for _, model := range []Object{{"host": "first.test", "crtFile": f.crt, "keyFile": f.key}, {"host": "first.test", "crtFile": f.crt, "keyFile": f.encrypted, "passphrase": "fixture-pass"}, {"host": "first.test", "pfxFile": f.pfx, "passphrase": "fixture-pass"}} {
		pair, err := LoadClientCertificate(model)
		if err != nil || pair.Leaf.Subject.CommonName != "Yeek test client" {
			t.Fatal(err)
		}
	}
	if _, err := LoadClientCertificate(Object{"host": "first.test", "pfxFile": f.pfx, "passphrase": "wrong"}); err == nil {
		t.Fatal("wrong PFX passphrase accepted")
	}
	e := testEngine(t)
	workspace := networkWorkspace(t, e, f.ca)
	var firstSaw, secondSaw atomic.Int64
	second := fixtureTLS(t, f, tls.RequestClientCert, func(w http.ResponseWriter, r *http.Request) {
		secondSaw.Store(int64(len(r.TLS.PeerCertificates)))
		_, _ = io.WriteString(w, "redirect reached")
	})
	first := fixtureTLS(t, f, tls.RequireAndVerifyClientCert, func(w http.ResponseWriter, r *http.Request) {
		firstSaw.Store(int64(len(r.TLS.PeerCertificates)))
		http.Redirect(w, r, endpointHost(t, second.URL, "second.test"), http.StatusFound)
	})
	certificate := Object{"host": "FIRST.TEST.", "crtFile": f.crt, "keyFile": f.encrypted, "passphrase": "fixture-pass", "enabled": true}
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": Object{"type": "disabled"}, "clientCertificates": []any{certificate}})
	request := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(workspace, "id"), "url": endpointHost(t, first.URL, "first.test")})
	response, err := e.SendHTTP(t.Context(), str(request, "id"), SendOptions{})
	if err != nil || number(response, "status") != 200 {
		t.Fatal(response, err)
	}
	if firstSaw.Load() != 1 || secondSaw.Load() != 0 {
		t.Fatalf("certificate scope leaked: first=%d second=%d", firstSaw.Load(), secondSaw.Load())
	}
	certificate["enabled"] = false
	saveTest(t, e, Object{"model": "settings", "id": "default", "clientCertificates": []any{certificate}})
	if _, err = e.SendHTTP(t.Context(), str(request, "id"), SendOptions{}); err == nil {
		t.Fatal("disabled certificate was still sent")
	}
}

func TestProxyParsingAuthenticationAndBypass(t *testing.T) {
	for _, raw := range []string{"localhost:9000", "http://localhost:9000", "https://proxy.test", "socks5://localhost:1080", "socks5h://localhost:1080"} {
		if _, err := NormalizeProxyURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"ftp://host", "http://host:99999", "http://host/path", "http://user:secret@host/?query"} {
		if _, err := NormalizeProxyURL(raw); err == nil {
			t.Fatal("invalid proxy accepted", raw)
		}
	}
	for _, tt := range []struct {
		target, bypass string
		want           bool
	}{{"https://api.example.com", "*.example.com", true}, {"https://notexample.com", ".example.com", false}, {"http://localhost:9000", "localhost:9000", true}, {"http://localhost:9001", "localhost:9000", false}, {"http://10.1.2.3", "10.0.0.0/8", true}, {"http://[::1]:8000", "[::1]:8000", true}} {
		u, _ := url.Parse(tt.target)
		if ProxyBypassed(u, tt.bypass) != tt.want {
			t.Fatal(tt)
		}
	}
	var proxyCalls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:fixture-pass")) {
			t.Error("missing proxy authentication")
		}
		_, _ = io.WriteString(w, "proxy")
	}))
	defer proxy.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "origin") }))
	defer origin.Close()
	e := testEngine(t)
	workspace := networkWorkspace(t, e, "")
	config := Object{"type": "enabled", "http": strings.TrimPrefix(proxy.URL, "http://"), "https": "", "bypass": "", "auth": Object{"user": "user", "password": "fixture-pass"}}
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": config})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(workspace, "id"), "url": origin.URL})
	res, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := e.Body(str(res, "id"))
	if string(body) != "proxy" {
		t.Fatal(string(body))
	}
	config["bypass"] = "127.0.0.1"
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": config})
	res, err = e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = e.Body(str(res, "id"))
	if string(body) != "origin" || proxyCalls.Load() != 1 {
		t.Fatal(string(body), proxyCalls.Load())
	}
	config["bypass"] = ""
	config["disabled"] = true
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": config})
	if _, err = e.SendHTTP(t.Context(), str(r, "id"), SendOptions{}); err != nil || proxyCalls.Load() != 1 {
		t.Fatal("temporarily disabled proxy was used", err)
	}
}

func connectFixture(t *testing.T, target string, count *atomic.Int64, peers *atomic.Int64) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target {
			http.Error(w, "unexpected CONNECT target", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:fixture-pass")) {
			http.Error(w, "authentication required", http.StatusProxyAuthRequired)
			return
		}
		count.Add(1)
		if peers != nil && r.TLS != nil {
			peers.Store(int64(len(r.TLS.PeerCertificates)))
		}
		upstream, err := net.DialTimeout("tcp", target, 3*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		defer func() { _ = client.Close(); _ = upstream.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err = rw.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, rw); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-done
	}
}
func TestHTTPSProxyKeepsClientIdentityScopedToOrigin(t *testing.T) {
	f := makeTLSFixture(t)
	origin := fixtureTLS(t, f, tls.RequireAndVerifyClientCert, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "mTLS") })
	u, _ := url.Parse(origin.URL)
	var calls, proxyPeers atomic.Int64
	proxy := fixtureTLS(t, f, tls.RequestClientCert, connectFixture(t, u.Host, &calls, &proxyPeers))
	e := testEngine(t)
	workspace := networkWorkspace(t, e, f.ca)
	saveTest(t, e, Object{"model": "settings", "id": "default", "clientCertificates": []any{Object{"host": "first.test", "pfxFile": f.pfx, "passphrase": "fixture-pass"}}, "proxy": Object{"type": "enabled", "https": endpointHost(t, proxy.URL, "proxy.test"), "auth": Object{"user": "user", "password": "fixture-pass"}}})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(workspace, "id"), "url": endpointHost(t, origin.URL, "first.test")})
	response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	if err != nil || number(response, "status") != 200 || calls.Load() != 1 || proxyPeers.Load() != 0 {
		t.Fatal(response, err, calls.Load(), proxyPeers.Load())
	}
}
func TestGRPCUsesCustomProxyAndDNS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthpb.RegisterHealthServer(server, health.NewServer())
	reflection.Register(server)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	var calls atomic.Int64
	proxy := httptest.NewServer(connectFixture(t, listener.Addr().String(), &calls, nil))
	defer proxy.Close()
	e := testEngine(t)
	workspace := networkWorkspace(t, e, "")
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": Object{"type": "enabled", "http": proxy.URL, "auth": Object{"user": "user", "password": "fixture-pass"}}})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	r := saveTest(t, e, Object{"model": "grpc_request", "workspaceId": str(workspace, "id"), "url": "http://rpc.test:" + port})
	services, err := e.ReflectGRPC(t.Context(), str(r, "id"), "", nil)
	if err != nil || len(services) == 0 || calls.Load() == 0 {
		t.Fatal(services, err, calls.Load())
	}
}

func TestDNSOverrideValidationAndCertificatePorts(t *testing.T) {
	for _, model := range []Object{{"hostname": "host", "ipv4": []any{"::1"}}, {"hostname": "http://host", "ipv4": []any{"127.0.0.1"}}, {"hostname": "host", "ipv6": []any{"invalid"}}} {
		if ValidateDNSOverride(model) == nil {
			t.Fatal(model)
		}
	}
	u, _ := url.Parse("https://EXAMPLE.COM:8443")
	if !certificateMatches(Object{"host": "example.com", "port": 8443}, u) || certificateMatches(Object{"host": "example.com", "port": 443}, u) {
		t.Fatal("certificate port matching failed")
	}
	if _, err := NetworkHostname("https://host"); err == nil {
		t.Fatal("accepted URL as a hostname")
	}
}

func TestSOCKSProxyAuthenticationAndDNSOverride(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "api.test:") {
			t.Error("Host was replaced by the DNS address")
		}
		_, _ = io.WriteString(w, "SOCKS reached origin")
	}))
	defer origin.Close()
	u, _ := url.Parse(origin.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan error, 1)
	go func() {
		client, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = client.Close() }()
		read := func(n int) ([]byte, error) {
			buf := make([]byte, n)
			_, err := io.ReadFull(client, buf)
			return buf, err
		}
		header, err := read(2)
		if err != nil {
			done <- err
			return
		}
		if _, err = read(int(header[1])); err != nil {
			done <- err
			return
		}
		_, _ = client.Write([]byte{5, 2})
		auth, err := read(2)
		if err != nil {
			done <- err
			return
		}
		user, err := read(int(auth[1]))
		if err != nil {
			done <- err
			return
		}
		length, err := read(1)
		if err != nil {
			done <- err
			return
		}
		password, err := read(int(length[0]))
		if err != nil {
			done <- err
			return
		}
		if string(user) != "user" || string(password) != "fixture-pass" {
			done <- errors.New("SOCKS authentication did not match")
			return
		}
		_, _ = client.Write([]byte{1, 0})
		request, err := read(4)
		if err != nil {
			done <- err
			return
		}
		if request[3] != 1 {
			done <- errors.New("SOCKS target did not use the DNS override")
			return
		}
		address, err := read(6)
		if err != nil {
			done <- err
			return
		}
		target := net.JoinHostPort(net.IP(address[:4]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(address[4:]))))
		if target != u.Host {
			done <- errors.New("SOCKS target did not match the fixture")
			return
		}
		upstream, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = upstream.Close() }()
		_, _ = client.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(copied) }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-copied
		done <- nil
	}()
	e := testEngine(t)
	workspace := networkWorkspace(t, e, "")
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": Object{"type": "enabled", "http": "socks5://" + listener.Addr().String(), "auth": Object{"user": "user", "password": "fixture-pass"}}})
	request := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(workspace, "id"), "url": endpointHost(t, origin.URL, "api.test")})
	response, err := e.SendHTTP(t.Context(), str(request, "id"), SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := e.Body(str(response, "id"))
	if err != nil || string(body) != "SOCKS reached origin" {
		t.Fatal(string(body), err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
