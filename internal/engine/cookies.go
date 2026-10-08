package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// CookieDomain returns the hostname and whether the cookie includes subdomains.
func CookieDomain(cookie Object) (string, bool) {
	domain := obj(cookie, "domain")
	if host := str(domain, "HostOnly"); host != "" {
		return host, false
	}
	return str(domain, "Suffix"), true
}

// CookieExpiry reads Yaak's Unix-second representation and earlier Yeek date
// strings. A zero time is a session cookie; invalid expiry data is an error.
func CookieExpiry(cookie Object) (time.Time, error) {
	if cookie["expires"] == nil || cookie["expires"] == "SessionEnd" {
		return time.Time{}, nil
	}
	raw := str(obj(cookie, "expires"), "AtUtc")
	if raw == "" {
		return time.Time{}, errors.New("cookie expiration is invalid")
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		value := time.Unix(seconds, 0).UTC()
		if value.Year() < 1601 || value.Year() > 9999 {
			return time.Time{}, errors.New("cookie expiration is outside the supported date range")
		}
		return value, nil
	}
	if value, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		if value.Year() < 1601 {
			return time.Time{}, errors.New("cookie expiration is outside the supported date range")
		}
		return value, nil
	}
	return time.Time{}, errors.New("cookie expiration must be Unix seconds or an RFC 3339 date")
}

func cookieHost(host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.String(), nil
	}
	if host == "" || strings.ContainsAny(host, "/:?#@\\ \t\r\n") {
		return "", errors.New("enter a hostname without a scheme, port, or path")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", errors.New("cookie domain is not a valid hostname")
	}
	return strings.ToLower(ascii), nil
}

// An explicit port makes the standard jar canonicalize bracketed IPv6 hosts
// consistently. Cookies themselves are independent of the request's port.
func cookieJarURL(u *url.URL) *url.URL {
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return u
	}
	result := u.Clone()
	result.Host = net.JoinHostPort(ip.String(), "0")
	return result
}

// CookieKey follows cookie identity: name, canonical domain, and path. Host-only
// and domain cookies for that same tuple replace one another on the wire.
func CookieKey(cookie Object) string {
	host, _ := CookieDomain(cookie)
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), ".")
	if canonical, err := cookieHost(host); err == nil {
		host = canonical
	}
	path := str(cookie, "path")
	if path == "" {
		path = "/"
	}
	return jsonString([]string{str(cookie, "name"), host, path})
}

func NormalizeCookie(cookie Object) (Object, error) {
	next := clone(cookie)
	name := strings.TrimSpace(str(cookie, "name"))
	if name == "" {
		return nil, errors.New("enter a cookie name")
	}
	value := str(cookie, "value")
	host, subdomains := CookieDomain(cookie)
	if strings.HasPrefix(host, ".") {
		subdomains = true
		host = strings.TrimLeft(host, ".")
	}
	host, err := cookieHost(host)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		subdomains = false
	}
	path := strings.TrimSpace(str(cookie, "path"))
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("cookie path must start with /")
	}
	check := &http.Cookie{Name: name, Value: value, Path: path} // #nosec G124 -- validates syntax only; this cookie is never sent or issued.
	if err := check.Valid(); err != nil {
		return nil, fmt.Errorf("invalid cookie: %w", err)
	}
	if subdomains {
		check.Domain = host
		if err := check.Valid(); err != nil {
			return nil, fmt.Errorf("invalid cookie domain: %w", err)
		}
		if suffix, icann := publicsuffix.PublicSuffix(host); icann && suffix == host {
			return nil, errors.New("cookie domain cannot be a public suffix")
		}
	}
	expires, err := CookieExpiry(cookie)
	if err != nil {
		return nil, err
	}
	next["name"], next["value"], next["path"] = name, value, path
	if subdomains {
		next["domain"] = Object{"Suffix": host}
	} else {
		next["domain"] = Object{"HostOnly": host}
	}
	next["expires"] = "SessionEnd"
	if !expires.IsZero() {
		next["expires"] = Object{"AtUtc": strconv.FormatInt(expires.Unix(), 10)}
	}
	next["secure"], next["httpOnly"] = boolean(cookie, "secure"), boolean(cookie, "httpOnly")
	sameSite := str(cookie, "sameSite")
	if !slices.Contains([]string{"", "Lax", "Strict", "None"}, sameSite) {
		return nil, errors.New("same-site must be Lax, Strict, None, or unspecified")
	}
	next["sameSite"] = nil
	if sameSite != "" {
		next["sameSite"] = sameSite
	}
	return next, nil
}

