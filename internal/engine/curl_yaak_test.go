package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type curlCapture struct {
	Method, URI, Body string
	Header            http.Header
}

// TestCurlExportMatchesYaakScenarios ports the cases in Yaak's
// plugins/action-copy-curl tests. Yeek's command format differs on purpose
// (one line, --url, --data-raw, --form-string, request settings), so each
// case runs the copied command through sh and curl and checks what the
// server receives.
func TestCurlExportMatchesYaakScenarios(t *testing.T) {
	received := make(chan curlCapture, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- curlCapture{Method: r.Method, URI: r.RequestURI, Body: string(body), Header: r.Header}
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	row := func(name, value string, on bool) Object { return Object{"name": name, "value": value, "enabled": on} }
	for _, c := range []struct {
		name  string
		model Object
		check func(t *testing.T, got curlCapture)
	}{
		{"GET with params", Object{"url": server.URL, "urlParameters": []any{row("a", "aaa", true), row("b", "bbb", true), row("c", "ccc", false)}}, func(t *testing.T, got curlCapture) {
			if got.Method != "GET" || got.URI != "/?a=aaa&b=bbb" {
				t.Fatal(got.Method, got.URI)
			}
		}},
		{"GET with params and hash", Object{"url": server.URL + "/path#section", "urlParameters": []any{row("a", "aaa", true)}}, func(t *testing.T, got curlCapture) {
			if got.URI != "/path?a=aaa" {
				t.Fatal(got.URI)
			}
		}},
		{"POST url form data", Object{"method": "POST", "url": server.URL, "bodyType": "application/x-www-form-urlencoded", "body": Object{"form": []any{row("a", "aaa", true), row("b", "bbb", true), row("c", "ccc", false)}}}, func(t *testing.T, got curlCapture) {
			if got.Method != "POST" || got.Body != "a=aaa&b=bbb" || got.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Fatal(got)
			}
		}},
		{"POST GraphQL data", Object{"method": "POST", "url": server.URL, "bodyType": "graphql", "body": Object{"query": "{foo,bar}", "variables": `{"a": "aaa", "b": "bbb"}`}}, func(t *testing.T, got curlCapture) {
			if got.Body != `{"query":"{foo,bar}","variables":{"a":"aaa","b":"bbb"}}` {
				t.Fatal(got.Body)
			}
		}},
		{"POST GraphQL operation name", Object{"method": "POST", "url": server.URL, "bodyType": "graphql", "body": Object{"query": "query Foo { foo } query Bar { bar }", "operationName": "Foo"}}, func(t *testing.T, got curlCapture) {
			if got.Body != `{"query":"query Foo { foo } query Bar { bar }","operationName":"Foo"}` {
				t.Fatal(got.Body)
			}
		}},
		{"POST GraphQL no variables", Object{"method": "POST", "url": server.URL, "bodyType": "graphql", "body": Object{"query": "{foo,bar}"}}, func(t *testing.T, got curlCapture) {
			if got.Body != `{"query":"{foo,bar}"}` {
				t.Fatal(got.Body)
			}
		}},
		{"JSON body with apostrophe", Object{"method": "POST", "url": server.URL + "/it's", "bodyType": "application/json", "body": Object{"text": `{"note":"don't stop"}`}, "headers": []any{row("X-Note", "it's fine", true)}}, func(t *testing.T, got curlCapture) {
			if got.URI != "/it's" || got.Header.Get("X-Note") != "it's fine" || got.Body != `{"note":"don't stop"}` || got.Header.Get("Content-Type") != "application/json" {
				t.Fatal(got)
			}
		}},
		{"multi-line JSON body", Object{"method": "POST", "url": server.URL, "bodyType": "application/json", "body": Object{"text": "{\"foo\":\"bar\",\n\"baz\":\"qux\"}"}}, func(t *testing.T, got curlCapture) {
			if got.Body != "{\"foo\":\"bar\",\n\"baz\":\"qux\"}" {
				t.Fatalf("%q", got.Body)
			}
		}},
		{"headers", Object{"url": server.URL, "headers": []any{row("a", "aaa", true), row("b", "bbb", true), row("c", "ccc", false)}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("a") != "aaa" || got.Header.Get("b") != "bbb" || got.Header.Values("c") != nil {
				t.Fatal(got.Header)
			}
		}},
		{"basic auth", Object{"url": server.URL, "authenticationType": "basic", "authentication": Object{"username": "user", "password": "pass"}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
				t.Fatal(got.Header)
			}
		}},
		{"broken basic auth", Object{"url": server.URL, "authenticationType": "basic", "authentication": Object{}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("Authorization") != "Basic Og==" {
				t.Fatal(got.Header)
			}
		}},
		{"bearer auth", Object{"url": server.URL, "authenticationType": "bearer", "authentication": Object{"token": "tok"}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("Authorization") != "Bearer tok" {
				t.Fatal(got.Header)
			}
		}},
		{"bearer custom prefix", Object{"url": server.URL, "authenticationType": "bearer", "authentication": Object{"token": "abc123", "prefix": "Token"}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("Authorization") != "Token abc123" {
				t.Fatal(got.Header)
			}
		}},
		{"bearer empty prefix", Object{"url": server.URL, "authenticationType": "bearer", "authentication": Object{"token": "xyz789", "prefix": ""}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("Authorization") != "xyz789" {
				t.Fatal(got.Header)
			}
		}},
		{"broken bearer auth", Object{"url": server.URL, "authenticationType": "bearer", "authentication": Object{"username": "user", "password": "pass"}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("Authorization") != "Bearer" {
				t.Fatal(got.Header)
			}
		}},
		{"API key header", Object{"url": server.URL, "authenticationType": "apikey", "authentication": Object{"location": "header", "key": "X-Header", "value": "my-token"}}, func(t *testing.T, got curlCapture) {
			if got.Header.Get("X-Header") != "my-token" {
				t.Fatal(got.Header)
			}
		}},
		{"API key header default", Object{"url": server.URL, "authenticationType": "apikey", "authentication": Object{"location": "header"}}, func(t *testing.T, got curlCapture) {
			if _, ok := got.Header["X-Api-Key"]; !ok {
				t.Fatal(got.Header)
			}
		}},
		{"API key query with existing and params", Object{"url": server.URL + "?hi=there", "urlParameters": []any{row("param", "hi", true)}, "authenticationType": "apikey", "authentication": Object{"location": "query", "key": "foo", "value": "bar"}}, func(t *testing.T, got curlCapture) {
			if got.URI != "/?hi=there&param=hi&foo=bar" {
				t.Fatal(got.URI)
			}
		}},
		{"API key query", Object{"url": server.URL, "authenticationType": "apikey", "authentication": Object{"location": "query", "key": "foo", "value": "bar-baz"}}, func(t *testing.T, got curlCapture) {
			if got.URI != "/?foo=bar-baz" {
				t.Fatal(got.URI)
			}
		}},
		{"API key query default", Object{"url": server.URL + "?foo=bar&baz=qux", "authenticationType": "apikey", "authentication": Object{"location": "query"}}, func(t *testing.T, got curlCapture) {
			if got.URI != "/?foo=bar&baz=qux&token=" {
				t.Fatal(got.URI)
			}
		}},
		{"stale body data", Object{"url": server.URL, "bodyType": nil, "body": Object{"text": "ignore me"}}, func(t *testing.T, got curlCapture) {
			if got.Body != "" || got.Method != "GET" {
				t.Fatal(got)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			model := Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "GET"}
			for k, v := range c.model {
				model[k] = v
			}
			r := saveTest(t, e, model)
			command, err := e.Curl(t.Context(), str(r, "id"), "")
			if err != nil {
				t.Fatal(err)
			}
			runExportedCurl(t, command)
			c.check(t, <-received)
		})
	}
}

