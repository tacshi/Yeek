package engine

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type networkTransport struct {
	mu                                   sync.Mutex
	clients                              map[string]*http.Transport
	settings, workspace, requestSettings Object
	roots                                *x509.CertPool
}

func (e *Engine) transport(ctx context.Context, request resolvedRequest) (*networkTransport, error) {
	settings, err := e.Store.Get(ctx, "default")
	if err != nil {
		return nil, err
	}
	roots, err := certificateAuthorities(str(request.Workspace, "caFile"))
	if err != nil {
		return nil, err
	}
	return &networkTransport{settings: settings, workspace: request.Workspace, requestSettings: request.Settings, roots: roots, clients: map[string]*http.Transport{}}, nil
}

func (t *networkTransport) tlsFor(target *url.URL) (*tls.Config, error) {
	host, err := NetworkHostname(target.Hostname())
	if err != nil {
		return nil, err
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: t.roots, ServerName: host, InsecureSkipVerify: !boolean(t.requestSettings, "settingValidateCertificates")} // #nosec G402 -- preserves the explicit inherited API testing option; verification is enabled by default.
	for _, model := range objects(array(t.settings, "clientCertificates")) {
		if target.Scheme != "https" && target.Scheme != "wss" && target.Scheme != "grpcs" {
			break
		}
		if !certificateMatches(model, target) {
			continue
		}
		pair, err := LoadClientCertificate(model)
		if err != nil {
			return nil, err
		}
		config.Certificates = append(config.Certificates, pair)
	}
	return config, nil
}
func (t *networkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	proxy, err := t.proxyFor(request.URL)
	if err != nil {
		return nil, err
	}
	key := request.URL.Scheme + "://" + request.URL.Host
	if proxy != nil {
		key += "|" + proxy.String()
	}
	t.mu.Lock()
	client := t.clients[key]
	if client == nil {
		client, err = t.httpTransport(request.URL, proxy)
		if err == nil {
			t.clients[key] = client
		}
	}
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return client.RoundTrip(request)
}
func (t *networkTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, client := range t.clients {
		client.CloseIdleConnections()
	}
}
func (t *networkTransport) httpTransport(target, proxy *url.URL) (*http.Transport, error) {
	config, err := t.tlsFor(target)
	if err != nil {
		return nil, err
	}
	client := &http.Transport{TLSClientConfig: config, DialContext: t.dialDirect, TLSHandshakeTimeout: 15 * time.Second, IdleConnTimeout: 30 * time.Second, DisableCompression: true, Protocols: new(http.Protocols)}
	switch str(t.requestSettings, "settingHttpVersion") {
	case "http1":
		client.Protocols.SetHTTP1(true)
	case "http2":
		client.Protocols.SetHTTP2(true)
		client.Protocols.SetUnencryptedHTTP2(target.Scheme == "http")
	default:
		client.Protocols.SetHTTP1(true)
		client.Protocols.SetHTTP2(true)
	}
	if proxy == nil {
		return client, nil
	}
	_, override, err := t.overrideAddresses(target.Hostname())
	if err != nil {
		return nil, err
	}
	if target.Scheme == "https" || proxy.Scheme == "socks5" || proxy.Scheme == "socks5h" || override {
		client.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return t.dialProxy(ctx, address, proxy)
		}
	} else {
		client.Proxy = http.ProxyURL(proxy)
		if proxy.Scheme == "https" {
			client.TLSClientConfig, err = t.tlsFor(proxy)
			if err != nil {
				return nil, err
			}
		}
	}
	return client, nil
}

func NormalizeProxyURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
		return nil, errors.New("proxy address must use HTTP, HTTPS, or SOCKS5")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy address must not contain a path, query, or fragment")
	}
	if _, err = NetworkHostname(u.Hostname()); err != nil {
		return nil, fmt.Errorf("proxy hostname: %w", err)
	}
	if raw := u.Port(); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("proxy port must be between 1 and 65535")
		}
	}
	u.Path = ""
	return u, nil
}
func ProxyBypassed(target *url.URL, bypass string) bool {
	host, err := NetworkHostname(target.Hostname())
	if err != nil {
		return false
	}
	for rule := range strings.SplitSeq(bypass, ",") {
		rule = strings.TrimSpace(strings.ToLower(rule))
		if rule == "" {
			continue
		}
		if rule == "*" {
			return true
		}
		if rule == "<local>" {
			if ip, err := netip.ParseAddr(host); err == nil {
				if ip.IsLoopback() {
					return true
				}
			} else if !strings.Contains(host, ".") {
				return true
			}
			continue
		}
		if prefix, err := netip.ParsePrefix(rule); err == nil {
			if ip, err := netip.ParseAddr(host); err == nil && prefix.Contains(ip) {
				return true
			}
			continue
		}
		name, port := rule, ""
		if h, p, err := net.SplitHostPort(rule); err == nil {
			name, port = h, p
		} else if strings.Count(rule, ":") == 1 {
			name, port, _ = strings.Cut(rule, ":")
		}
		if port != "" && port != networkPort(target) {
			continue
		}
		if strings.HasPrefix(name, "*.") || strings.HasPrefix(name, ".") {
			domain := strings.TrimPrefix(strings.TrimPrefix(name, "*"), ".")
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		} else if canonical, err := NetworkHostname(name); err == nil && host == canonical {
			return true
		}
	}
	return false
}
func (t *networkTransport) proxyFor(target *url.URL) (*url.URL, error) {
	settings := obj(t.settings, "proxy")
	if len(settings) == 0 {
		endpoint := target.Clone()
		if endpoint.Scheme == "wss" || endpoint.Scheme == "grpcs" {
			endpoint.Scheme = "https"
		}
		if endpoint.Scheme == "ws" || endpoint.Scheme == "grpc" {
			endpoint.Scheme = "http"
		}
		return http.ProxyFromEnvironment(&http.Request{URL: endpoint})
	}
	if str(settings, "type") == "disabled" || boolean(settings, "disabled") {
		return nil, nil
	}
	if ProxyBypassed(target, str(settings, "bypass")) {
		return nil, nil
	}
	key := "http"
	if target.Scheme == "https" || target.Scheme == "wss" || target.Scheme == "grpcs" {
		key = "https"
	}
	u, err := NormalizeProxyURL(str(settings, key))
	if err != nil || u == nil {
		return u, err
	}
	if auth := obj(settings, "auth"); len(auth) > 0 {
		u.User = url.UserPassword(str(auth, "user"), str(auth, "password"))
	}
	return u, nil
}

