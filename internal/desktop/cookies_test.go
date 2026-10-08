package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func cookieApp(t *testing.T) (*App, *engine.Engine) {
	t.Helper()
	e, err := engine.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	if _, err = e.Save(t.Context(), engine.Object{"model": "workspace", "name": "Cookie tests"}); err != nil {
		t.Fatal(err)
	}
	a, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	a.testMode = true
	t.Cleanup(a.cancel)
	return a, e
}

func TestCookieJarCreationSelectionAndSession(t *testing.T) {
	a, e := cookieApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Cookie jars"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("New Cookie Jar…"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Staging")
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if s(a.activeCookieJar(), "name") != "Staging" {
		t.Fatal("new jar not selected")
	}
	selected := a.cookieJar
	if err := tt.Click("Cookie jars"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Rename Cookie Jar…"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Renamed")
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if s(a.activeCookieJar(), "name") != "Renamed" {
		t.Fatal("jar rename not applied")
	}
	a.persistSession()
	reopened, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.cancel)
	reopened.testMode = true
	if s(reopened.activeCookieJar(), "id") != selected {
		t.Fatal("selected jar not restored")
	}
	other, err := e.Save(t.Context(), engine.Object{"model": "workspace", "name": "Other"})
	if err != nil {
		t.Fatal(err)
	}
	first := a.workspace
	a.switchWorkspace(s(other, "id"))
	otherJar := s(a.activeCookieJar(), "id")
	if otherJar == selected || otherJar == "" {
		t.Fatal("jar leaked across workspaces")
	}
	a.switchWorkspace(first)
	if s(a.activeCookieJar(), "id") != selected {
		t.Fatal("workspace-specific jar selection was lost")
	}
	a.deleteCookieJar(selected)
	if s(a.activeCookieJar(), "id") == selected {
		t.Fatal("deleted active jar stayed selected")
	}
}