// Yaak's digest and AWS cases check the flags, which curl then acts on.
func TestCurlExportDigestAndAWSFlags(t *testing.T) {
	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace"})
	for _, c := range []struct {
		auth Object
		kind string
		want []string
	}{
		{Object{"username": "user", "password": "pass"}, "digest", []string{"--digest --user 'user:pass'"}},
		{Object{"accessKeyId": "ak", "secretAccessKey": "sk", "sessionToken": "", "region": "us-east-1", "service": "s3"}, "awsv4", []string{"--aws-sigv4 'aws:amz:us-east-1:s3'", "--user 'ak:sk'"}},
		{Object{"accessKeyId": "ak", "secretAccessKey": "sk", "sessionToken": "st", "region": "us-east-1", "service": "s3"}, "awsv4", []string{"--aws-sigv4 'aws:amz:us-east-1:s3'", "--user 'ak:sk'", "--header 'X-Amz-Security-Token: st'"}},
	} {
		r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "method": "GET", "url": "https://yaak.app", "authenticationType": c.kind, "authentication": c.auth})
		command, err := e.Curl(t.Context(), str(r, "id"), "")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range c.want {
			if !strings.Contains(command, want) {
				t.Errorf("%s: %s lacks %s", c.kind, command, want)
			}
		}
		if c.auth["sessionToken"] == "" && strings.Contains(command, "X-Amz-Security-Token") {
			t.Errorf("empty session token exported: %s", command)
		}
	}
}
