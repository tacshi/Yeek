package engine

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func oauthRandom() string {
	data := make([]byte, 32)
	_, _ = rand.Read(data)
	return base64.RawURLEncoding.EncodeToString(data)
}
func oauthPKCE(auth Object) (string, string, string, error) {
	if !oauthBool(auth, "usePkce", true) {
		return "", "", "", nil
	}
	verifier := cmp.Or(str(auth, "pkceCodeVerifier"), oauthRandom())
	if len(verifier) < 43 || len(verifier) > 128 || strings.ContainsFunc(verifier, func(r rune) bool {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)
		return !valid
	}) {
		return "", "", "", errors.New("PKCE verifier must contain 43–128 letters, digits, or -._~")
	}
	method := cmp.Or(str(auth, "pkceChallengeMethod"), "S256")
	switch method {
	case "plain":
		return verifier, verifier, method, nil
	case "S256":
		hash := sha256.Sum256([]byte(verifier))
		return verifier, base64.RawURLEncoding.EncodeToString(hash[:]), method, nil
	default:
		return "", "", "", errors.New("PKCE method must be S256 or plain")
	}
}
func validateOAuthRedirect(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User != nil || u.Fragment != "" || slices.Contains([]string{"javascript", "data", "file", "about"}, strings.ToLower(u.Scheme)) {
		return nil, errors.New("enter an absolute OAuth redirect URI without credentials or a fragment")
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() == "" {
		return nil, errors.New("HTTP redirect URIs require a hostname")
	}
	return u, nil
}
func BuildOAuthAuthorization(auth Object, redirect, contextID string) (OAuthAuthorization, string, error) {
	auth = normalizeOAuth(auth)
	endpoint, err := oauthHTTPURL(str(auth, "authorizationUrl"), "authorization URL")
	if err != nil {
		return OAuthAuthorization{}, "", err
	}
	if _, err = validateOAuthRedirect(redirect); err != nil {
		return OAuthAuthorization{}, "", err
	}
	grant := str(auth, "grantType")
	q := endpoint.Query()
	responseType := "code"
	if grant == "implicit" {
		responseType = str(auth, "responseType")
		if !slices.Contains([]string{"token", "id_token", "id_token token", "token id_token"}, responseType) {
			return OAuthAuthorization{}, "", errors.New("choose a supported implicit response type")
		}
	}
	q.Set("response_type", responseType)
	q.Set("client_id", str(auth, "clientId"))
	q.Set("state", cmp.Or(str(auth, "state"), oauthRandom()))
	for _, key := range []string{"scope", "audience"} {
		if value := str(auth, key); value != "" {
			q.Set(key, value)
		}
	}
	verifier := ""
	if grant == "authorization_code" {
		var challenge, method string
		verifier, challenge, method, err = oauthPKCE(auth)
		if err != nil {
			return OAuthAuthorization{}, "", err
		}
		q.Del("code_challenge")
		q.Del("code_challenge_method")
		if verifier != "" {
			q.Set("code_challenge", challenge)
			q.Set("code_challenge_method", method)
		}
	}
	if strings.Contains(responseType, "id_token") {
		q.Set("nonce", oauthRandom())
	}
	pairs, err := parseOAuthPairs(auth["authorizationParams"])
	if err != nil {
		return OAuthAuthorization{}, "", fmt.Errorf("authorization parameters: %w", err)
	}
	seen := map[string]bool{}
	for _, pair := range pairs {
		if !seen[pair.Name] {
			q.Del(pair.Name)
			seen[pair.Name] = true
		}
		q.Add(pair.Name, pair.Value)
	}
	q.Set("redirect_uri", redirect)
	if q.Get("state") == "" || len(q["state"]) != 1 {
		return OAuthAuthorization{}, "", errors.New("authorization requires one nonempty state value")
	}
	if q.Get("response_type") != responseType {
		return OAuthAuthorization{}, "", errors.New("set response type with the OAuth grant controls")
	}
	if strings.Contains(responseType, "id_token") && (q.Get("nonce") == "" || len(q["nonce"]) != 1) {
		return OAuthAuthorization{}, "", errors.New("ID token authorization requires one nonempty nonce")
	}
	endpoint.RawQuery = q.Encode()
	return OAuthAuthorization{URL: endpoint.String(), RedirectURI: redirect, State: q.Get("state"), Nonce: q.Get("nonce"), GrantType: grant, TokenName: str(auth, "tokenName"), ContextID: contextID}, verifier, nil
}

func OAuthCallbackMatches(raw, redirect string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	expected, err := url.Parse(redirect)
	if err != nil {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		if u.Scheme == "http" {
			return "80"
		}
		return ""
	}
	path := func(u *url.URL) string {
		if u.Path == "" {
			return "/"
		}
		return u.Path
	}
	if !strings.EqualFold(u.Scheme, expected.Scheme) || !strings.EqualFold(u.Hostname(), expected.Hostname()) || port(u) != port(expected) || path(u) != path(expected) || u.Opaque != expected.Opaque || u.User != nil {
		return false
	}
	for name, values := range expected.Query() {
		if !slices.Equal(u.Query()[name], values) {
			return false
		}
	}
	return true
}
func ParseOAuthCallback(raw string, authorization OAuthAuthorization) (Object, error) {
	if len(raw) > 2<<20 {
		return nil, errors.New("OAuth callback exceeds 2 MiB")
	}
	if !OAuthCallbackMatches(raw, authorization.RedirectURI) {
		return nil, errors.New("OAuth callback does not match the redirect URI")
	}
	u, _ := url.Parse(raw)
	values := u.Query()
	if u.Fragment != "" {
		fragment, err := url.ParseQuery(u.Fragment)
		if err != nil {
			return nil, errors.New("invalid OAuth callback fragment")
		}
		for key, entries := range fragment {
			if _, exists := values[key]; exists {
				return nil, fmt.Errorf("OAuth callback repeats %s", key)
			}
			values[key] = entries
		}
	}
	for _, key := range []string{"state", "code", "access_token", "id_token", "error"} {
		if len(values[key]) > 1 {
			return nil, fmt.Errorf("OAuth callback repeats %s", key)
		}
	}
	if subtle.ConstantTimeCompare([]byte(values.Get("state")), []byte(authorization.State)) != 1 {
		return nil, errors.New("OAuth callback state does not match")
	}
	if code := values.Get("error"); code != "" {
		return nil, fmt.Errorf("authorization declined: %s %s", oauthErrorText(code), oauthErrorText(values.Get("error_description")))
	}
	if authorization.GrantType == "authorization_code" && values.Get("code") == "" {
		return nil, errors.New("OAuth callback is missing an authorization code")
	}
	if authorization.GrantType == "implicit" && values.Get(authorization.TokenName) == "" {
		return nil, fmt.Errorf("OAuth callback is missing %s", authorization.TokenName)
	}
	if authorization.Nonce != "" && values.Get("id_token") != "" {
		claims := oauthJWTClaims(values.Get("id_token"))
		if subtle.ConstantTimeCompare([]byte(str(claims, "nonce")), []byte(authorization.Nonce)) != 1 {
			return nil, errors.New("ID token nonce does not match this authorization")
		}
	}
	result := Object{}
	for key, entries := range values {
		if len(entries) > 0 {
			result[key] = entries[0]
		}
	}
	return result, nil
}

func (e *Engine) authorizeOAuth(ctx context.Context, auth Object, options OAuthOptions) (Object, string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	redirect := str(auth, "redirectUri")
	external := oauthBool(auth, "useExternalBrowser", options.Browser == nil)
	if !external && options.Browser != nil {
		redirect = cmp.Or(redirect, "http://127.0.0.1:9461/callback")
		authorization, verifier, err := BuildOAuthAuthorization(auth, redirect, options.ContextID)
		if err != nil {
			return nil, "", "", err
		}
		callback, err := options.Browser(ctx, authorization)
		if err != nil {
			return nil, "", "", err
		}
		result, err := ParseOAuthCallback(callback, authorization)
		return result, redirect, verifier, err
	}
	if options.OpenExternal == nil {
		return nil, "", "", errors.New("fetch the token from an OAuth sign-in window first")
	}
	if redirect == "" {
		port := cmp.Or(str(auth, "callbackPort"), "9461")
		if number, err := strconv.Atoi(port); err != nil || number < 0 || number > 65535 {
			return nil, "", "", errors.New("callback port must be between 0 and 65535")
		}
		redirect = "http://127.0.0.1:" + port + "/callback"
	}
	callback, err := validateOAuthRedirect(redirect)
	if err != nil {
		return nil, "", "", err
	}
	host := callback.Hostname()
	ip := net.ParseIP(host)
	if callback.Scheme != "http" || host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, "", "", errors.New("system-browser callbacks must use HTTP on a loopback address")
	}
	bindHost := host
	if bindHost == "localhost" {
		bindHost = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindHost, cmp.Or(callback.Port(), "80")))
	if err != nil {
		return nil, "", "", fmt.Errorf("start OAuth callback listener: %w", err)
	}
	defer func() { _ = listener.Close() }()
	if callback.Port() == "0" {
		_, port, _ := net.SplitHostPort(listener.Addr().String())
		callback.Host = net.JoinHostPort(host, port)
		redirect = callback.String()
	}
	authorization, verifier, err := BuildOAuthAuthorization(auth, redirect, options.ContextID)
	if err != nil {
		return nil, "", "", err
	}
	received := make(chan string, 1)
	returnURI := "yeek://oauth/callback/" + oauthRandom()
	e.oauthMu.Lock()
	if e.oauthCallbacks == nil {
		e.oauthCallbacks = map[string]func(string) bool{}
	}
	e.oauthCallbacks[returnURI] = func(raw string) bool {
		u, err := url.Parse(raw)
		if err != nil {
			return false
		}
		actual := callback.Clone()
		query := actual.Query()
		for key, values := range u.Query() {
			query[key] = values
		}
		actual.RawQuery, actual.Fragment = query.Encode(), u.Fragment
		values := actual.Query()
		if actual.Fragment != "" {
			fragment, parseErr := url.ParseQuery(actual.Fragment)
			if parseErr != nil {
				return false
			}
			for key, entries := range fragment {
				if _, exists := values[key]; exists {
					return false
				}
				values[key] = entries
			}
		}
		if len(values["state"]) != 1 || subtle.ConstantTimeCompare([]byte(values.Get("state")), []byte(authorization.State)) != 1 {
			return false
		}
		select {
		case received <- actual.String():
			return true
		default:
			return false
		}
	}
	e.oauthMu.Unlock()
	defer func() { e.oauthMu.Lock(); delete(e.oauthCallbacks, returnURI); e.oauthMu.Unlock() }()
	server := http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if cmp.Or(r.URL.Path, "/") != cmp.Or(callback.Path, "/") || !strings.EqualFold(r.Host, callback.Host) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "Use GET or POST", http.StatusMethodNotAllowed)
			return
		}
		actual := callback.Clone()
		actual.RawQuery = r.URL.RawQuery
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Invalid callback form", http.StatusBadRequest)
				return
			}
			values := actual.Query()
			for key, values2 := range r.PostForm {
				values[key] = values2
			}
			actual.RawQuery = values.Encode()
		}
		values := actual.Query()
		if values.Get("code") == "" && values.Get(authorization.TokenName) == "" && values.Get("error") == "" {
			// RFC 9110 preserves the original fragment when Location has none.
			// This delivers implicit tokens to the app without a JavaScript relay.
			http.Redirect(w, r, returnURI, http.StatusFound)
			return
		}
		if values.Get("state") != authorization.State {
			http.Error(w, "OAuth state does not match", http.StatusBadRequest)
			return
		}
		select {
		case received <- actual.String():
		default:
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Authorization received. Return to Yeek.")
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener); close(done) }()
	defer func() { _ = server.Close(); <-done }()
	if err = options.OpenExternal(authorization.URL); err != nil {
		return nil, "", "", err
	}
	select {
	case err := <-done:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			err = errors.New("OAuth callback listener closed")
		}
		return nil, "", "", err
	case raw := <-received:
		result, err := ParseOAuthCallback(raw, authorization)
		return result, redirect, verifier, err
	case <-ctx.Done():
		return nil, "", "", ctx.Err()
	}
}

// HandleOAuthCallback handles only an active one-time app callback route.
func (e *Engine) HandleOAuthCallback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "yeek" || u.Host != "oauth" {
		return false
	}
	base := u.Clone()
	base.RawQuery, base.Fragment = "", ""
	e.oauthMu.Lock()
	callback := e.oauthCallbacks[base.String()]
	e.oauthMu.Unlock()
	return callback != nil && callback(raw)
}
