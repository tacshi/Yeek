package desktop

import (
	"testing"

	"yeek/internal/engine"
)

func TestContentTypeCharset(t *testing.T) {
	for contentType, want := range map[string]string{
		"text/plain; charset=ISO-8859-1":    "windows-1252",
		`text/html; charset="Shift_JIS"`:    "shift_jis",
		"application/json; charset=utf-8":   "utf-8",
		"application/json":                  "",
		"text/plain; charset=not-a-charset": "",
	} {
		if got := contentTypeCharset(contentType); got != want {
			t.Errorf("%q: %q, want %q", contentType, got, want)
		}
	}
}

func TestStoredBodyText(t *testing.T) {
	if got := storedBodyText([]byte("caf\xe9"), "text/plain; charset=iso-8859-1"); got != "café" {
		t.Fatalf("%q", got)
	}
	if got := storedBodyText([]byte{0x82, 0xa0}, "text/plain; charset=shift_jis"); got != "あ" {
		t.Fatalf("%q", got)
	}
	png := string([]byte{0x89, 'P', 'N', 'G', 0xff})
	if got := storedBodyText([]byte(png), "image/png"); got != png {
		t.Fatal("unlabelled bytes changed")
	}
	if got := storedBodyText([]byte(png), "image/png; charset=iso-8859-1"); got != png {
		t.Fatal("a binary type is never decoded, whatever its label says")
	}
	if got := storedBodyText([]byte("caf\xe9"), "application/json; charset=iso-8859-1"); got != "café" {
		t.Fatalf("%q", got)
	}
	if got := storedBodyText([]byte("caf\xe9"), "text/plain; charset=utf-8"); got != "caf\xe9" {
		t.Fatal("utf-8 bodies are kept as they are")
	}
}

func TestDecodeBodyText(t *testing.T) {
	if got := decodeBodyText([]byte("caf\xe9 \x80"), ""); got != "café €" {
		t.Fatalf("%q", got)
	}
	if got := decodeBodyText([]byte("café"), ""); got != "café" {
		t.Fatalf("%q", got)
	}
	if got := decodeBodyText([]byte("caf\xe9"), "application/x-www-form-urlencoded; charset=iso-8859-1"); got != "café" {
		t.Fatalf("%q", got)
	}
	response := engine.Object{"requestHeaders": []any{engine.Object{"name": "content-type", "value": "text/plain; charset=latin1"}}}
	if got := requestContentType(response); got != "text/plain; charset=latin1" {
		t.Fatalf("%q", got)
	}
}

func TestLabelledResponseShowsAsText(t *testing.T) {
	_, _, tt := viewerApp(t, "text/plain; charset=iso-8859-1", storedBodyText([]byte("caf\xe9 au lait"), "text/plain; charset=iso-8859-1"), nil)
	if !tt.HasText("Response body") || tt.HasText("Save to File") {
		t.Fatal(tt.Texts())
	}
	_, _, tt = viewerApp(t, "text/plain; charset=iso-8859-1", "caf\xe9 au lait", nil)
	if !tt.HasText("Save to File") {
		t.Fatal("undecoded bytes should still show as binary", tt.Texts())
	}
}
