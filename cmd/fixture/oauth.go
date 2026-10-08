package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

func oauthFixture(mux *http.ServeMux) {
	var mu sync.Mutex
	codes := map[string]url.Values{}
	refreshes := map[string]bool{}
	var sequence atomic.Int64
	page := template.Must(template.New("consent").Parse(`<!doctype html><title>OAuth Fixture</title><main style="font:16px system-ui;max-width:32rem;margin:4rem auto"><h1>Authorize fixture account</h1><p>Client: {{.Client}}</p><form method="get">{{range $name,$values:=.Values}}{{range $values}}<input type="hidden" name="{{$name}}" value="{{.}}">{{end}}{{end}}<button name="decision" value="allow">Authorize</button> <button name="decision" value="deny">Deny</button></form></main>`))
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		values := r.URL.Query()
		redirect, err := url.Parse(values.Get("redirect_uri"))
		valid := false
		if err == nil && redirect.User == nil && redirect.Fragment == "" {
			host := redirect.Hostname()
			ip := net.ParseIP(host)
			valid = redirect.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback())
		}
		if !valid {
			http.Error(w, "Missing redirect URI", 400)
			return
		}
		if values.Get("decision") == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = page.Execute(w, struct {
				Client string
				Values url.Values
			}{values.Get("client_id"), values})
			return
		}
		result := url.Values{"state": {values.Get("state")}}
		if values.Get("decision") == "deny" {
			result.Set("error", "access_denied")
		} else if values.Get("response_type") == "code" {
			code := uuid.New().String()
			mu.Lock()
			codes[code] = values
			mu.Unlock()
			result.Set("code", code)
		} else {
			if strings.Contains(values.Get("response_type"), "token") {
				result.Set("access_token", "fixture-token-"+strconv.FormatInt(sequence.Add(1), 10))
				result.Set("token_type", "Bearer")
				result.Set("expires_in", "60")
			}
			if strings.Contains(values.Get("response_type"), "id_token") {
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "fixture-user", "nonce": values.Get("nonce"), "exp": time.Now().Add(time.Minute).Unix()})
				signed, err := token.SignedString([]byte("fixture-only-signing-key"))
				if err != nil {
					http.Error(w, "Token error", 500)
					return
				}
				result.Set("id_token", signed)
			}
		}
		if values.Get("response_type") == "code" {
			query := redirect.Query()
			for key, values := range result {
				query[key] = values
			}
			redirect.RawQuery = query.Encode()
		} else {
			redirect.Fragment = result.Encode()
		}
		http.Redirect(w, r, redirect.String(), http.StatusFound) // #nosec G710 -- this synthetic OAuth provider only redirects to the loopback HTTP callback validated above.
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		form, err := url.ParseQuery(string(data))
		if err != nil {
			w.WriteHeader(400)
			return
		}
		fail := func(code string) { w.WriteHeader(400); _ = json.MarshalWrite(w, map[string]any{"error": code}) }
		client, secret := form.Get("client_id"), form.Get("client_secret")
		if user, password, ok := r.BasicAuth(); ok {
			client, _ = url.QueryUnescape(user)
			secret, _ = url.QueryUnescape(password)
		}
		if client != "fixture-client" || secret != "" && secret != "fixture-secret" {
			fail("invalid_client")
			return
		}
		grant := form.Get("grant_type")
		switch grant {
		case "authorization_code":
			mu.Lock()
			authorization := codes[form.Get("code")]
			delete(codes, form.Get("code"))
			mu.Unlock()
			if authorization == nil || authorization.Get("redirect_uri") != form.Get("redirect_uri") || authorization.Get("client_id") != client {
				fail("invalid_grant")
				return
			}
			if expected := authorization.Get("code_challenge"); expected != "" {
				got := form.Get("code_verifier")
				if authorization.Get("code_challenge_method") == "S256" {
					hash := sha256.Sum256([]byte(got))
					got = base64.RawURLEncoding.EncodeToString(hash[:])
				}
				if expected != got {
					fail("invalid_grant")
					return
				}
			}
		case "refresh_token":
			mu.Lock()
			valid := refreshes[form.Get("refresh_token")]
			delete(refreshes, form.Get("refresh_token"))
			mu.Unlock()
			if !valid {
				fail("invalid_grant")
				return
			}
		case "client_credentials", "password":
		default:
			fail("unsupported_grant_type")
			return
		}
		number := strconv.FormatInt(sequence.Add(1), 10)
		refresh := "fixture-refresh-" + number
		mu.Lock()
		refreshes[refresh] = true
		mu.Unlock()
		expires := 60
		if value, err := strconv.Atoi(r.URL.Query().Get("expires")); err == nil && value >= 0 && value <= 3600 {
			expires = value
		}
		_ = json.MarshalWrite(w, map[string]any{"access_token": "fixture-token-" + number, "refresh_token": refresh, "token_type": "Bearer", "expires_in": expires, "grant_type": grant})
	})
	mux.HandleFunc("/oauth/resource", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fixture-token-") {
			http.Error(w, "Missing fixture token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.MarshalWrite(w, map[string]any{"authorized": true, "method": r.Method})
	})
}
