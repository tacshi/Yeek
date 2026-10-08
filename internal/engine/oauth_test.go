package engine

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func oauthEngine(t *testing.T) (*Engine, OAuthOptions) {
	t.Helper()
	e := testEngine(t)
	e.secrets = memorySecrets{}
	w := saveTest(t, e, Object{"model": "workspace", "name": "OAuth"})
	return e, OAuthOptions{WorkspaceID: str(w, "id"), ContextID: str(w, "id")}
}
func TestOAuthCredentialsAndCustomParameters(t *testing.T) {
	requests := make(chan Object, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			t.Error(err)
		}
		form, err := url.ParseQuery(string(data))
		if err != nil {
			t.Error(err)
		}
		username, password, _ := r.BasicAuth()
		requests <- Object{"user": username, "password": password, "form": form, "header": r.Header.Get("X-Custom")}
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = io.WriteString(w, "access_token=fixture-token&token_type=Bearer&expires_in=3600")
	}))
	defer server.Close()
	e, opts := oauthEngine(t)
	auth := Object{"grantType": "password", "accessTokenUrl": server.URL, "clientId": "client:id", "clientSecret": "with space&", "credentials": "basic", "username": "user", "password": "fixture-password", "scope": "read", "audience": "resource", "tokenHeaders": oauthPairsForTest("X-Custom", "yes"), "tokenBodyParams": oauthPairsForTest("scope", "write", "resource", "one", "resource", "two")}
	token, err := e.AcquireOAuthToken(t.Context(), auth, opts)
	if err != nil || token.Value() != "fixture-token" {
		t.Fatal(token, err)
	}
	request := <-requests
	form := request["form"].(url.Values)
	if str(request, "user") != "client%3Aid" || str(request, "password") != "with+space%26" || form.Get("client_secret") != "" || form.Get("username") != "user" || form.Get("password") != "fixture-password" || form.Get("scope") != "write" || len(form["resource"]) != 2 || str(request, "header") != "yes" {
		t.Fatal(request)
	}
	data, err := e.Export(t.Context(), opts.WorkspaceID, true)
	if err != nil || strings.Contains(string(data), "fixture-token") {
		t.Fatal("token leaked through export", err)
	}
	stored, err := e.Store.List(t.Context(), "oauth_token", opts.WorkspaceID)
	if err != nil || len(stored) != 1 || strings.Contains(jsonString(stored), "fixture-token") {
		t.Fatal("token was not encrypted", err)
	}
	auth["credentials"] = "none"
	if _, err = e.AcquireOAuthToken(t.Context(), auth, opts); err != nil {
		t.Fatal(err)
	}
	request = <-requests
	form = request["form"].(url.Values)
	if str(request, "user") != "" || form.Get("client_id") != "client:id" || form.Has("client_secret") {
		t.Fatal(request)
	}
}

func TestOAuthCacheRefreshAndConcurrentRequests(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		form, _ := url.ParseQuery(string(data))
		if call == 1 {
			_ = json.MarshalWrite(w, Object{"access_token": "first", "expires_in": 0, "refresh_token": "refresh-one"})
		} else {
			if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "refresh-one" || form.Get("resource") != "refresh-resource" || r.Header.Get("X-Custom") != "refresh" {
				t.Error("wrong refresh configuration", form, r.Header)
			}
			_ = json.MarshalWrite(w, Object{"access_token": "second", "expires_in": 3600})
		}
	}))
	defer server.Close()
	e, options := oauthEngine(t)
	auth := Object{"grantType": "client_credentials", "accessTokenUrl": server.URL, "clientId": "fixture", "tokenHeaders": oauthPairsForTest("X-Custom", "initial"), "refreshHeaders": oauthPairsForTest("x-custom", "refresh"), "tokenBodyParams": oauthPairsForTest("resource", "initial"), "refreshBodyParams": oauthPairsForTest("resource", "refresh-resource")}
	first, err := e.AcquireOAuthToken(t.Context(), auth, options)
	if err != nil || !first.Expired(time.Now()) {
		t.Fatal(first, err)
	}
	results := make(chan error, 12)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			token, err := e.AcquireOAuthToken(t.Context(), auth, options)
			if err == nil && token.Value() != "second" {
				err = errors.New("wrong cached token")
			}
			results <- err
		})
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("parallel callers refreshed more than once", calls.Load())
	}
	stored, err := e.StoredOAuthToken(t.Context(), auth, options)
	if err != nil || str(stored.Response, "refresh_token") != "refresh-one" {
		t.Fatal(stored, err)
	}
	other := options
	other.EnvironmentID = "other-environment"
	if token, err := e.StoredOAuthToken(t.Context(), auth, other); err != nil || token != nil {
		t.Fatal("token crossed environment scope", token, err)
	}
}

