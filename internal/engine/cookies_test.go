package engine

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/publicsuffix"
)

func storedCookie(name, value, host, path string) Object {
	return Object{"name": name, "value": value, "domain": Object{"HostOnly": host}, "path": path, "expires": "SessionEnd", "secure": false, "httpOnly": false, "sameSite": nil}
}
func cookieURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCookieExpiryCompatibilityAndScope(t *testing.T) {
	target := cookieURL(t, "https://api.example.com/a/b")
	for _, raw := range []string{"1900000000", time.Unix(1900000000, 0).UTC().Format(time.RFC3339)} {
		cookie := storedCookie("session", "value", "api.example.com", "/a")
		cookie["expires"] = Object{"AtUtc": raw}
		cookie["secure"] = true
		normalized, err := NormalizeCookie(cookie)
		if err != nil || str(obj(normalized, "expires"), "AtUtc") != "1900000000" {
			t.Fatal(normalized, err)
		}
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		loadCookies(jar, Object{"cookies": []any{cookie}})
		if len(jar.Cookies(target)) != 1 {
			t.Fatal("valid expiry was not replayed")
		}
		for _, raw := range []string{"http://api.example.com/a/b", "https://sub.api.example.com/a/b", "https://api.example.com/ab"} {
			if len(jar.Cookies(cookieURL(t, raw))) != 0 {
				t.Fatalf("cookie escaped its scope to %s", raw)
			}
		}
	}
	for _, raw := range []string{"1", "invalid", "9223372036854775807"} {
		cookie := storedCookie("expired", "value", "api.example.com", "/")
		cookie["expires"] = Object{"AtUtc": raw}
		jar, _ := cookiejar.New(nil)
		loadCookies(jar, Object{"cookies": []any{cookie}})
		if len(jar.Cookies(target)) != 0 {
			t.Fatalf("expired/invalid cookie replayed: %q", raw)
		}
	}
}

