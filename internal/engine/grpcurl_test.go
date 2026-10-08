package engine

import (
	"strings"
	"testing"
)

// The cases of Yaak's action-copy-grpcurl tests.
func TestGrpcurlMatchesYaak(t *testing.T) {
	lines := func(parts ...string) string { return strings.Join(parts, " \\\n  ") }
	for name, c := range map[string]struct {
		request Object
		files   []string
		want    string
	}{
		"simple":       {Object{"url": "https://yaak.app"}, nil, lines("grpcurl yaak.app")},
		"metadata":     {Object{"url": "https://yaak.app", "headers": []any{Object{"name": "aaa", "value": "AAA"}, Object{"enabled": true, "name": "bbb", "value": "BBB"}, Object{"enabled": false, "name": "disabled", "value": "ddd"}}}, nil, lines("grpcurl -H 'aaa: AAA'", "-H 'bbb: BBB'", "yaak.app")},
		"basic":        {Object{"url": "https://yaak.app", "authenticationType": "basic", "authentication": Object{"username": "user", "password": "pass"}}, nil, lines("grpcurl -H 'Authorization: Basic dXNlcjpwYXNz'", "yaak.app")},
		"apikey":       {Object{"url": "https://yaak.app", "authenticationType": "apikey", "authentication": Object{"key": "X-Token", "value": "tok"}}, nil, lines("grpcurl -H 'X-Token: tok'", "yaak.app")},
		"apikey query": {Object{"url": "https://yaak.app", "authenticationType": "apikey", "authentication": Object{"location": "query", "key": "token", "value": "tok 1"}}, nil, lines("grpcurl", "yaak.app?token=tok%201")},
		"one proto":    {Object{"url": "https://yaak.app"}, []string{"/foo/bar/baz.proto"}, lines("grpcurl -import-path '/foo/bar'", "-import-path '/foo'", "-proto '/foo/bar/baz.proto'", "yaak.app")},
		"same dir":     {Object{"url": "https://yaak.app"}, []string{"/foo/bar/aaa.proto", "/foo/bar/bbb.proto"}, lines("grpcurl -import-path '/foo/bar'", "-import-path '/foo'", "-proto '/foo/bar/aaa.proto'", "-proto '/foo/bar/bbb.proto'", "yaak.app")},
		"mixed":        {Object{"url": "https://yaak.app"}, []string{"/aaa/bbb", "/xxx/yyy", "/foo/bar.proto"}, lines("grpcurl -import-path '/aaa/bbb'", "-import-path '/xxx/yyy'", "-import-path '/foo'", "-import-path '/'", "-proto '/foo/bar.proto'", "yaak.app")},
		"data":         {Object{"url": "https://yaak.app", "message": "{\n  \"foo\": \"bar\",\n  \"baz\": 1\n}"}, []string{"/foo.proto"}, lines("grpcurl -import-path '/'", "-proto '/foo.proto'", "-d '{\n  \"foo\": \"bar\",\n  \"baz\": 1\n}'", "yaak.app")},
	} {
		if got := grpcurl(c.request, c.files); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", name, got, c.want)
		}
	}
	got := grpcurl(Object{"url": "http://yaak.app", "service": "Service", "method": "Method", "message": `{"note":"don't stop"}`, "headers": []any{Object{"name": "x-note", "value": "it's fine"}}}, nil)
	for _, want := range []string{"-plaintext", `'{"note":"don'\''t stop"}'`, `'x-note: it'\''s fine'`, "Service/Method"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}
