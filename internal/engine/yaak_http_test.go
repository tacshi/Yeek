package engine

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"slices"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Ported from yaak-http/src/types.rs build_url tests. Yaak's empty and
// repeated fragment cases are left out: fragments never reach the wire and
// Go's url.URL can't represent them verbatim.
func TestBuildURLMatchesYaak(t *testing.T) {
	param := func(name, value string) Object { return Object{"name": name, "value": value, "enabled": true} }
	for _, c := range []struct {
		name, url, want string
		params          []any
	}{
		{"no params", "https://example.com/api", "https://example.com/api", nil},
		{"params keep table order", "https://example.com/api", "https://example.com/api?foo=bar&baz=qux", []any{param("foo", "bar"), param("baz", "qux")}},
		{"disabled params", "https://example.com/api", "https://example.com/api?enabled=value", []any{Object{"name": "disabled", "value": "value", "enabled": false}, param("enabled", "value")}},
		{"existing query", "https://example.com/api?existing=param", "https://example.com/api?existing=param&new=value", []any{param("new", "value")}},
		{"existing query keeps its order", "https://example.com/api?b=2&a=1", "https://example.com/api?b=2&a=1&c=3", []any{param("c", "3")}},
		{"empty existing query", "https://example.com/api?", "https://example.com/api?new=value", []any{param("new", "value")}},
		{"special chars", "https://example.com/api", "https://example.com/api?special%20chars%21%40%23=value%20with%20spaces%20%26%20symbols", []any{param("special chars!@#", "value with spaces & symbols")}},
		{"adds protocol", "example.com/api", "http://example.com/api?foo=bar", []any{param("foo", "bar")}},
		{"adds https for dev domain", "example.dev/api", "https://example.dev/api?foo=bar", []any{param("foo", "bar")}},
		{"adds https for app domain", "yaak.app", "https://yaak.app", nil},
		{"adds https for page domain", "docs.example.page/x", "https://docs.example.page/x", nil},
		{"protocol-relative", "//example.dev/api", "https://example.dev/api", nil},
		{"fragment", "https://example.com/api#section", "https://example.com/api?foo=bar#section", []any{param("foo", "bar")}},
		{"trims leading whitespace", " https://example.com/api", "https://example.com/api", nil},
		{"trims trailing whitespace", "https://example.com/api ", "https://example.com/api", nil},
		{"trims whitespace with params", " https://example.com/api ", "https://example.com/api?foo=bar", []any{param("foo", "bar")}},
		{"trims whitespace without scheme", "  example.com/api\t", "http://example.com/api", nil},
		{"existing query and fragment", "https://yaak.app?foo=bar#some-hash", "https://yaak.app?foo=bar&baz=qux#some-hash", []any{param("baz", "qux")}},
		{"empty query and fragment", "https://example.com/api?#section", "https://example.com/api?foo=bar#section", []any{param("foo", "bar")}},
		{"fragment with special chars", "https://example.com#section/with/slashes?and=fake&query", "https://example.com?real=param#section/with/slashes?and=fake&query", []any{param("real", "param")}},
		{"unencoded space in typed query", "https://example.com/api?q=a b", "https://example.com/api?q=a%20b", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			u, err := buildURL(Object{"url": c.url, "urlParameters": c.params})
			if err != nil || u.String() != c.want {
				t.Fatalf("got %v, %v; want %s", u, err, c.want)
			}
		})
	}
}

func TestBuildURLGraphQLGetReplacesOperationName(t *testing.T) {
	u, err := buildURL(Object{
		"url": "https://example.com/graphql", "method": "GET", "bodyType": "graphql",
		"body":          Object{"query": "query Foo { foo } query Bar { bar }", "operationName": "Bar"},
		"urlParameters": []any{Object{"name": "operationName", "value": "Foo", "enabled": true}},
	})
	want := "https://example.com/graphql?query=query%20Foo%20%7B%20foo%20%7D%20query%20Bar%20%7B%20bar%20%7D&operationName=Bar"
	if err != nil || u.String() != want {
		t.Fatalf("got %v, %v", u, err)
	}
}

