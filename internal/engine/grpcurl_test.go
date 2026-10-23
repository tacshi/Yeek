package engine

import (
	"strings"
	"testing"
)

func TestGrpcurl(t *testing.T) {
	lines := func(parts ...string) string { return strings.Join(parts, " \\\n  ") }
	for name, c := range map[string]struct {
		request Object
		files   []string
		want    string
	}{
		"simple":       {Object{"url": "https://api.example.com"}, nil, lines("grpcurl api.example.com")},
		"metadata":     {Object{"url": "https://api.example.com", "headers": []any{Object{"name": "aaa", "value": "AAA"}, Object{"enabled": true, "name": "bbb", "value": "BBB"}, Object{"enabled": false, "name": "disabled", "value": "ddd"}}}, nil, lines("grpcurl -H 'aaa: AAA'", "-H 'bbb: BBB'", "api.example.com")},
		"basic":        {Object{"url": "https://api.example.com", "authenticationType": "basic", "authentication": Object{"username": "user", "password": "pass"}}, nil, lines("grpcurl -H 'Authorization: Basic dXNlcjpwYXNz'", "api.example.com")},
		"apikey":       {Object{"url": "https://api.example.com", "authenticationType": "apikey", "authentication": Object{"key": "X-Token", "value": "tok"}}, nil, lines("grpcurl -H 'X-Token: tok'", "api.example.com")},
		"apikey query": {Object{"url": "https://api.example.com", "authenticationType": "apikey", "authentication": Object{"location": "query", "key": "token", "value": "tok 1"}}, nil, lines("grpcurl", "api.example.com?token=tok%201")},
		"one proto":    {Object{"url": "https://api.example.com"}, []string{"/foo/bar/baz.proto"}, lines("grpcurl -import-path '/foo/bar'", "-import-path '/foo'", "-proto '/foo/bar/baz.proto'", "api.example.com")},
		"same dir":     {Object{"url": "https://api.example.com"}, []string{"/foo/bar/aaa.proto", "/foo/bar/bbb.proto"}, lines("grpcurl -import-path '/foo/bar'", "-import-path '/foo'", "-proto '/foo/bar/aaa.proto'", "-proto '/foo/bar/bbb.proto'", "api.example.com")},
		"mixed":        {Object{"url": "https://api.example.com"}, []string{"/aaa/bbb", "/xxx/yyy", "/foo/bar.proto"}, lines("grpcurl -import-path '/aaa/bbb'", "-import-path '/xxx/yyy'", "-import-path '/foo'", "-import-path '/'", "-proto '/foo/bar.proto'", "api.example.com")},
		"data":         {Object{"url": "https://api.example.com", "message": "{\n  \"foo\": \"bar\",\n  \"baz\": 1\n}"}, []string{"/foo.proto"}, lines("grpcurl -import-path '/'", "-proto '/foo.proto'", "-d '{\n  \"foo\": \"bar\",\n  \"baz\": 1\n}'", "api.example.com")},
	} {
		if got := grpcurl(c.request, c.files); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", name, got, c.want)
		}
	}
	got := grpcurl(Object{"url": "http://api.example.com", "service": "Service", "method": "Method", "message": `{"note":"don't stop"}`, "headers": []any{Object{"name": "x-note", "value": "it's fine"}}}, nil)
	for _, want := range []string{"-plaintext", `'{"note":"don'\''t stop"}'`, `'x-note: it'\''s fine'`, "Service/Method"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}
