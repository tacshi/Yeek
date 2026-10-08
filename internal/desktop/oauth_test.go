package desktop

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func oauthApp(t *testing.T, auth engine.Object) (*App, *Draft, *ui.Tester) {
	t.Helper()
	a, e := cookieApp(t)
	m, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "OAuth request", "url": "http://127.0.0.1:1/resource", "authenticationType": "oauth2", "authentication": auth})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	d := a.drafts[a.active]
	d.Tab = 3
	return a, d, ui.NewTester(a.View, 1360, 1000)
}
func TestOAuthEditorGrantControlsAndBooleanPersistence(t *testing.T) {
	a, d, tt := oauthApp(t, engine.Object{"grantType": "authorization_code"})
	if !tt.HasText("Use PKCE") || !tt.HasText("Authorization URL") {
		t.Fatal("authorization controls missing")
	}
	if err := tt.Click("Use PKCE"); err != nil {
		t.Fatal(err)
	}
	a.saveActive()
	saved, err := a.Engine.Store.Get(t.Context(), d.ID)
	if err != nil || o(saved, "authentication")["usePkce"] != false {
		t.Fatal(saved, err)
	}
	if err = tt.Click("OAuth Grant Type"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("Implicit"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Response Type") || tt.HasText("Token URL") {
		t.Fatal("implicit grant fields did not update")
	}
	if err = tt.Click("OAuth Response Type"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("ID Token"); err != nil {
		t.Fatal(err)
	}
	if d.Auth["tokenName"] != "id_token" {
		t.Fatal(d.Auth)
	}
}
func TestOAuthFetchCommitsQueuedFieldsAndShowsInlineFailure(t *testing.T) {
	requests := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		form, _ := url.ParseQuery(string(body))
		requests <- form
		w.WriteHeader(400)
		_ = json.MarshalWrite(w, engine.Object{"error": "invalid_client", "error_description": "Fixture rejects credentials"})
	}))
	defer server.Close()
	a, d, tt := oauthApp(t, engine.Object{"grantType": "client_credentials", "accessTokenUrl": server.URL})
	if err := tt.Click("OAuth Client ID"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() {
		tt.Type("complete-client-id")
		if err := tt.Click("Fetch Token"); err != nil {
			t.Fatal(err)
		}
	})
	if form := <-requests; form.Get("client_id") != "complete-client-id" {
		t.Fatal(form)
	}
	state := a.oauthState(d)
	if state.busy || !strings.Contains(state.error, "invalid_client") || !tt.HasText(state.error) {
		t.Fatal("token error not visible", state.error)
	}
}
func TestOAuthCustomParameterJSONSurvivesDraftSave(t *testing.T) {
	a, d, _ := oauthApp(t, engine.Object{"grantType": "client_credentials", "tokenBodyParams": []any{engine.Object{"name": "resource", "value": "a\"b\\c", "enabled": true}}, "usePkce": true})
	var rows []engine.Object
	if err := json.Unmarshal([]byte(d.Auth["tokenBodyParams"]), &rows); err != nil || len(rows) != 1 || s(rows[0], "value") != "a\"b\\c" {
		t.Fatal(d.Auth, err)
	}
	d.Dirty = true
	a.saveActive()
	saved, _ := a.Engine.Store.Get(t.Context(), d.ID)
	if err := json.Unmarshal([]byte(s(o(saved, "authentication"), "tokenBodyParams")), &rows); err != nil {
		t.Fatal(err)
	}
	if o(saved, "authentication")["usePkce"] != true {
		t.Fatal(saved)
	}
}