func TestOAuthDeleteDuringFetchCannotRestoreToken(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_ = json.MarshalWrite(w, Object{"access_token": "late-token", "expires_in": 3600})
	}))
	defer server.Close()
	e, options := oauthEngine(t)
	auth := Object{"grantType": "client_credentials", "accessTokenUrl": server.URL}
	done := make(chan error, 1)
	go func() { _, err := e.AcquireOAuthToken(t.Context(), auth, options); done <- err }()
	<-started
	if err := e.DeleteOAuthToken(t.Context(), auth, options); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "cleared") {
		t.Fatal(err)
	}
	if token, err := e.StoredOAuthToken(t.Context(), auth, options); err != nil || token != nil {
		t.Fatal(token, err)
	}
}

func TestOAuthPKCEAndCallbackValidation(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	auth := Object{"grantType": "authorization_code", "authorizationUrl": "https://provider.test/authorize", "clientId": "client", "pkceCodeVerifier": verifier, "usePkce": true, "authorizationParams": `[{"name":"prompt","value":"consent"},{"name":"redirect_uri","value":"https://wrong.test"}]`}
	challenge, gotVerifier, err := BuildOAuthAuthorization(auth, "http://localhost:9461/callback", "owner")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(challenge.URL)
	if gotVerifier != verifier || u.Query().Get("code_challenge") != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" || u.Query().Get("prompt") != "consent" || u.Query().Get("redirect_uri") != challenge.RedirectURI {
		t.Fatal(challenge)
	}
	callback := challenge.RedirectURI + "?code=valid&state=" + url.QueryEscape(challenge.State)
	if result, err := ParseOAuthCallback(callback, challenge); err != nil || str(result, "code") != "valid" {
		t.Fatal(result, err)
	}
	for _, raw := range []string{strings.Replace(callback, "localhost", "wrong.test", 1), strings.Replace(callback, "/callback?", "/callback/other?", 1), strings.Replace(callback, challenge.State, "wrong", 1), callback + "&code=second"} {
		if _, err := ParseOAuthCallback(raw, challenge); err == nil {
			t.Fatal("invalid callback accepted", raw)
		}
	}
	auth["pkceChallengeMethod"] = "plain"
	challenge, _, err = BuildOAuthAuthorization(auth, "http://localhost:9461/callback", "owner")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(challenge.URL)
	if u.Query().Get("code_challenge") != verifier {
		t.Fatal(u)
	}
	auth["usePkce"] = false
	challenge, gotVerifier, err = BuildOAuthAuthorization(auth, "http://localhost:9461/callback", "owner")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(challenge.URL)
	if gotVerifier != "" || u.Query().Has("code_challenge") {
		t.Fatal(u)
	}
}

func TestOAuthAuthorizationCodeAndImplicitBrowser(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		form, _ = url.ParseQuery(string(data))
		_ = json.MarshalWrite(w, Object{"access_token": "code-token", "expires_in": 3600})
	}))
	defer server.Close()
	e, options := oauthEngine(t)
	var challenge string
	options.Browser = func(ctx context.Context, authorization OAuthAuthorization) (string, error) {
		u, _ := url.Parse(authorization.URL)
		challenge = u.Query().Get("code_challenge")
		return authorization.RedirectURI + "?code=fixture-code&state=" + url.QueryEscape(authorization.State), nil
	}
	auth := Object{"grantType": "authorization_code", "authorizationUrl": "https://provider.test/authorize", "accessTokenUrl": server.URL, "redirectUri": "https://callback.test/return", "scope": "read", "clientId": "fixture", "credentials": "none"}
	token, err := e.AcquireOAuthToken(t.Context(), auth, options)
	if err != nil || token.Value() != "code-token" {
		t.Fatal(token, err)
	}
	hash := sha256.Sum256([]byte(form.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || form.Get("code") != "fixture-code" || form.Get("redirect_uri") != "https://callback.test/return" || form.Has("scope") {
		t.Fatal(form)
	}
	auth["grantType"], auth["responseType"], auth["tokenName"] = "implicit", "id_token", "id_token"
	options.Browser = func(ctx context.Context, authorization OAuthAuthorization) (string, error) {
		id := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"nonce": authorization.Nonce, "exp": time.Now().Add(time.Hour).Unix()})
		signed, err := id.SignedString([]byte("fixture-signing-key"))
		if err != nil {
			return "", err
		}
		return authorization.RedirectURI + "#id_token=" + signed + "&state=" + url.QueryEscape(authorization.State), nil
	}
	token, err = e.AcquireOAuthToken(t.Context(), auth, options)
	if err != nil || token.Value() == "" || token.TokenName != "id_token" || token.ExpiresAt.IsZero() {
		t.Fatal(token, err)
	}
}