// Ported from yaak-http/src/decompress.rs.
func TestDecodeContentMatchesYaak(t *testing.T) {
	original := []byte("hello world, this is a test of compression")
	compress := func(w io.WriteCloser, buf *bytes.Buffer) []byte {
		_, _ = w.Write(original)
		_ = w.Close()
		return buf.Bytes()
	}
	var gz, zl, raw, br, zs bytes.Buffer
	flateWriter, _ := flate.NewWriter(&raw, flate.DefaultCompression)
	zstdWriter, _ := zstd.NewWriter(&zs)
	encoded := map[string][]byte{
		"gzip":     compress(gzip.NewWriter(&gz), &gz),
		"x-gzip":   gz.Bytes(),
		"GZIP":     gz.Bytes(),
		"deflate":  compress(flateWriter, &raw),
		"br":       compress(brotli.NewWriter(&br), &br),
		"zstd":     compress(zstdWriter, &zs),
		" zstd ":   zs.Bytes(),
		"":         original,
		"identity": original,
		"unknown":  original,
	}
	zlibWriter := zlib.NewWriter(&zl)
	encoded["deflate (zlib)"] = compress(zlibWriter, &zl)
	for encoding, body := range encoded {
		header := encoding
		if encoding == "deflate (zlib)" {
			header = "deflate"
		}
		reader, closeDecoder, err := decodeContent(bytes.NewReader(body), header)
		if err != nil {
			t.Fatal(encoding, err)
		}
		got, err := io.ReadAll(reader)
		closeDecoder()
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("%s: got %q, %v", encoding, got, err)
		}
	}
	if _, _, err := decodeContent(bytes.NewReader([]byte("not gzip")), "gzip"); err == nil {
		t.Fatal("corrupt gzip should fail")
	}
}

// Ported from yaak-models/src/queries/duplicate_name.rs.
func TestDuplicateNamesMatchYaak(t *testing.T) {
	for name, want := range map[string]string{"Foo": "Foo Copy", "Foo Copy": "Foo Copy 2", "Foo Copy 2": "Foo Copy 3", "Foo Copy 99": "Foo Copy 100", "Copy": "Copy Copy", "Foo Copy x": "Foo Copy x Copy"} {
		if got := nextCopyName(name); got != want {
			t.Errorf("nextCopyName(%q) = %q, want %q", name, got, want)
		}
	}
	siblings := []string{"Foo", "Foo Copy", ""}
	for name, want := range map[string]string{"Foo": "Foo Copy 2", "Bar": "Bar", "": ""} {
		if got := conflictFreeName(name, siblings); got != want {
			t.Errorf("conflictFreeName(%q) = %q, want %q", name, got, want)
		}
	}

	e := testEngine(t)
	w := saveTest(t, e, Object{"model": "workspace", "name": "W"})
	folder := saveTest(t, e, Object{"model": "folder", "workspaceId": str(w, "id"), "name": "Folder"})
	request := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "name": "Foo"})
	// Same name in another folder and as another model type don't count.
	saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "folderId": str(folder, "id"), "name": "Foo Copy"})
	saveTest(t, e, Object{"model": "grpc_request", "workspaceId": str(w, "id"), "name": "Foo Copy"})
	names := []string{}
	for range 3 {
		id, err := e.Duplicate(t.Context(), str(request, "id"))
		if err != nil {
			t.Fatal(err)
		}
		m, _ := e.Store.Get(t.Context(), id)
		names = append(names, str(m, "name"))
	}
	if want := []string{"Foo Copy", "Foo Copy 2", "Foo Copy 3"}; !slices.Equal(names, want) {
		t.Fatalf("got %q, want %q", names, want)
	}
	copyID, _ := e.Duplicate(t.Context(), str(request, "id"))
	again, _ := e.Store.Get(t.Context(), copyID)
	id, _ := e.Duplicate(t.Context(), str(again, "id"))
	m, _ := e.Store.Get(t.Context(), id)
	if str(m, "name") != "Foo Copy 5" {
		t.Fatalf("duplicating %q gave %q", str(again, "name"), str(m, "name"))
	}
	unnamed := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": "https://example.com"})
	id, _ = e.Duplicate(t.Context(), str(unnamed, "id"))
	if m, _ = e.Store.Get(t.Context(), id); str(m, "name") != "" {
		t.Fatalf("unnamed copy got %q", str(m, "name"))
	}
}
