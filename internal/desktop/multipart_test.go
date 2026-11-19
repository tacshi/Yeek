package desktop

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const gmailBatchBoundary = "batch_8Js-Xj0pJXhmxHz6uTLe9MxOxKn8BPHl"

var gmailBatchBody = strings.Join([]string{
	"",
	"--" + gmailBatchBoundary,
	"Content-Type: application/http",
	"Content-ID: response-gtlr_43499",
	"",
	"HTTP/1.1 200 OK",
	"Content-Type: application/json; charset=UTF-8",
	"Vary: Origin",
	"Vary: X-Origin",
	"Vary: Referer",
	"",
	`{"id": "IMPORTANT", "name": "IMPORTANT", "messagesTotal": 23545}`,
	"--" + gmailBatchBoundary + "--",
	"",
}, "\r\n")

func partTexts(parts []multipartPart) []string {
	out := make([]string, len(parts))
	for i, part := range parts {
		out[i] = string(part.data)
	}
	return out
}

func mustParseMultipart(t *testing.T, body, boundary string) multipartResult {
	t.Helper()
	result, err := parseMultipart([]byte(body), boundary)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMultipartGmailBatch(t *testing.T) {
	boundary := boundaryFromContentType("multipart/mixed; boundary=" + gmailBatchBoundary)
	result := mustParseMultipart(t, gmailBatchBody, boundary)
	if !result.complete || len(result.parts) != 1 {
		t.Fatalf("%+v", result)
	}
	part := result.parts[0]
	if part.mediaType != "application/http" || part.contentID != "response-gtlr_43499" {
		t.Fatalf("%+v", part)
	}
	message := parseHTTPMessage(part.data)
	if message == nil || message.status != 200 || message.statusReason != "OK" {
		t.Fatalf("%+v", message)
	}
	var vary []string
	for _, h := range message.headers {
		if h.name == "Vary" {
			vary = append(vary, h.value)
		}
	}
	if !reflect.DeepEqual(vary, []string{"Origin", "X-Origin", "Referer"}) {
		t.Fatal(vary)
	}
	if v, _ := headerValue(message.headers, "content-type"); v != "application/json; charset=UTF-8" {
		t.Fatal(v)
	}
	var body map[string]any
	if err := json.Unmarshal(message.body, &body); err != nil || body["id"] != "IMPORTANT" {
		t.Fatal(string(message.body), err)
	}
}

func TestMultipartPreambleAndEpilogue(t *testing.T) {
	body := strings.Join([]string{"This is a multi-part message in MIME format.", "--b", `Content-Disposition: form-data; name="a"`, "", "one", "--b", `Content-Disposition: form-data; name="b"`, "", "two", "--b--", "This is the epilogue. --b"}, "\r\n")
	result := mustParseMultipart(t, body, "b")
	if !result.complete || result.parts[0].name != "a" || result.parts[1].name != "b" || !reflect.DeepEqual(partTexts(result.parts), []string{"one", "two"}) {
		t.Fatalf("%+v", result)
	}
}

func TestMultipartCutOff(t *testing.T) {
	result := mustParseMultipart(t, strings.Join([]string{"--b", "Content-Type: text/plain", "", "one", "--b", "", "two cut sh"}, "\r\n"), "b")
	if result.complete || !reflect.DeepEqual(partTexts(result.parts), []string{"one", "two cut sh"}) || len(result.parts[1].headers) != 0 {
		t.Fatalf("%+v", result)
	}
	result = mustParseMultipart(t, "--b\r\n\r\nhello\r\n", "b")
	if partTexts(result.parts)[0] != "hello\r\n" {
		t.Fatalf("%q", partTexts(result.parts))
	}
}

func TestMultipartLFOnly(t *testing.T) {
	result := mustParseMultipart(t, strings.Join([]string{"--b", "Content-Type: text/plain", "", "one", "--b--"}, "\n"), "b")
	if !result.complete || string(result.parts[0].data) != "one" {
		t.Fatalf("%+v", result)
	}
}

func TestMultipartNested(t *testing.T) {
	inner := strings.Join([]string{"--inner", "Content-Type: text/plain", "", "deep", "--inner--"}, "\r\n")
	body := strings.Join([]string{"--outer", `Content-Type: multipart/alternative; boundary="inner"`, "", inner, "--outer", "Content-Type: text/plain", "", "shallow", "--outer--"}, "\r\n")
	outer := mustParseMultipart(t, body, "outer")
	if len(outer.parts) != 2 || outer.parts[0].mediaType != "multipart/alternative" {
		t.Fatalf("%+v", outer)
	}
	nested := mustParseMultipart(t, string(outer.parts[0].data), boundaryFromContentType(outer.parts[0].contentType))
	if !nested.complete || string(nested.parts[0].data) != "deep" || string(outer.parts[1].data) != "shallow" {
		t.Fatalf("%+v", nested)
	}
}

func TestMultipartBinaryPart(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0x0d, 0x0a}
	head := strings.Join([]string{"--b", `Content-Disposition: form-data; name="file"; filename="a.bin"`, "Content-Type: application/octet-stream", "", ""}, "\r\n")
	result := mustParseMultipart(t, head+string(png)+"\r\n--b--\r\n", "b")
	part := result.parts[0]
	if part.filename != "a.bin" || !reflect.DeepEqual(part.data, png) || !isProbablyBinary(part.data) {
		t.Fatalf("%+v", part)
	}
}

