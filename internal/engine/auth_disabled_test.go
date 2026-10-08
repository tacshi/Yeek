package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authentication.disabled is Yaak's Enabled / Disabled / "Enabled when..."
// switch: true skips auth, and a template skips it when it renders empty.
func TestAuthenticationDisabled(t *testing.T) {
	var requests []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Header.Clone())
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	env := saveTest(t, e, Object{"model": "environment", "workspaceId": str(w, "id"), "parentModel": "workspace", "parentId": str(w, "id"), "variables": []any{
		Object{"name": "on", "value": "yes", "enabled": true},
		Object{"name": "off", "value": "", "enabled": true},
	}})
	send := func(t *testing.T, kind string, auth Object) http.Header {
		t.Helper()
		requests = nil
		var authType any = kind
		if kind == "" {
			authType = nil // inherit
		}
		r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "GET", "url": server.URL, "authenticationType": authType, "authentication": auth})
		if _, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{EnvironmentID: str(env, "id")}); err != nil {
			t.Fatal(err)
		}
		if len(requests) != 1 {
			t.Fatalf("server saw %d requests", len(requests))
		}
		return requests[0]
	}
	basic := func(disabled any) Object {
		auth := Object{"username": "user", "password": "pass"}
		if disabled != nil {
			auth["disabled"] = disabled
		}
		return auth
	}
	for _, c := range []struct {
		name     string
		disabled any
		applied  bool
	}{
		{"absent", nil, true},
		{"enabled", false, true},
		{"disabled", true, false},
		{"enabled when non-empty", "${[ on ]}", true},
		{"enabled when empty", "${[ off ]}", false},
		{"empty condition", "", false},
		{"condition that fails to render", "${[ missing ]}", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := send(t, "basic", basic(c.disabled)).Get("Authorization"); (got != "") != c.applied {
				t.Fatalf("Authorization = %q", got)
			}
		})
	}
	t.Run("disabled digest sends no probe", func(t *testing.T) {
		if got := send(t, "digest", basic(true)).Get("Authorization"); got != "" {
			t.Fatal(got)
		}
	})
	t.Run("disabled API key", func(t *testing.T) {
		if got := send(t, "apikey", Object{"key": "X-Key", "value": "secret", "disabled": true}); got.Get("X-Key") != "" {
			t.Fatal(got)
		}
	})
	t.Run("inherited from the workspace", func(t *testing.T) {
		w["authenticationType"], w["authentication"] = "bearer", Object{"token": "tok", "disabled": "${[ off ]}"}
		saveTest(t, e, w)
		defer func() {
			w["authenticationType"], w["authentication"] = nil, Object{}
			saveTest(t, e, w)
		}()
		if got := send(t, "", nil).Get("Authorization"); got != "" {
			t.Fatal(got)
		}
		obj(w, "authentication")["disabled"] = false
		saveTest(t, e, w)
		if got := send(t, "", nil).Get("Authorization"); got != "Bearer tok" {
			t.Fatal(got)
		}
	})
	t.Run("rendered model keeps only disabled", func(t *testing.T) {
		r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": server.URL, "authenticationType": "basic", "authentication": basic(true)})
		resolved, err := e.resolve(t.Context(), r, str(env, "id"))
		if err != nil {
			t.Fatal(err)
		}
		if auth := obj(resolved.Model, "authentication"); len(auth) != 1 || auth["disabled"] != true {
			t.Fatal(auth)
		}
		r["authentication"] = basic("${[ on ]}")
		saveTest(t, e, r)
		resolved, _ = e.resolve(t.Context(), r, str(env, "id"))
		if auth := obj(resolved.Model, "authentication"); auth["disabled"] != false || auth["username"] != "user" {
			t.Fatal(auth)
		}
	})
}

// Ported from Yaak's action-copy-curl "Basic auth disabled", plus the
// "Enabled when..." form.
func TestCurlExportSkipsDisabledAuth(t *testing.T) {
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	for _, disabled := range []any{true, ""} {
		r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "GET", "url": server.URL, "authenticationType": "basic", "authentication": Object{"disabled": disabled, "username": "user", "password": "pass"}})
		command, err := e.Curl(t.Context(), str(r, "id"), "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(command, "user:pass") {
			t.Fatalf("disabled=%v exported credentials: %s", disabled, command)
		}
		runExportedCurl(t, command)
		if got := (<-received).Get("Authorization"); got != "" {
			t.Fatalf("disabled=%v sent %q", disabled, got)
		}
	}
}
