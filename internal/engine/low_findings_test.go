package engine

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"encoding/json/v2"
)

func TestSyncFileCannotTakeOverAnotherWorkspacesModel(t *testing.T) {
	f := newSyncFixture(t, "request.yaml")
	other := saveTest(t, f.e, Object{"model": "workspace", "name": "Other"})
	victim := saveTest(t, f.e, Object{"model": "http_request", "workspaceId": str(other, "id"), "name": "Victim", "url": "https://victim.test"})
	f.writeModel(filepath.Join(f.dir, "intruder.yaml"), Object{"model": "http_request", "id": str(victim, "id"), "workspaceId": f.workspace, "name": "Intruder", "url": "https://intruder.test"})
	if _, err := f.e.PlanSync(t.Context(), f.workspace, f.dir); err == nil || !strings.Contains(err.Error(), str(other, "id")) {
		t.Fatal("plan accepted a file that takes over another workspace's request:", err)
	}
	stored, err := f.e.Store.Get(t.Context(), str(victim, "id"))
	if err != nil || str(stored, "workspaceId") != str(other, "id") || str(stored, "url") != "https://victim.test" {
		t.Fatal(stored, err)
	}
}

func TestOAuthWaitersRetryWhenTheFirstCallerCancels(t *testing.T) {
	var calls atomic.Int64
	started, finished := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-r.Context().Done():
			case <-finished:
			}
			return
		}
		_ = json.MarshalWrite(w, Object{"access_token": "fresh", "expires_in": 3600})
	}))
	defer server.Close()
	defer close(finished)
	e, options := oauthEngine(t)
	auth := Object{"grantType": "client_credentials", "accessTokenUrl": server.URL, "clientId": "fixture"}
	first, cancel := context.WithCancel(t.Context())
	leader := make(chan error, 1)
	go func() { _, err := e.AcquireOAuthToken(first, auth, options); leader <- err }()
	<-started
	waiter := make(chan error, 1)
	go func() {
		token, err := e.AcquireOAuthToken(t.Context(), auth, options)
		if err == nil && token.Value() != "fresh" {
			err = errors.New("wrong token " + token.Value())
		}
		waiter <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-waiter; err != nil {
		t.Fatal("a waiting caller failed with the first caller's cancellation:", err)
	}
}

func TestOpenAPISharedExamplesStayUnmerged(t *testing.T) {
	models := openAPIModels(t, `{"openapi":"3.1.0","info":{"title":"Examples"},"paths":{
"/a":{"post":{"summary":"Extended","requestBody":{"content":{"application/json":{"schema":{"oneOf":[{"$ref":"#/components/schemas/Base"}],"properties":{"extra":{"type":"string"}}}}}}}},
"/b":{"post":{"summary":"Plain","requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Base"}}}}}}},
"components":{"schemas":{"Base":{"type":"object","example":{"name":"base"}}}}}`)
	plain := importedNamed(t, models, "http_request", "Plain")
	if text := str(obj(plain, "body"), "text"); strings.Contains(text, "extra") || !strings.Contains(text, "base") {
		t.Fatalf("plain body %q", text)
	}
	extended := importedNamed(t, models, "http_request", "Extended")
	if text := str(obj(extended, "body"), "text"); !strings.Contains(text, "extra") {
		t.Fatalf("extended body %q", text)
	}
}