func TestMultipartBoundaries(t *testing.T) {
	boundary := "a'b(c)+_,-./:=? d"
	if got := boundaryFromContentType(`multipart/form-data; boundary="` + boundary + `"; charset=utf-8`); got != boundary {
		t.Fatalf("%q", got)
	}
	if got := normalizeBoundary(`"` + boundary + `"; charset=utf-8`); got != boundary {
		t.Fatalf("%q", got)
	}
	if got := normalizeBoundary("simple; charset=utf-8"); got != "simple" {
		t.Fatalf("%q", got)
	}
	result := mustParseMultipart(t, strings.Join([]string{"--" + boundary, "", "x", "--" + boundary + "--"}, "\r\n"), `"`+boundary+`"`)
	if !reflect.DeepEqual(partTexts(result.parts), []string{"x"}) {
		t.Fatal(partTexts(result.parts))
	}
	spaced := boundaryFromContentType(`multipart/mixed; boundary=" b"`)
	if spaced != " b" || normalizeBoundary(spaced) != " b" {
		t.Fatalf("%q", spaced)
	}
	if got := partTexts(mustParseMultipart(t, "-- b\r\n\r\nx\r\n-- b--", spaced).parts); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatal(got)
	}
}

func TestMultipartDelimiterLines(t *testing.T) {
	result := mustParseMultipart(t, strings.Join([]string{"--b", "", "x", "--bb", "still x", "--b--"}, "\r\n"), "b")
	if !reflect.DeepEqual(partTexts(result.parts), []string{"x\r\n--bb\r\nstill x"}) {
		t.Fatalf("%q", partTexts(result.parts))
	}
	result = mustParseMultipart(t, strings.Join([]string{"--b", "", "x", "--b-suffix", "--b--suffix", "still x", "--b  ", "", "y", "--b-- ", "epilogue"}, "\r\n"), "b")
	if !result.complete || !reflect.DeepEqual(partTexts(result.parts), []string{"x\r\n--b-suffix\r\n--b--suffix\r\nstill x", "y"}) {
		t.Fatalf("%q", partTexts(result.parts))
	}
}

func TestMultipartDispositionParameters(t *testing.T) {
	result := mustParseMultipart(t, strings.Join([]string{"--b", `Content-Disposition: form-data; name="a;b"; filename*=UTF-8''r%C3%A9sum%C3%A9.txt`, "", "x", "--b--"}, "\r\n"), "b")
	if result.parts[0].name != "a;b" || result.parts[0].filename != "résumé.txt" {
		t.Fatalf("%+v", result.parts[0])
	}
	result = mustParseMultipart(t, strings.Join([]string{"--b", `Content-Disposition: form-data; filename="a; name=wrong"; name="café"`, "", "x", "--b", "Content-Disposition: attachment; filename*=ISO-8859-1''caf%E9.txt", "", "y", "--b--"}, "\r\n"), "b")
	if result.parts[0].name != "café" || result.parts[0].filename != "a; name=wrong" || result.parts[1].filename != "café.txt" {
		t.Fatalf("%+v", result.parts)
	}
}

func TestMultipartMissingBoundary(t *testing.T) {
	if _, err := parseMultipart([]byte("nothing here"), "b"); err == nil || !strings.Contains(err.Error(), `"--b"`) {
		t.Fatal(err)
	}
}

func TestParseHTTPMessage(t *testing.T) {
	message := parseHTTPMessage([]byte("GET /people(1) HTTP/1.1\r\nAccept: application/json\r\n\r\n"))
	if message == nil || message.method != "GET" || message.url != "/people(1)" || message.status != 0 || len(message.body) != 0 {
		t.Fatalf("%+v", message)
	}
	if parseHTTPMessage([]byte("hello world")) != nil {
		t.Fatal("not HTTP")
	}
}

func TestMultipartPartLabels(t *testing.T) {
	parts := []multipartPart{{name: "n"}, {filename: "f"}, {contentID: "c"}, {}}
	var got []string
	for i, part := range parts {
		got = append(got, partLabel(part, i))
	}
	if !reflect.DeepEqual(got, []string{"n", "f", "c", "Part 4"}) {
		t.Fatal(got)
	}
	if partLabel(multipartPart{}, 11) != "Part 12" {
		t.Fatal(partLabel(multipartPart{}, 11))
	}
}

func TestMultipartViewBatchAndCutOff(t *testing.T) {
	_, _, tt := viewerApp(t, "multipart/mixed; boundary="+gmailBatchBoundary, gmailBatchBody, nil)
	for _, want := range []string{"response-gtlr_43499", "200 OK", "Vary"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	_, _, tt = viewerApp(t, "multipart/mixed; boundary=b", "--b\r\nContent-Type: text/plain\r\n\r\none\r\n--b\r\n\r\ntwo cut", nil)
	if !tt.HasText("The body ends without its closing boundary; showing the parts that arrived.") || !tt.HasText("Part 2") {
		t.Fatal(tt.Texts())
	}
}
