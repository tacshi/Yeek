package engine

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"io"
	"testing"
)

func inflateForTest(t *testing.T, body []byte) string {
	t.Helper()
	r, closer, err := decodeContent(bytes.NewReader(body), "deflate")
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestDeflateKinds(t *testing.T) {
	var zlibbed, raw bytes.Buffer
	zw := zlib.NewWriter(&zlibbed)
	_, _ = zw.Write([]byte("hello zlib"))
	_ = zw.Close()
	fw, _ := flate.NewWriter(&raw, flate.BestCompression)
	_, _ = fw.Write([]byte("hello raw"))
	_ = fw.Close()
	if got := inflateForTest(t, zlibbed.Bytes()); got != "hello zlib" {
		t.Fatalf("zlib: %q", got)
	}
	if got := inflateForTest(t, raw.Bytes()); got != "hello raw" {
		t.Fatalf("raw: %q", got)
	}
	if deflateKind(zlibbed.Bytes()) != deflateZlib || deflateKind(raw.Bytes()) != deflateRaw {
		t.Fatal(deflateKind(zlibbed.Bytes()), deflateKind(raw.Bytes()))
	}
	if deflateKind([]byte{0x88, 0x01}) != deflateRaw {
		t.Fatal("a window over 32 KB is not zlib")
	}
}

func TestDeflateStoredBlockThatLooksLikeZlib(t *testing.T) {
	body := []byte{0x78, 0x01, 0x00, 0xfe, 0xff, 'a', 0x03, 0x00}
	if deflateKind(body) != deflateEither {
		t.Fatalf("kind %d", deflateKind(body))
	}
	if got := inflateForTest(t, body); got != "a" {
		t.Fatalf("%q", got)
	}
}

func TestDeflateLargeAmbiguousRawBody(t *testing.T) {
	body := []byte{0x78, 0x01, 0x00, 0xfe, 0xff, 'a'}
	want := []byte{'a'}
	chunk := bytes.Repeat([]byte{'z'}, 0xffff)
	for range 20 {
		body = append(body, 0x00, 0xff, 0xff, 0x00, 0x00)
		body = append(body, chunk...)
		want = append(want, chunk...)
	}
	body = append(body, 0x03, 0x00)
	if deflateKind(body) != deflateEither || len(body) <= deflateHeldLimit {
		t.Fatal("not a large ambiguous body")
	}
	if got := inflateForTest(t, body); got != string(want) {
		t.Fatalf("read %d bytes, want %d", len(got), len(want))
	}
}