func ValidateDNSOverride(value Object) error {
	if _, err := NetworkHostname(str(value, "hostname")); err != nil {
		return err
	}
	n := 0
	for _, key := range []string{"ipv4", "ipv6"} {
		for _, raw := range array(value, key) {
			ip, err := netip.ParseAddr(fmt.Sprint(raw))
			if err != nil || key == "ipv4" && !ip.Is4() || key == "ipv6" && !ip.Is6() {
				return fmt.Errorf("enter valid %s addresses separated by commas", strings.ToUpper(key))
			}
			n++
		}
	}
	if n == 0 {
		return errors.New("enter at least one IP address")
	}
	return nil
}
func (t *networkTransport) overrideAddresses(host string) ([]string, bool, error) {
	want, err := NetworkHostname(host)
	if err != nil {
		return nil, false, err
	}
	for _, entry := range objects(array(t.workspace, "settingDnsOverrides")) {
		if !enabled(entry) {
			continue
		}
		name, err := NetworkHostname(str(entry, "hostname"))
		if err != nil || name != want {
			continue
		}
		if err = ValidateDNSOverride(entry); err != nil {
			return nil, true, fmt.Errorf("DNS override for %s: %w", host, err)
		}
		result := []string{}
		for _, key := range []string{"ipv4", "ipv6"} {
			for _, ip := range array(entry, key) {
				result = append(result, fmt.Sprint(ip))
			}
		}
		return result, true, nil
	}
	return nil, false, nil
}
func (t *networkTransport) dialDirect(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	addresses, override, err := t.overrideAddresses(host)
	if err != nil {
		return nil, err
	}
	if !override {
		return dialer.DialContext(ctx, network, address)
	}
	if trace := httptrace.ContextClientTrace(ctx); trace != nil {
		if trace.DNSStart != nil {
			trace.DNSStart(httptrace.DNSStartInfo{Host: host})
		}
		if trace.DNSDone != nil {
			values := []net.IPAddr{}
			for _, ip := range addresses {
				values = append(values, net.IPAddr{IP: net.ParseIP(ip)})
			}
			trace.DNSDone(httptrace.DNSDoneInfo{Addrs: values})
		}
	}
	var last error
	for _, ip := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, fmt.Errorf("DNS override for %s: %w", host, last)
}
func (t *networkTransport) dialProxy(ctx context.Context, address string, proxyURL *url.URL) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, override, err := t.overrideAddresses(host)
	if err != nil {
		return nil, err
	}
	if !override {
		addresses = []string{host}
	}
	var last error
	for _, candidate := range addresses {
		target := net.JoinHostPort(candidate, port)
		var conn net.Conn
		if proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h" {
			var auth *xproxy.Auth
			if proxyURL.User != nil {
				password, _ := proxyURL.User.Password()
				auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
			}
			dialer, err := xproxy.SOCKS5("tcp", net.JoinHostPort(proxyURL.Hostname(), networkPort(proxyURL)), auth, contextDialer{ctx: ctx, dial: t.dialDirect})
			if err != nil {
				return nil, err
			}
			if d, ok := dialer.(xproxy.ContextDialer); ok {
				conn, last = d.DialContext(ctx, "tcp", target)
			} else {
				return nil, errors.New("SOCKS proxy does not support cancellation")
			}
		} else {
			conn, last = t.connectProxy(ctx, target, proxyURL)
		}
		if last == nil {
			return conn, nil
		}
	}
	return nil, last
}

type contextDialer struct {
	ctx  context.Context
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d contextDialer) Dial(network, address string) (net.Conn, error) {
	return d.dial(d.ctx, network, address)
}
func (d contextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d.dial(ctx, network, address)
}

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c bufferedProxyConn) Read(data []byte) (int, error) { return c.reader.Read(data) }
func (t *networkTransport) connectProxy(ctx context.Context, target string, proxyURL *url.URL) (net.Conn, error) {
	conn, err := t.dialDirect(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), networkPort(proxyURL)))
	if err != nil {
		return nil, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if proxyURL.Scheme == "https" {
		config, err := t.tlsFor(proxyURL)
		if err != nil {
			return nil, err
		}
		config.NextProtos = []string{"http/1.1"}
		tlsConn := tls.Client(conn, config)
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			return nil, fmt.Errorf("proxy TLS: %w", err)
		}
		conn = tlsConn
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+password)))
	}
	if err = req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("proxy CONNECT returned %s", response.Status)
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if !stop() && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	succeeded = true
	return bufferedProxyConn{Conn: conn, reader: reader}, nil
}
func (t *networkTransport) grpcDialer(target *url.URL) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		address := net.JoinHostPort(target.Hostname(), networkPort(target))
		proxy, err := t.proxyFor(target)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			return t.dialProxy(ctx, address, proxy)
		}
		return t.dialDirect(ctx, "tcp", address)
	}
}