func TestCookieNativeEditorFilterAndDelete(t *testing.T) {
	a, e := cookieApp(t)
	jarID := s(a.activeCookieJar(), "id")
	a.openCookies(jarID)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Add Cookie"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("Cookie name") {
		t.Fatal("new cookie did not focus its name")
	}
	tt.Type("session")
	for _, field := range []struct{ name, value string }{{"Cookie value", "token=="}, {"Cookie domain", "EXAMPLE.com"}, {"Cookie path", "/api"}} {
		if err := tt.Click(field.name); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(field.value)
	}
	if err := tt.Click("Include subdomains"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Session cookie"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Cookie expiration"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("2030-01-02T03:04:05Z")
	a.cookies.draft.secure = true
	a.cookies.draft.httpOnly = true
	a.cookies.draft.sameSite = "Strict"
	if err := tt.Click("Save Cookie"); err != nil {
		t.Fatal(err)
	}
	if a.cookies.error != "" {
		t.Fatal(a.cookies.error)
	}
	if !tt.Focused("Cookies") {
		t.Fatal("save did not return keyboard focus to the cookie table")
	}
	jar, err := e.Store.Get(t.Context(), jarID)
	if err != nil {
		t.Fatal(err)
	}
	cookies := oslice(jar, "cookies")
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	cookie := cookies[0]
	expires, _ := engine.CookieExpiry(cookie)
	if s(cookie, "name") != "session" || s(cookie, "value") != "token==" || s(cookie, "path") != "/api" || !b(cookie, "secure") || !b(cookie, "httpOnly") || s(cookie, "sameSite") != "Strict" || expires.UTC().Format(time.RFC3339) != "2030-01-02T03:04:05Z" {
		t.Fatal(cookie)
	}
	if host, sub := engine.CookieDomain(cookie); host != "example.com" || !sub {
		t.Fatal(cookie)
	}
	if err = tt.Click("Filter cookies"); err != nil {
		t.Fatal(err)
	}
	tt.Type("no-match")
	if !tt.HasText("No cookies match this filter") {
		t.Fatal("filter did not update table")
	}
	if err = tt.Click("Clear cookie filter"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("Delete cookie session"); err != nil {
		t.Fatal(err)
	}
	jar, err = e.Store.Get(t.Context(), jarID)
	if err != nil || len(oslice(jar, "cookies")) != 0 {
		t.Fatal(jar, err)
	}
}

func TestCookieInvalidEditStaysOpen(t *testing.T) {
	a, _ := cookieApp(t)
	a.openCookies(s(a.activeCookieJar(), "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Add Cookie"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Save Cookie"); err != nil {
		t.Fatal(err)
	}
	if a.cookies.draft == nil || a.cookies.error == "" {
		t.Fatal("invalid cookie was silently saved")
	}
	if err := tt.Click("Cancel Edit"); err != nil {
		t.Fatal(err)
	}
	if a.cookies.draft != nil || a.cookies.error != "" {
		t.Fatal("cancel did not discard draft")
	}
}

func TestCookieSaveCommitsQueuedTyping(t *testing.T) {
	a, e := cookieApp(t)
	jarID := s(a.activeCookieJar(), "id")
	a.openCookies(jarID)
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Add Cookie"); err != nil {
		t.Fatal(err)
	}
	a.cookies.draft.name = "typed"
	a.cookies.draft.domain = "example.com"
	tt.Frame()
	if err := tt.Click("Cookie value"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() { tt.Type("complete-value"); tt.Key(ui.Cmd, ui.KeyEnter) })
	jar, err := e.Store.Get(t.Context(), jarID)
	if err != nil {
		t.Fatal(err)
	}
	cookies := oslice(jar, "cookies")
	if len(cookies) != 1 || s(cookies[0], "value") != "complete-value" {
		t.Fatalf("save dropped queued input: %#v", cookies)
	}
}

func TestRequestSaveCommitsQueuedTyping(t *testing.T) {
	a, e := cookieApp(t)
	r, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "url": "http://localhost/"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(r)
	a.openRequest(s(r, "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	if err = tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Batch(func() { tt.Type("http://localhost/complete"); tt.Key(ui.Cmd, ui.KeyS) })
	saved, err := e.Store.Get(t.Context(), s(r, "id"))
	if err != nil || s(saved, "url") != "http://localhost/complete" {
		t.Fatalf("save dropped queued request input: %s %v", s(saved, "url"), err)
	}
}

func TestRequestNavigationCommitsQueuedTyping(t *testing.T) {
	a, e := cookieApp(t)
	first, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "First", "url": "http://localhost/initial"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Next", "url": "http://localhost/next"})
	if err != nil {
		t.Fatal(err)
	}
	a.applyModel(first)
	a.applyModel(next)
	a.openRequest(s(first, "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	if err = tt.Click("Request URL"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Batch(func() {
		tt.Type("http://localhost/final-character")
		if err := tt.Click("Next"); err != nil {
			t.Fatal(err)
		}
	})
	saved, err := e.Store.Get(t.Context(), s(first, "id"))
	if err != nil || s(saved, "url") != "http://localhost/final-character" || a.active != s(next, "id") {
		t.Fatal(saved, a.active, err)
	}
}

func TestCookieResponsePaneShowsSentReceivedAndDeletion(t *testing.T) {
	a, p := templateTestApp()
	response := engine.Object{"requestHeaders": []any{engine.Object{"name": "Cookie", "value": "session=abc=def"}}, "headers": []any{engine.Object{"name": "Set-Cookie", "value": "session=; Max-Age=0; Path=/; HttpOnly; SameSite=Lax"}}}
	tt := ui.NewTester(func(c *ui.Context) { a.responseCookies(c, p, response) }, 650, 500)
	for _, text := range []string{"Sent cookies", "Received cookies", "abc=def", "Deleted", "HTTP Only", "Lax"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %s", text, strings.Join(tt.Texts(), ", "))
		}
	}
}