func (e *Engine) EditCookie(ctx context.Context, jarID, oldKey string, cookie Object) (Object, error) {
	next, err := NormalizeCookie(cookie)
	if err != nil {
		return nil, err
	}
	return e.changeCookies(ctx, jarID, func(current []any) []any {
		key := CookieKey(next)
		current = slices.DeleteFunc(current, func(v any) bool { c, _ := v.(map[string]any); return CookieKey(c) == oldKey || CookieKey(c) == key })
		return append(current, next)
	})
}
func (e *Engine) DeleteCookie(ctx context.Context, jarID, key string) (Object, error) {
	return e.changeCookies(ctx, jarID, func(current []any) []any {
		return slices.DeleteFunc(current, func(v any) bool { cookie, _ := v.(map[string]any); return CookieKey(cookie) == key })
	})
}
func (e *Engine) ClearCookies(ctx context.Context, jarID string) (Object, error) {
	return e.changeCookies(ctx, jarID, func([]any) []any { return []any{} })
}
func (e *Engine) changeCookies(ctx context.Context, jarID string, apply func([]any) []any) (Object, error) {
	var result Object
	err := e.Store.Write(ctx, Object{"type": "background"}, func(tx *modelTx) error {
		jar, err := tx.get(ctx, jarID)
		if err != nil {
			return err
		}
		if str(jar, "model") != "cookie_jar" {
			return errors.New("select a cookie jar")
		}
		jar["cookies"] = apply(slices.Clone(array(jar, "cookies")))
		result, err = tx.upsert(ctx, jar)
		return err
	})
	return result, err
}
func (e *Engine) DeleteCookieJar(ctx context.Context, id string) error {
	return e.Store.Write(ctx, Object{"type": "background"}, func(tx *modelTx) error {
		jar, err := tx.get(ctx, id)
		if err != nil {
			return err
		}
		if str(jar, "model") != "cookie_jar" {
			return errors.New("select a cookie jar")
		}
		all, err := tx.all(ctx)
		if err != nil {
			return err
		}
		count := 0
		for _, m := range all {
			if str(m, "model") == "cookie_jar" && str(m, "workspaceId") == str(jar, "workspaceId") {
				count++
			}
		}
		if count < 2 {
			return errors.New("create another cookie jar before deleting the last one")
		}
		return tx.delete(ctx, id)
	})
}

func (e *Engine) selectedCookieJar(ctx context.Context, workspace, id string) (Object, error) {
	if id == "" {
		jars, err := e.Store.List(ctx, "cookie_jar", workspace)
		if err != nil {
			return nil, err
		}
		if len(jars) == 0 {
			return nil, nil
		}
		id = str(jars[0], "id")
	}
	jar, err := e.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if str(jar, "model") != "cookie_jar" || str(jar, "workspaceId") != workspace {
		return nil, errors.New("select a cookie jar in this workspace")
	}
	return jar, nil
}
func (e *Engine) prepareCookieJar(ctx context.Context, workspace, id string, send, store bool) (string, *recordingCookieJar, error) {
	model, err := e.selectedCookieJar(ctx, workspace, id)
	if err != nil {
		return "", nil, err
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	loadCookies(jar, model)
	return str(model, "id"), &recordingCookieJar{CookieJar: jar, send: send, store: store, cookies: slices.Clone(array(model, "cookies"))}, nil
}
func loadCookies(jar *cookiejar.Jar, model Object) {
	for _, cookie := range objects(array(model, "cookies")) {
		host, subdomains := CookieDomain(cookie)
		host, err := cookieHost(strings.TrimPrefix(host, "."))
		if err != nil {
			continue
		}
		expires, err := CookieExpiry(cookie)
		if err != nil || !expires.IsZero() && !expires.After(time.Now()) {
			continue
		}
		scheme := "http"
		if boolean(cookie, "secure") {
			scheme = "https"
		}
		value := &http.Cookie{Name: str(cookie, "name"), Value: str(cookie, "value"), Path: str(cookie, "path"), Expires: expires, Secure: boolean(cookie, "secure"), HttpOnly: boolean(cookie, "httpOnly")} // #nosec G124 -- an API client replays the cookie's configured attributes, rather than issuing authentication cookies.
		if subdomains {
			value.Domain = host
		}
		if value.Valid() != nil {
			continue
		}
		jar.SetCookies(&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, "0"), Path: "/"}, []*http.Cookie{value})
	}
}
func cookieDefaultPath(u *url.URL) string {
	path, _, ok := strings.CutLast(u.Path, "/")
	if !ok || path == "" {
		return "/"
	}
	return path
}

type cookieChange struct {
	key   string
	value Object
}