func TestOAuthExternalFragmentRelay(t *testing.T) {
	e := testEngine(t)
	auth := Object{"grantType": "implicit", "authorizationUrl": "https://provider.test/authorize", "redirectUri": "http://127.0.0.1:0/callback", "useExternalBrowser": true}
	var relay string
	token, err := e.FetchOAuthToken(t.Context(), auth, func(raw string) error {
		u, _ := url.Parse(raw)
		redirect := u.Query().Get("redirect_uri")
		client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, redirect, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		relay = response.Header.Get("Location")
		if !strings.HasPrefix(relay, "yeek://oauth/callback/") || strings.Contains(relay, "#") {
			return errors.New("invalid fragment relay")
		}
		fragment := "#access_token=implicit-fixture&expires_in=60&state=" + url.QueryEscape(u.Query().Get("state"))
		if !e.HandleOAuthCallback(relay + fragment) {
			return errors.New("active app callback rejected")
		}
		return nil
	})
	if err != nil || str(token, "access_token") != "implicit-fixture" {
		t.Fatal(token, err)
	}
	if e.HandleOAuthCallback(relay + "#access_token=replay&state=wrong") {
		t.Fatal("closed callback accepted")
	}
}

func TestOAuthClientAssertionJWK(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(value *big.Int) string { return base64.RawURLEncoding.EncodeToString(value.Bytes()) }
	jwk := Object{"kty": "RSA", "kid": "fixture-key", "n": encode(private.N), "e": encode(big.NewInt(int64(private.E))), "d": encode(private.D), "p": encode(private.Primes[0]), "q": encode(private.Primes[1])}
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		form, _ := url.ParseQuery(string(data))
		if form.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" || form.Get("client_id") != "client" || form.Has("client_secret") || r.Header.Get("Authorization") != "" {
			t.Error(form, r.Header)
		}
		token, err := jwt.Parse(form.Get("client_assertion"), func(token *jwt.Token) (any, error) { return &private.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(endpoint), jwt.WithIssuer("client"), jwt.WithSubject("client"))
		if err != nil || !token.Valid || token.Header["kid"] != "fixture-key" {
			t.Error(token, err)
		}
		_ = json.MarshalWrite(w, Object{"access_token": "asserted", "expires_in": 300})
	}))
	defer server.Close()
	endpoint = server.URL
	e, opts := oauthEngine(t)
	auth := Object{"grantType": "client_credentials", "accessTokenUrl": endpoint, "clientId": "client", "clientCredentialsMethod": "client_assertion", "clientAssertionAlgorithm": "RS256", "clientAssertionSecret": jsonString(jwk)}
	if token, err := e.AcquireOAuthToken(t.Context(), auth, opts); err != nil || token.Value() != "asserted" {
		t.Fatal(token, err)
	}
	jwk["d"] = "invalid"
	if _, err := buildOAuthClientAssertion(Object{"clientAssertionAlgorithm": "RS256", "clientAssertionSecret": jsonString(jwk)}, endpoint); err == nil {
		t.Fatal("invalid JWK accepted")
	}
}

func TestOAuthAutomaticAuthUsesWorkspaceNetworkAndCache(t *testing.T) {
	var tokens, resources atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokens.Add(1)
			if r.Host == "" || !strings.HasPrefix(r.Host, "oauth.test:") {
				t.Error("DNS override lost logical host", r.Host)
			}
			_ = json.MarshalWrite(w, Object{"access_token": "automatic", "expires_in": 3600})
			return
		}
		resources.Add(1)
		if r.Header.Get("Authorization") != "Bearer automatic" {
			t.Error(r.Header)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	e, opts := oauthEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "id": opts.WorkspaceID, "authenticationType": "oauth2", "authentication": Object{"grantType": "client_credentials", "accessTokenUrl": "http://oauth.test:" + u.Port() + "/token", "clientId": "client"}, "settingDnsOverrides": []any{Object{"hostname": "oauth.test", "ipv4": []any{"127.0.0.1"}, "enabled": true}}})
	for range 2 {
		r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": server.URL + "/resource"})
		if _, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if tokens.Load() != 1 || resources.Load() != 2 {
		t.Fatal(tokens.Load(), resources.Load())
	}
	entries, _ := e.Store.List(t.Context(), "http_response", opts.WorkspaceID)
	if len(entries) != 2 {
		t.Fatal("token exchange leaked into request history", entries)
	}
}

