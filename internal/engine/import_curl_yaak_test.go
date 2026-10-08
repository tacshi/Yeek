package engine

import (
	"encoding/json/v2"
	"maps"
	"strings"
	"testing"
)

// Ported from Yaak's plugins/importer-curl/tests. Requests compare on the
// fields Yaak's baseRequest sets.

func yaakCurlRequest(fields Object) Object {
	request := Object{"authentication": Object{}, "authenticationType": nil, "body": Object{}, "bodyType": nil, "headers": []any{}, "method": "GET", "name": "", "url": "", "urlParameters": []any{}}
	maps.Copy(request, fields)
	return request
}

func curlProjection(t *testing.T, m Object) string {
	t.Helper()
	keys := []string{"authentication", "authenticationType", "body", "bodyType", "headers", "method", "name", "url", "urlParameters"}
	projected := Object{}
	for _, k := range keys {
		projected[k] = m[k]
	}
	data, err := json.Marshal(projected, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func expectCurl(t *testing.T, command string, want ...Object) {
	t.Helper()
	got, err := ConvertCurl(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if g, w := curlProjection(t, got[i]), curlProjection(t, yaakCurlRequest(want[i])); g != w {
			t.Errorf("request %d\n got: %s\nwant: %s", i, g, w)
		}
	}
}

func h(name, value string) Object { return Object{"name": name, "value": value, "enabled": true} }

var formURLEncoded = h("Content-Type", "application/x-www-form-urlencoded")

func TestCurlImportMatchesYaak(t *testing.T) {
	cases := []struct {
		name, command string
		want          []Object
	}{
		{"basic GET", "curl https://yaak.app", []Object{{"url": "https://yaak.app"}}},
		{"combined short flags", "curl -fsSL https://yaak.app", []Object{{"url": "https://yaak.app"}}},
		{"short cluster ending in a value flag", "curl -sSXPOST https://yaak.app", []Object{{"url": "https://yaak.app", "method": "POST"}}},
		{"explicit URL", "curl --url https://yaak.app", []Object{{"url": "https://yaak.app"}}},
		{"missing URL", "curl -X POST", []Object{{"method": "POST"}}},
		{"URL between", "curl -v https://yaak.app -X POST", []Object{{"url": "https://yaak.app", "method": "POST"}}},
		{"random flags", "curl --random -Z -Y -S --foo https://yaak.app", []Object{{"url": "https://yaak.app"}}},
		{"--request method", "curl --request POST https://yaak.app", []Object{{"url": "https://yaak.app", "method": "POST"}}},
		{"-XPOST method", "curl -XPOST --request POST https://yaak.app", []Object{{"url": "https://yaak.app", "method": "POST"}}},
		{"multiple requests", "curl \\\n  https://yaak.app\necho \"foo\"\ncurl example.com;curl foo.com", []Object{{"url": "https://yaak.app"}, {"url": "example.com"}, {"url": "foo.com"}}},
		{"continuations with trailing whitespace", "curl -X POST https://yaak.app \\ \n  -H 'Content-Type: application/json' \\\t\n  --data '{\"a\":1}' \\ \r\n  -H 'Accept: application/json'", []Object{{"url": "https://yaak.app", "method": "POST", "headers": []any{h("Content-Type", "application/json"), h("Accept", "application/json")}, "bodyType": "application/json", "body": Object{"text": `{"a":1}`}}}},
		{"Windows CRLF", "curl \\\r\n  -X POST \\\r\n  https://yaak.app", []Object{{"url": "https://yaak.app", "method": "POST"}}},
		{"form data", `curl -X POST -F "a=aaa" -F b=bbb -F f=@filepath https://yaak.app`, []Object{{"method": "POST", "url": "https://yaak.app", "headers": []any{h("Content-Type", "multipart/form-data")}, "bodyType": "multipart/form-data", "body": Object{"form": []any{h("a", "aaa"), h("b", "bbb"), Object{"enabled": true, "name": "f", "file": "filepath"}}}}}},
		{"data params as form url-encoded", "curl -d a -d b -d c=ccc https://yaak.app", []Object{{"method": "POST", "url": "https://yaak.app", "bodyType": "application/x-www-form-urlencoded", "headers": []any{formURLEncoded}, "body": Object{"form": []any{h("a", ""), h("b", ""), h("c", "ccc")}}}}},
		{"combined data params", `curl -d 'a=aaa&b=bbb&c' https://yaak.app`, []Object{{"method": "POST", "url": "https://yaak.app", "bodyType": "application/x-www-form-urlencoded", "headers": []any{formURLEncoded}, "body": Object{"form": []any{h("a", "aaa"), h("b", "bbb"), h("c", "")}}}}},
		{"--data-urlencode stays whole", `curl --data-urlencode 'q=a&b=c' https://yaak.app`, []Object{{"method": "POST", "url": "https://yaak.app", "bodyType": "application/x-www-form-urlencoded", "headers": []any{formURLEncoded}, "body": Object{"form": []any{h("q", "a&b=c")}}}}},
		{"invalid percent-encoding", `curl -d 'a=100%' https://yaak.app`, []Object{{"method": "POST", "url": "https://yaak.app", "bodyType": "application/x-www-form-urlencoded", "headers": []any{formURLEncoded}, "body": Object{"form": []any{h("a", "100%")}}}}},
		{"valid escape beside a stray percent", `curl -d 'a=50%25 and 100%' https://yaak.app`, []Object{{"method": "POST", "url": "https://yaak.app", "bodyType": "application/x-www-form-urlencoded", "headers": []any{formURLEncoded}, "body": Object{"form": []any{h("a", "50% and 100%")}}}}},
		{"-G --data-urlencode into the query", `curl -G --data-urlencode 'q=a&b' https://yaak.app`, []Object{{"url": "https://yaak.app", "urlParameters": []any{h("q", "a&b")}}}},
		{"data params as text", "curl -H Content-Type:text/plain -d a -d b -d c=ccc https://yaak.app", []Object{{"method": "POST", "url": "https://yaak.app", "headers": []any{h("Content-Type", "text/plain")}, "bodyType": "text/plain", "body": Object{"text": "a&b&c=ccc"}}}},
		{"post data into URL", "curl -G https://api.stripe.com/v1/payment_links -d limit=3", []Object{{"url": "https://api.stripe.com/v1/payment_links", "urlParameters": []any{h("limit", "3")}}}},
		{"multi-line JSON", "curl -H Content-Type:application/json -d $'{\n  \"foo\":\"bar\"\n}' https://yaak.app", []Object{{"method": "POST", "url": "https://yaak.app", "headers": []any{h("Content-Type", "application/json")}, "bodyType": "application/json", "body": Object{"text": "{\n  \"foo\":\"bar\"\n}"}}}},
		{"multiple headers", "curl -H Foo:bar --header Name -H AAA:bbb -H :ccc https://yaak.app", []Object{{"url": "https://yaak.app", "headers": []any{h("Name", ""), h("Foo", "bar"), h("AAA", "bbb"), h("", "ccc")}}}},
		{"basic auth", "curl --user user:pass https://yaak.app", []Object{{"url": "https://yaak.app", "authenticationType": "basic", "authentication": Object{"username": "user", "password": "pass"}}}},
		{"digest auth", "curl --digest --user user:pass https://yaak.app", []Object{{"url": "https://yaak.app", "authenticationType": "digest", "authentication": Object{"username": "user", "password": "pass"}}}},
		{"Bearer from Authorization header", `curl -H "Authorization: Bearer token123" https://yaak.app`, []Object{{"url": "https://yaak.app", "authenticationType": "bearer", "authentication": Object{"token": "token123", "prefix": "Bearer"}}}},
		{"trims whitespace before Bearer token", `curl -H "Authorization: Bearer    token123" https://yaak.app`, []Object{{"url": "https://yaak.app", "authenticationType": "bearer", "authentication": Object{"token": "token123", "prefix": "Bearer"}}}},
		{"Basic from Authorization header", `curl -H "Authorization: Basic dXNlcjpwYXNzd29yZA==" https://yaak.app`, []Object{{"url": "https://yaak.app", "authenticationType": "basic", "authentication": Object{"username": "user", "password": "password"}}}},
		{"Authorization header beats -u", `curl -u admin:secret -H "Authorization: Bearer token123" https://yaak.app`, []Object{{"url": "https://yaak.app", "authenticationType": "bearer", "authentication": Object{"token": "token123", "prefix": "Bearer"}}}},
		{"case-insensitive Authorization", `curl -H "authorization: bearer lowercaseToken" https://yaak.app`, []Object{{"url": "https://yaak.app", "authenticationType": "bearer", "authentication": Object{"token": "lowercaseToken", "prefix": "Bearer"}}}},
		{"keeps other headers", `curl -H "Authorization: Bearer token123" -H "X-Custom: value" https://yaak.app`, []Object{{"url": "https://yaak.app", "authenticationType": "bearer", "authentication": Object{"token": "token123", "prefix": "Bearer"}, "headers": []any{h("X-Custom", "value")}}}},
		{"invalid base64 Basic stays a header", `curl -H "Authorization: Basic not-valid-base64!!!" https://yaak.app`, []Object{{"url": "https://yaak.app", "headers": []any{h("Authorization", "Basic not-valid-base64!!!")}}}},
		{"cookie as header", `curl --cookie "foo=bar" https://yaak.app`, []Object{{"url": "https://yaak.app", "headers": []any{h("Cookie", "foo=bar")}}}},
		{"query params", `curl "https://yaak.app" --url-query foo=bar --url-query baz=qux`, []Object{{"url": "https://yaak.app", "urlParameters": []any{h("foo", "bar"), h("baz", "qux")}}}},
		{"query params from the URL", `curl "https://yaak.app?foo=bar&baz=a%20a"`, []Object{{"url": "https://yaak.app", "urlParameters": []any{h("foo", "bar"), h("baz", "a a")}}}},
		{"weird body", `curl 'https://yaak.app' -X POST --data-raw 'foo=bar=baz'`, []Object{{"url": "https://yaak.app", "method": "POST", "bodyType": "application/x-www-form-urlencoded", "body": Object{"form": []any{h("foo", "bar=baz")}}, "headers": []any{formURLEncoded}}}},
		{"Unicode escapes", `curl 'https://yaak.app' -H 'Content-Type: application/json' --data-raw $'{"query":"SearchQueryInput\u0021"}' -X POST`, []Object{{"url": "https://yaak.app", "method": "POST", "headers": []any{h("Content-Type", "application/json")}, "bodyType": "application/json", "body": Object{"text": `{"query":"SearchQueryInput!"}`}}}},
		{"GraphQL JSON as GraphQL", `curl 'https://yaak.app/graphql' -H 'Content-Type: application/json' --data-raw $'{"query":"query Search($id: ID\u0021) { node(id: $id) { id } }","variables":{"id":"123"}}'`, []Object{{"url": "https://yaak.app/graphql", "method": "POST", "headers": []any{h("Content-Type", "application/json")}, "bodyType": "graphql", "body": Object{"query": "query Search($id: ID!) { node(id: $id) { id } }", "variables": "{\n  \"id\": \"123\"\n}"}}}},
		{"GraphQL JSON with extensions stays JSON", `curl 'https://yaak.app/graphql' -H 'Content-Type: application/json' --data-raw $'{"query":"query Search($id: ID\u0021) { node(id: $id) { id } }","extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc123"}}}'`, []Object{{"url": "https://yaak.app/graphql", "method": "POST", "headers": []any{h("Content-Type", "application/json")}, "bodyType": "application/json", "body": Object{"text": `{"query":"query Search($id: ID!) { node(id: $id) { id } }","extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc123"}}}`}}}},
		{"multiple escape sequences", `curl 'https://yaak.app' --data-raw $'Line1\nLine2\tTab\u0021Exclamation' -X POST`, []Object{{"url": "https://yaak.app", "method": "POST", "bodyType": "application/x-www-form-urlencoded", "body": Object{"form": []any{h("Line1\nLine2\tTab!Exclamation", "")}}, "headers": []any{formURLEncoded}}}},
		{"Chrome DevTools multipart", "curl 'http://localhost:8080/system' \\\n  -H 'Content-Type: multipart/form-data; boundary=----WebKitFormBoundaryHwsXKi4rKA6P5VBd' \\\n  --data-raw $'------WebKitFormBoundaryHwsXKi4rKA6P5VBd\\r\\nContent-Disposition: form-data; name=\"username\"\\r\\n\\r\\njsgj\\r\\n------WebKitFormBoundaryHwsXKi4rKA6P5VBd\\r\\nContent-Disposition: form-data; name=\"password\"\\r\\n\\r\\n654321\\r\\n------WebKitFormBoundaryHwsXKi4rKA6P5VBd\\r\\nContent-Disposition: form-data; name=\"captcha\"; filename=\"test.xlsx\"\\r\\nContent-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet\\r\\n\\r\\n\\r\\n------WebKitFormBoundaryHwsXKi4rKA6P5VBd--\\r\\n'", []Object{{"url": "http://localhost:8080/system", "method": "POST", "headers": []any{h("Content-Type", "multipart/form-data; boundary=----WebKitFormBoundaryHwsXKi4rKA6P5VBd")}, "bodyType": "multipart/form-data", "body": Object{"form": []any{h("username", "jsgj"), h("password", "654321"), Object{"name": "captcha", "file": "test.xlsx", "enabled": true}}}}}},
		{"JSON with newlines in $quotes", `curl 'https://yaak.app' -H 'Content-Type: application/json' --data-raw $'{\n  "foo": "bar",\n  "baz": "qux"\n}' -X POST`, []Object{{"url": "https://yaak.app", "method": "POST", "headers": []any{h("Content-Type", "application/json")}, "bodyType": "application/json", "body": Object{"text": "{\n  \"foo\": \"bar\",\n  \"baz\": \"qux\"\n}"}}}},
		{"double quotes ending in even backslashes", `curl -d "C:\\" https://yaak.app;curl https://example.com`, []Object{{"url": "https://yaak.app", "method": "POST", "bodyType": "application/x-www-form-urlencoded", "body": Object{"form": []any{h(`C:\`, "")}}, "headers": []any{formURLEncoded}}, {"url": "https://example.com"}}},
		{"$quotes ending in a literal backslash", `curl -d $'C:\\' https://yaak.app;curl https://example.com`, []Object{{"url": "https://yaak.app", "method": "POST", "bodyType": "application/x-www-form-urlencoded", "body": Object{"form": []any{h(`C:\`, "")}}, "headers": []any{formURLEncoded}}, {"url": "https://example.com"}}},
		{"$quoted header with escaped quotes", `curl https://yaak.app -H $'X-Custom: it\'s a test'`, []Object{{"url": "https://yaak.app", "headers": []any{h("X-Custom", "it's a test")}}}},
		{"escaped semicolon outside quotes", `curl https://yaak.app?a=1\;b=2`, []Object{{"url": "https://yaak.app", "urlParameters": []any{h("a", "1;b=2")}}}},
		{"text-only multipart from --data-raw", "curl 'http://example.com/api' \\\n  -H 'Content-Type: multipart/form-data; boundary=----FormBoundary123' \\\n  --data-raw $'------FormBoundary123\\r\\nContent-Disposition: form-data; name=\"field1\"\\r\\n\\r\\nvalue1\\r\\n------FormBoundary123\\r\\nContent-Disposition: form-data; name=\"field2\"\\r\\n\\r\\nvalue2\\r\\n------FormBoundary123--\\r\\n'", []Object{{"url": "http://example.com/api", "method": "POST", "headers": []any{h("Content-Type", "multipart/form-data; boundary=----FormBoundary123")}, "bodyType": "multipart/form-data", "body": Object{"form": []any{h("field1", "value1"), h("field2", "value2")}}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCurl(t, c.command, c.want...) })
	}
}

func TestCurlImportEdgeCasesMatchYaak(t *testing.T) {
	if _, err := ConvertCurl(`curl -X POST -F "a=aaa" -F b=bbb" https://yaak.app`); err == nil {
		t.Error("malformed quotes imported")
	}
	m, err := ParseCurl(`curl --url-query "filter=type=book" --url-query "t=eyJhIjoxfQ==" https://yaak.app`)
	if err != nil {
		t.Fatal(err)
	}
	if got := curlProjection(t, Object{"urlParameters": m["urlParameters"]}); !strings.Contains(got, `"name":"filter","value":"type=book"`) || !strings.Contains(got, `"name":"t","value":"eyJhIjoxfQ=="`) {
		t.Error("= inside --url-query", got)
	}
	m, err = ParseCurl(`curl -F "t=eyJhIjoxfQ==" -F "q=a=b" https://yaak.app`)
	if err != nil {
		t.Fatal(err)
	}
	if form := objects(array(obj(m, "body"), "form")); len(form) != 2 || str(form[0], "value") != "eyJhIjoxfQ==" || str(form[1], "value") != "a=b" {
		t.Error("= inside a form value", form)
	}
	resources, err := ParseImport([]byte("curl https://a.test\ncurl https://b.test"))
	if err != nil || len(resources) != 3 || str(resources[0], "name") != "Curl Import" || str(resources[2], "url") != "https://b.test" || str(resources[1], "workspaceId") != str(resources[0], "id") {
		t.Error("multi-request import", resources, err)
	}
	if _, err = ConvertCurl("wget https://yaak.app"); err == nil {
		t.Error("non-curl text imported")
	}
}

// Ported from Yaak's importer-curl graphql tests.
func TestCurlGraphQLDetectionMatchesYaak(t *testing.T) {
	enc := func(v any) string { data, _ := json.Marshal(v); return string(data) }
	for _, c := range []struct {
		name, mime, text, url string
		want                  Object
	}{
		{"named query without GraphQL URL", "application/json", `{"query":"query Search($id: ID!) { node(id: $id) { id } }","variables":{"id":"123"},"operationName":"Search"}`, "https://api.example.com/search", Object{"query": "query Search($id: ID!) { node(id: $id) { id } }", "variables": "{\n  \"id\": \"123\"\n}", "operationName": "Search"}},
		{"mutation", "application/json", enc(Object{"query": "mutation Save { saveThing { id } }"}), "https://api.example.com", Object{"query": "mutation Save { saveThing { id } }"}},
		{"anonymous selection set", "application/json", enc(Object{"query": "{ viewer { id email } }"}), "https://api.example.com", Object{"query": "{ viewer { id email } }"}},
		{"GraphQL-looking path", "application/json", `{"query":"query Search { viewer { id } }","operationName":"Search"}`, "https://api.example.com/v1/graphql", Object{"query": "query Search { viewer { id } }", "operationName": "Search"}},
		{"incomplete operation", "application/json", `{"query":"query Search","operationName":"Search"}`, "https://api.example.com/graphql", nil},
		{"plain JSON query field", "application/json", enc(Object{"query": "SearchQueryInput!"}), "https://api.example.com/graphql", nil},
		{"variables and operationName alone", "application/json", `{"query":"SearchQueryInput!","variables":{"id":"123"},"operationName":"Search"}`, "https://api.example.com", nil},
		{"string variables", "application/json", `{"query":"query Search($id: ID!) { node(id: $id) { id } }","variables":"{ \"id\": \"123\" }"}`, "https://api.example.com", Object{"query": "query Search($id: ID!) { node(id: $id) { id } }", "variables": `{ "id": "123" }`}},
		{"extra envelope fields", "application/json", `{"query":"query Search($id: ID!) { node(id: $id) { id } }","variables":{"id":"123"},"extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc123"}}}`, "https://api.example.com/graphql", nil},
		{"invalid JSON", "application/json", "not json", "https://api.example.com/graphql", nil},
		{"non-object JSON", "application/json", "[]", "https://api.example.com/graphql", nil},
		{"non-JSON MIME type", "text/plain", enc(Object{"query": "query Search { viewer { id } }"}), "https://api.example.com/graphql", nil},
	} {
		got := parseGraphQLJSONBody(c.mime, c.text, c.url)
		if (got == nil) != (c.want == nil) || got != nil && curlProjection(t, Object{"body": got}) != curlProjection(t, Object{"body": c.want}) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Yeek's Copy as cURL writes flags Yaak's importer doesn't read; they must
// still import back.
func TestCurlImportReadsYeekExportFlags(t *testing.T) {
	m, err := ParseCurl(`curl --globoff -X 'PATCH' --insecure --max-time '2.5' --url 'https://a.test/x' --header 'Accept:' --header 'X-Empty;' --header 'X-Amz-Security-Token: st' --aws-sigv4 'aws:amz:eu-west-1:s3' --user 'ak:sk' -L --json '{"a":1}'`)
	if err != nil {
		t.Fatal(err)
	}
	headers := objects(array(m, "headers"))
	if str(m, "method") != "PATCH" || str(m, "url") != "https://a.test/x" || str(m, "bodyType") != "application/json" || str(obj(m, "body"), "text") != `{"a":1}` {
		t.Fatal(m)
	}
	if len(headers) < 2 || enabled(headers[0]) || str(headers[0], "name") != "Accept" || !enabled(headers[1]) || str(headers[1], "name") != "X-Empty" {
		t.Fatal("disabled and empty headers", headers)
	}
	auth := obj(m, "authentication")
	if str(m, "authenticationType") != "awsv4" || str(auth, "accessKeyId") != "ak" || str(auth, "region") != "eu-west-1" || str(auth, "service") != "s3" || str(auth, "sessionToken") != "st" {
		t.Fatal("aws", auth)
	}
	if number(obj(m, "settingRequestTimeout"), "value") != 2500 || boolean(obj(m, "settingValidateCertificates"), "value") || !boolean(obj(m, "settingFollowRedirects"), "value") {
		t.Fatal("settings", m)
	}
	if m, _ = ParseCurl(`curl -I https://a.test`); str(m, "method") != "HEAD" {
		t.Fatal("-I", m["method"])
	}
	if m, _ = ParseCurl(`curl --ntlm -u 'DOMAIN\user:pw' https://a.test`); str(m, "authenticationType") != "windows" {
		t.Fatal("--ntlm", m["authenticationType"])
	}
	if m, _ = ParseCurl(`curl --data-binary @/tmp/file.bin https://a.test`); str(m, "bodyType") != "binary" || str(obj(m, "body"), "filePath") != "/tmp/file.bin" || str(m, "method") != "POST" {
		t.Fatal("binary", m)
	}
}