// Cookies rejected by the standard jar must never enter persistent storage;
// otherwise a later request could replay an attacker's cookie on another host.
func receivedCookie(u *url.URL, cookie *http.Cookie, now time.Time) (cookieChange, bool) {
	u = cookieJarURL(u)
	probe, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	copy := *cookie // #nosec G124 -- this in-memory origin acceptance probe is never sent.
	copy.Path = "/"
	copy.Secure = false
	copy.Expires = time.Time{}
	copy.MaxAge = 0
	probe.SetCookies(u, []*http.Cookie{&copy})
	if len(probe.Cookies(u)) == 0 {
		return cookieChange{}, false
	}
	host, err := cookieHost(u.Hostname())
	if err != nil {
		return cookieChange{}, false
	}
	domain := Object{"HostOnly": host}
	if cookie.Domain != "" {
		d := strings.ToLower(strings.TrimPrefix(cookie.Domain, "."))
		if net.ParseIP(d) == nil && publicsuffix.List.PublicSuffix(d) != d {
			domain = Object{"Suffix": d}
		}
	}
	path := cookie.Path
	if !strings.HasPrefix(path, "/") {
		path = cookieDefaultPath(u)
	}
	value := Object{"name": cookie.Name, "value": cookie.Value, "domain": domain, "path": path, "secure": cookie.Secure, "httpOnly": cookie.HttpOnly, "sameSite": nil, "expires": "SessionEnd"}
	expires := cookie.Expires
	if cookie.MaxAge > 0 {
		// Saturate at the maximum HTTP cookie date instead of overflowing a duration.
		seconds := min(int64(cookie.MaxAge), int64(253402300799)-now.Unix())
		expires = time.Unix(now.Unix()+seconds, 0)
	}
	if !expires.IsZero() {
		value["expires"] = Object{"AtUtc": strconv.FormatInt(expires.Unix(), 10)}
	}
	switch cookie.SameSite {
	case http.SameSiteLaxMode:
		value["sameSite"] = "Lax"
	case http.SameSiteStrictMode:
		value["sameSite"] = "Strict"
	case http.SameSiteNoneMode:
		value["sameSite"] = "None"
	}
	change := cookieChange{key: CookieKey(value), value: value}
	if cookie.MaxAge < 0 || !expires.IsZero() && !expires.After(now) {
		change.value = nil
	}
	return change, true
}
func applyCookieChanges(current []any, changes []cookieChange) []any {
	result := slices.Clone(current)
	for _, change := range changes {
		result = slices.DeleteFunc(result, func(v any) bool { cookie, _ := v.(map[string]any); return CookieKey(cookie) == change.key })
		if change.value != nil {
			result = append(result, change.value)
		}
	}
	return result
}
func mergeCookies(existing []any, u *url.URL, cookies []*http.Cookie) []any {
	changes := []cookieChange{}
	for _, cookie := range cookies {
		if change, ok := receivedCookie(u, cookie, time.Now()); ok {
			changes = append(changes, change)
		}
	}
	return applyCookieChanges(existing, changes)
}

type recordingCookieJar struct {
	http.CookieJar
	mu          sync.Mutex
	send, store bool
	cookies     []any
	changes     []cookieChange
}

func (j *recordingCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if !j.send {
		return nil
	}
	return j.CookieJar.Cookies(cookieJarURL(u))
}
func (j *recordingCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if !j.store {
		return
	}
	u = cookieJarURL(u)
	j.CookieJar.SetCookies(u, cookies)
	j.mu.Lock()
	defer j.mu.Unlock()
	changes := []cookieChange{}
	for _, cookie := range cookies {
		if change, ok := receivedCookie(u, cookie, time.Now()); ok {
			changes = append(changes, change)
		}
	}
	j.cookies = applyCookieChanges(j.cookies, changes)
	j.changes = append(j.changes, changes...)
}
func (j *recordingCookieJar) snapshot() []any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.cookies)
}
func (e *Engine) persistCookies(ctx context.Context, id string, jar *recordingCookieJar) error {
	if id == "" || jar == nil {
		return nil
	}
	jar.mu.Lock()
	changes := slices.Clone(jar.changes)
	jar.mu.Unlock()
	if len(changes) == 0 {
		return nil
	}
	_, err := e.changeCookies(ctx, id, func(current []any) []any { return applyCookieChanges(current, changes) })
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} // A deleted jar must not be recreated by a pending send.
	if err == nil {
		jar.mu.Lock()
		jar.changes = jar.changes[min(len(changes), len(jar.changes)):]
		jar.mu.Unlock()
	}
	return err
}