func TestOAuthTemplatesPreservePairQuoting(t *testing.T) {
	e, opts := oauthEngine(t)
	saveTest(t, e, Object{"model": "environment", "workspaceId": opts.WorkspaceID, "parentModel": "workspace", "variables": []any{Object{"name": "value", "value": "a\"b\\c"}}})
	auth, err := e.RenderOAuth(t.Context(), Object{"tokenBodyParams": oauthPairsForTest("resource", "${[ value ]}")}, opts.WorkspaceID, "", "", opts.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := parseOAuthPairs(auth["tokenBodyParams"])
	if err != nil || len(pairs) != 1 || pairs[0].Value != "a\"b\\c" {
		t.Fatal(pairs, err)
	}
}

func oauthPairsForTest(values ...string) string {
	rows := []any{}
	for i := 0; i+1 < len(values); i += 2 {
		rows = append(rows, Object{"name": values[i], "value": values[i+1]})
	}
	return jsonString(rows)
}

func TestOAuthECAssertionAndExpirySelection(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private, err := key.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	public, err := key.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 65 {
		t.Fatal("unexpected P-256 public key length")
	}
	jwk := Object{"kty": "EC", "crv": "P-256", "d": base64.RawURLEncoding.EncodeToString(private), "x": base64.RawURLEncoding.EncodeToString(public[1:33]), "y": base64.RawURLEncoding.EncodeToString(public[33:])}
	signed, err := buildOAuthClientAssertion(Object{"clientId": "client", "clientAssertionAlgorithm": "ES256", "clientAssertionSecret": jsonString(jwk)}, "https://provider.test/token")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(signed, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience("https://provider.test/token"))
	if err != nil || !parsed.Valid {
		t.Fatal(parsed, err)
	}
	expires := time.Now().Add(30 * time.Second).Unix()
	id := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": expires})
	value, err := id.SignedString([]byte("fixture-expiry-key"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := newOAuthToken(Object{"id_token": value, "access_token": "opaque", "expires_in": 3600}, "id_token")
	if err != nil || token.ExpiresAt.Unix() != expires {
		t.Fatal(token, err)
	}
}

func TestOAuthRejectsCrossOriginCredentialRedirect(t *testing.T) {
	var reached atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(204) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	e, opts := oauthEngine(t)
	_, err := e.AcquireOAuthToken(t.Context(), Object{"grantType": "client_credentials", "accessTokenUrl": server.URL, "clientId": "fixture", "clientSecret": "fixture-secret"}, opts)
	if err == nil || !strings.Contains(err.Error(), "another origin") || reached.Load() != 0 {
		t.Fatal(reached.Load(), err)
	}
}

func TestOAuthRefreshFailureFallsBackAndServerErrorsRetainCache(t *testing.T) {
	var count atomic.Int64
	var reject atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		form, _ := url.ParseQuery(string(data))
		if form.Get("grant_type") == "refresh_token" {
			if reject.Load() {
				w.WriteHeader(500)
				_ = json.MarshalWrite(w, Object{"error": "server_error"})
			} else {
				w.WriteHeader(400)
				_ = json.MarshalWrite(w, Object{"error": "invalid_grant"})
			}
			return
		}
		n := count.Add(1)
		_ = json.MarshalWrite(w, Object{"access_token": "fresh-" + strconv.FormatInt(n, 10), "refresh_token": "fixture-refresh", "expires_in": 0})
	}))
	defer server.Close()
	e, opts := oauthEngine(t)
	auth := Object{"grantType": "client_credentials", "accessTokenUrl": server.URL}
	if _, err := e.AcquireOAuthToken(t.Context(), auth, opts); err != nil {
		t.Fatal(err)
	}
	token, err := e.AcquireOAuthToken(t.Context(), auth, opts)
	if err != nil || token.Value() != "fresh-2" {
		t.Fatal(token, err)
	}
	reject.Store(true)
	if _, err = e.AcquireOAuthToken(t.Context(), auth, opts); err == nil {
		t.Fatal("server error hidden")
	}
	stored, err := e.StoredOAuthToken(t.Context(), auth, opts)
	if err != nil || stored == nil || stored.Value() != "fresh-2" {
		t.Fatal("temporary failure discarded the cache", stored, err)
	}
}
