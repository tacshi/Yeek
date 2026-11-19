package desktop

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"

	"yeek/internal/engine"
)

var charsetParam = regexp.MustCompile(`(?i);\s*charset\s*=\s*"?([^";\s]+)"?`)

func contentTypeCharset(contentType string) string {
	m := charsetParam.FindStringSubmatch(contentType)
	if m == nil {
		return ""
	}
	enc, err := htmlindex.Get(m[1])
	if err != nil {
		return ""
	}
	name, _ := htmlindex.Name(enc)
	return name
}

func labelledText(data []byte, contentType string) (string, bool) {
	charset := contentTypeCharset(contentType)
	if charset == "" {
		return "", false
	}
	text, ok := decodeCharset(data, charset)
	return text, ok
}

func storedBodyText(data []byte, contentType string) string {
	if !textMIME.MatchString(mediaTypeOf(contentType)) {
		return string(data)
	}
	if charset := contentTypeCharset(contentType); charset != "" && charset != "utf-8" {
		if text, ok := decodeCharset(data, charset); ok {
			return text
		}
	}
	return string(data)
}

func decodeBodyText(data []byte, contentType string) string {
	if text, ok := labelledText(data, contentType); ok {
		return text
	}
	if utf8.Valid(data) {
		return string(data)
	}
	text, _ := charmap.Windows1252.NewDecoder().Bytes(data)
	return string(text)
}

func requestContentType(response engine.Object) string {
	for _, header := range oslice(response, "requestHeaders") {
		if strings.EqualFold(s(header, "name"), "Content-Type") {
			return s(header, "value")
		}
	}
	return ""
}