func TestReceivedCookiesCannotPoisonAnotherDomain(t *testing.T) {
	u := cookieURL(t, "https://api.example.com/a/b")
	for _, domain := range []string{"attacker.invalid", "com", "example.com."} {
		cookies := mergeCookies(nil, u, []*http.Cookie{parseCookieFixture(t, "session=poison; Domain="+domain)})
		if len(cookies) != 0 {
			t.Fatalf("persisted rejected domain %q: %v", domain, cookies)
		}
	}
	cookies := mergeCookies(nil, u, []*http.Cookie{parseCookieFixture(t, "token=a=b; Domain=.example.com; SameSite=Strict; HttpOnly")})
	if len(cookies) != 1 {
		t.Fatal(cookies)
	}
	cookie := objects(cookies)[0]
	if str(cookie, "path") != "/a" || str(obj(cookie, "domain"), "Suffix") != "example.com" || str(cookie, "sameSite") != "Strict" {
		t.Fatal(cookie)
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	loadCookies(jar, Object{"cookies": cookies})
	if len(jar.Cookies(cookieURL(t, "https://www.example.com/a/c"))) != 1 {
		t.Fatal("domain cookie did not reach a subdomain")
	}
	if len(jar.Cookies(cookieURL(t, "https://www.example.com/other"))) != 0 {
		t.Fatal("default path was broadened")
	}
	deleted := mergeCookies(cookies, u, []*http.Cookie{parseCookieFixture(t, "token=; Domain=example.com; Path=/a; Max-Age=0")})
	if len(deleted) != 0 {
		t.Fatal("Max-Age deletion left a cookie")
	}
	maxAge := mergeCookies(nil, u, []*http.Cookie{parseCookieFixture(t, "ttl=x; Max-Age=60; Expires=Thu, 01 Jan 1970 00:00:01 GMT")})
	expires, err := CookieExpiry(objects(maxAge)[0])
	if err != nil || time.Until(expires) < 58*time.Second {
		t.Fatal("Max-Age did not override Expires", expires, err)
	}
}

func TestCookieEditsAndConcurrentRequests(t *testing.T) {
	e := testEngine(t)
	workspace := saveTest(t, e, Object{"model": "workspace"})
	wid := str(workspace, "id")
	jars, err := e.Store.List(t.Context(), "cookie_jar", wid)
	if err != nil {
		t.Fatal(err)
	}
	id := str(jars[0], "id")
	a := storedCookie("manual", "original", "127.0.0.1", "/")
	b := storedCookie("removed", "original", "127.0.0.1", "/")
	for _, cookie := range []Object{a, b} {
		if _, err = e.EditCookie(t.Context(), id, "", cookie); err != nil {
			t.Fatal(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "fast"
		if r.URL.Path == "/slow" {
			name = "slow"
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.SetCookie(w, &http.Cookie{Name: name, Value: "received", Path: "/"}) // #nosec G124 -- synthetic loopback cookies verify HTTP persistence.
		w.WriteHeader(204)
	}))
	defer server.Close()
	slow := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": server.URL + "/slow"})
	fast := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": server.URL + "/fast"})
	done := make(chan error, 1)
	go func() { _, err := e.SendHTTP(t.Context(), str(slow, "id"), SendOptions{CookieJarID: id}); done <- err }()
	<-started
	updated := clone(a)
	updated["value"] = "edited"
	if _, err = e.EditCookie(t.Context(), id, CookieKey(a), updated); err != nil {
		unblock()
		t.Fatal(err)
	}
	if _, err = e.DeleteCookie(t.Context(), id, CookieKey(b)); err != nil {
		unblock()
		t.Fatal(err)
	}
	if _, err = e.Save(t.Context(), Object{"model": "cookie_jar", "id": id, "name": "Renamed"}); err != nil {
		unblock()
		t.Fatal(err)
	}
	if _, err = e.SendHTTP(t.Context(), str(fast, "id"), SendOptions{CookieJarID: id}); err != nil {
		unblock()
		t.Fatal(err)
	}
	unblock()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	jar, err := e.Store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, cookie := range objects(array(jar, "cookies")) {
		values[str(cookie, "name")] = str(cookie, "value")
	}
	if values["manual"] != "edited" || values["removed"] != "" || values["fast"] != "received" || values["slow"] != "received" || str(jar, "name") != "Renamed" {
		t.Fatal(jar)
	}
	if err = e.DeleteCookieJar(t.Context(), id); err == nil {
		t.Fatal("last jar was deleted")
	}
}

func TestCookieStoreFlagAndSelectedPreview(t *testing.T) {
	e := testEngine(t)
	workspace := saveTest(t, e, Object{"model": "workspace"})
	wid := str(workspace, "id")
	other := saveTest(t, e, Object{"model": "cookie_jar", "workspaceId": wid, "name": "Other"})
	cookie := storedCookie("selected", "correct", "EXAMPLE.com", "/")
	if _, err := e.EditCookie(t.Context(), str(other, "id"), "", cookie); err != nil {
		t.Fatal(err)
	}
	preview, err := e.PreviewTemplate(t.Context(), `${[ cookie.value(name="selected",domain=".example.COM") ]}`, wid, "", "", "", str(other, "id"))
	if err != nil || preview != "correct" {
		t.Fatal(preview, err)
	}
	_, jar, err := e.prepareCookieJar(t.Context(), wid, str(other, "id"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(cookieURL(t, "https://example.com/"), []*http.Cookie{parseCookieFixture(t, "new=ignored; Max-Age=3600")})
	if len(jar.snapshot()) != 1 || len(jar.Cookies(cookieURL(t, "https://example.com/"))) != 1 {
		t.Fatal("store disabled still accepted response cookies")
	}
	if _, err = e.ClearCookies(t.Context(), str(other, "id")); err != nil {
		t.Fatal(err)
	}
	if err = e.persistCookies(t.Context(), str(other, "id"), jar); err != nil {
		t.Fatal(err)
	}
	current, err := e.Store.Get(t.Context(), str(other, "id"))
	if err != nil || len(array(current, "cookies")) != 0 {
		t.Fatal("unchanged snapshot restored cleared cookies")
	}
}

func TestCookieEditingValidationAndCanonicalIdentity(t *testing.T) {
	cookie := storedCookie(" name ", "value", "EXAMPLE.COM", "/")
	next, err := NormalizeCookie(cookie)
	if err != nil || str(next, "name") != "name" {
		t.Fatal(next, err)
	}
	suffix := clone(next)
	suffix["domain"] = Object{"Suffix": "example.com"}
	if CookieKey(suffix) != CookieKey(next) {
		t.Fatal("host-only flag created a second wire identity")
	}
	for key, value := range map[string]any{"name": "bad;name", "value": "line\nbreak", "path": "relative", "sameSite": "invalid", "domain": Object{"HostOnly": "https://example.com"}, "expires": Object{"AtUtc": strconv.FormatInt(time.Now().Unix(), 10) + "oops"}} {
		invalid := clone(next)
		invalid[key] = value
		if _, err := NormalizeCookie(invalid); err == nil {
			t.Fatal("invalid field accepted", key)
		}
	}
	if !strings.Contains(CookieKey(next), "example.com") {
		t.Fatal("domain was not canonicalized")
	}
}

func TestCookieRedirectHistoryAndPersistence(t *testing.T) {
	e := testEngine(t)
	workspace := saveTest(t, e, Object{"model": "workspace"})
	wid := str(workspace, "id")
	jars, err := e.Store.List(t.Context(), "cookie_jar", wid)
	if err != nil {
		t.Fatal(err)
	}
	jarID := str(jars[0], "id")
	if _, err = e.EditCookie(t.Context(), jarID, "", storedCookie("initial", "stored", "127.0.0.1", "/")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "hop", Value: "redirect", Path: "/", HttpOnly: true}) // #nosec G124 -- loopback HTTP fixture.
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		if cookie, err := r.Cookie("hop"); err != nil || cookie.Value != "redirect" {
			t.Error("redirect did not send its new cookie")
		}
		http.SetCookie(w, &http.Cookie{Name: "hop", MaxAge: -1, Path: "/"}) // #nosec G124 -- loopback cookie deletion fixture.
		w.WriteHeader(204)
	}))
	defer server.Close()
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": wid, "url": server.URL + "/start"})
	response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{CookieJarID: jarID})
	if err != nil {
		t.Fatal(err)
	}
	sent, received := ResponseCookies(response)
	if len(sent) != 2 || len(received) != 1 || !boolean(received[0], "deleted") {
		t.Fatal(sent, received)
	}
	hasCookie := false
	for _, h := range objects(array(response, "requestHeaders")) {
		if strings.EqualFold(str(h, "name"), "Cookie") && strings.Contains(str(h, "value"), "hop=redirect") {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Fatal("actual sent cookies are missing from request history")
	}
	jar, err := e.Store.Get(t.Context(), jarID)
	if err != nil || len(array(jar, "cookies")) != 1 {
		t.Fatal(jar, err)
	}
}

