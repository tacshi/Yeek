package engine

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type cookieTraceTransport struct {
	base    http.RoundTripper
	observe func(*http.Request, *http.Response)
}

func (t cookieTraceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	t.observe(request, response)
	return response, err
}

func cookieHeaderDetails(request http.Header, response http.Header, at time.Time) Object {
	sent, received := []any{}, []any{}
	for _, cookie := range (&http.Request{Header: request}).Cookies() {
		sent = append(sent, Object{"name": cookie.Name, "value": cookie.Value})
	}
	for _, raw := range response.Values("Set-Cookie") {
		cookie, err := http.ParseSetCookie(raw)
		if err != nil {
			received = append(received, Object{"raw": raw, "error": err.Error()})
			continue
		}
		value := Object{"name": cookie.Name, "value": cookie.Value, "domain": cookie.Domain, "path": cookie.Path, "secure": cookie.Secure, "httpOnly": cookie.HttpOnly, "raw": raw}
		if !cookie.Expires.IsZero() {
			value["expires"] = cookie.Expires.UTC().Format(time.RFC3339)
		}
		if cookie.MaxAge != 0 {
			value["maxAge"] = strconv.Itoa(max(cookie.MaxAge, 0))
		}
		value["deleted"] = cookie.MaxAge < 0 || cookie.MaxAge == 0 && !cookie.Expires.IsZero() && !cookie.Expires.After(at)
		switch cookie.SameSite {
		case http.SameSiteLaxMode:
			value["sameSite"] = "Lax"
		case http.SameSiteStrictMode:
			value["sameSite"] = "Strict"
		case http.SameSiteNoneMode:
			value["sameSite"] = "None"
		}
		received = append(received, value)
	}
	return Object{"sent": sent, "received": received}
}

// ResponseCookies includes redirects and authentication exchanges when the
// request recorded them, and can still read responses saved by earlier versions.
func ResponseCookies(response Object) (sent, received []Object) {
	history := objects(array(response, "cookieHistory"))
	if len(history) == 0 {
		requestHeaders, responseHeaders := http.Header{}, http.Header{}
		for _, h := range objects(array(response, "requestHeaders")) {
			requestHeaders.Add(str(h, "name"), str(h, "value"))
		}
		for _, h := range objects(array(response, "headers")) {
			responseHeaders.Add(str(h, "name"), str(h, "value"))
		}
		created, _ := time.Parse("2006-01-02T15:04:05.999999999", str(response, "createdAt"))
		if created.IsZero() {
			created = time.Now()
		}
		history = []Object{cookieHeaderDetails(requestHeaders, responseHeaders, created)}
	}
	sentIndex, receivedIndex := map[string]int{}, map[string]int{}
	upsert := func(values []Object, index map[string]int, value Object, key string) []Object {
		if i, ok := index[key]; ok {
			values[i] = value
			return values
		}
		index[key] = len(values)
		return append(values, value)
	}

	for _, exchange := range history {
		for _, cookie := range objects(array(exchange, "sent")) {
			sent = upsert(sent, sentIndex, cookie, str(cookie, "name")+"\x00"+str(cookie, "value"))
		}
		for _, cookie := range objects(array(exchange, "received")) {
			key := str(cookie, "name") + "\x00" + strings.ToLower(str(cookie, "domain")) + "\x00" + str(cookie, "path")
			if str(cookie, "error") != "" {
				key = str(cookie, "raw")
			}
			received = upsert(received, receivedIndex, cookie, key)
		}
	}
	return
}