func TestCookieDeletionDuringRequestDoesNotResurrectJar(t *testing.T) {
	e := testEngine(t)
	workspace := saveTest(t, e, Object{"model": "workspace"})
	wid := str(workspace, "id")
	jar := saveTest(t, e, Object{"model": "cookie_jar", "workspaceId": wid, "name": "Temporary"})
	id, recording, err := e.prepareCookieJar(t.Context(), wid, str(jar, "id"), true, true)
	if err != nil {
		t.Fatal(err)
	}
	recording.SetCookies(cookieURL(t, "https://example.com/"), []*http.Cookie{parseCookieFixture(t, "session=fresh")})
	if err = e.DeleteCookieJar(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err = e.persistCookies(t.Context(), id, recording); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Store.Get(t.Context(), id); err == nil {
		t.Fatal("request resurrected a deleted jar")
	}
}

func parseCookieFixture(t *testing.T, raw string) *http.Cookie {
	t.Helper()
	cookie, err := http.ParseSetCookie(raw)
	if err != nil {
		t.Fatal(err)
	}
	return cookie
}
func TestCookieSecureLoopbackException(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		u := cookieURL(t, "http://"+host+"/")
		cookie := storedCookie("secure", "fixture", u.Hostname(), "/")
		cookie["secure"] = true
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		loadCookies(jar, Object{"cookies": []any{cookie}})
		recording := &recordingCookieJar{CookieJar: jar, send: true, store: true}
		if len(recording.Cookies(u)) != 1 {
			t.Fatalf("secure cookie missing from trusted loopback origin %s", host)
		}
		if host == "[::1]" {
			for _, raw := range []string{"http://[::1]:8080/", "http://[0:0:0:0:0:0:0:1]:9090/"} {
				if len(recording.Cookies(cookieURL(t, raw))) != 1 {
					t.Fatal("IPv6 cookie was incorrectly bound to an address spelling or port")
				}
			}
		}
	}
}
