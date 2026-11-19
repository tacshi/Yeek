package desktop

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"

	"yeek/locales"
)

type multipartHeader struct{ name, value string }

type multipartPart struct {
	headers                   []multipartHeader
	contentType, mediaType    string
	name, filename, contentID string
	data                      []byte
}

type multipartResult struct {
	parts    []multipartPart
	complete bool
}

type httpMessage struct {
	startLine, statusReason, method, url string
	status                               int
	headers                              []multipartHeader
	body                                 []byte
}

func latin1(b []byte) string {
	text, _ := charmap.ISO8859_1.NewDecoder().Bytes(b)
	return string(text)
}

func boundaryFromContentType(contentType string) string {
	return headerParams(contentType)["boundary"]
}

var quotedBoundary = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"`)
var escapedChar = regexp.MustCompile(`\\(.)`)

func normalizeBoundary(boundary string) string {
	value := boundary
	if trimmed := strings.TrimLeft(value, " \t\r\n"); strings.HasPrefix(trimmed, `"`) {
		if m := quotedBoundary.FindStringSubmatch(trimmed); m != nil {
			value = escapedChar.ReplaceAllString(m[1], "$1")
		} else {
			value = trimmed[1:]
		}
	} else {
		value, _, _ = strings.Cut(value, ";")
		value = strings.TrimRight(value, " \t\r\n")
	}
	return value
}

func parseMultipart(data []byte, rawBoundary string) (multipartResult, error) {
	boundary := normalizeBoundary(rawBoundary)
	if boundary == "" {
		boundary = rawBoundary
	}
	delimiter := []byte("--" + boundary)
	start := findDelimiter(data, delimiter, 0)
	if start < 0 {
		return multipartResult{}, errors.New(locales.NoDelimiterFound("--" + boundary))
	}
	var parts []multipartPart
	for {
		pos := start + len(delimiter)
		if byteAt(data, pos) == '-' && byteAt(data, pos+1) == '-' {
			return multipartResult{parts: parts, complete: true}, nil
		}
		for byteAt(data, pos) == ' ' || byteAt(data, pos) == '\t' {
			pos++
		}
		if byteAt(data, pos) == '\r' {
			pos++
		}
		if byteAt(data, pos) == '\n' {
			pos++
		}
		if pos >= len(data) {
			return multipartResult{parts: parts}, nil
		}
		next := findDelimiter(data, delimiter, pos)
		if next < 0 {
			parts = append(parts, parsePart(data[pos:]))
			return multipartResult{parts: parts}, nil
		}
		end := pos
		if next != pos {
			end = lineStart(data, next)
		}
		parts = append(parts, parsePart(data[pos:end]))
		start = next
	}
}

func byteAt(data []byte, i int) int {
	if i < 0 || i >= len(data) {
		return -1
	}
	return int(data[i])
}

func findDelimiter(data, delimiter []byte, from int) int {
	for i := from; i+len(delimiter) <= len(data); {
		j := bytes.Index(data[i:], delimiter)
		if j < 0 {
			return -1
		}
		i += j
		if (i == 0 || data[i-1] == '\n') && endsDelimiter(data, i+len(delimiter)) {
			return i
		}
		i++
	}
	return -1
}

func endsDelimiter(data []byte, pos int) bool {
	if byteAt(data, pos) == '-' && byteAt(data, pos+1) == '-' {
		pos += 2
	}
	for byteAt(data, pos) == ' ' || byteAt(data, pos) == '\t' {
		pos++
	}
	if pos >= len(data) || data[pos] == '\n' {
		return true
	}
	return data[pos] == '\r' && (pos+1 >= len(data) || data[pos+1] == '\n')
}

func lineStart(data []byte, pos int) int {
	if pos > 0 && data[pos-1] == '\n' {
		if pos > 1 && data[pos-2] == '\r' {
			return pos - 2
		}
		return pos - 1
	}
	return pos
}

func parsePart(b []byte) multipartPart {
	headers, body := splitHeaders(b)
	part := multipartPart{headers: headers, data: body}
	part.contentType, _ = headerValue(headers, "content-type")
	part.mediaType = mediaTypeOf(part.contentType)
	disposition, _ := headerValue(headers, "content-disposition")
	params := headerParams(disposition)
	part.name = params["name"]
	if extended, ok := params["filename*"]; ok {
		part.filename = decodeExtendedValue(extended)
	} else {
		part.filename = params["filename"]
	}
	if id, ok := headerValue(headers, "content-id"); ok {
		if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") && len(id) >= 2 {
			id = id[1 : len(id)-1]
		}
		part.contentID = id
	}
	return part
}

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+\\s*:")

func splitHeaders(b []byte) ([]multipartHeader, []byte) {
	pos := 0
	var lines []string
	for {
		lf := bytes.IndexByte(b[pos:], '\n')
		lineEnd := len(b)
		if lf >= 0 {
			lf += pos
			lineEnd = lf
		}
		contentEnd := lineEnd
		if lineEnd > pos && b[lineEnd-1] == '\r' {
			contentEnd = lineEnd - 1
		}
		if contentEnd == pos {
			if lf < 0 {
				pos = len(b)
			} else {
				pos = lf + 1
			}
			break
		}
		line := decodeHeaderLine(b[pos:contentEnd])
		if len(lines) == 0 && !headerName.MatchString(line) {
			return nil, b
		}
		lines = append(lines, line)
		if lf < 0 {
			pos = len(b)
			break
		}
		pos = lf + 1
	}
	var headers []multipartHeader
	for _, line := range lines {
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && len(headers) > 0 {
			last := &headers[len(headers)-1]
			last.value += " " + strings.TrimSpace(line)
			continue
		}
		colon := strings.Index(line, ":")
		if colon <= 0 {
			continue
		}
		headers = append(headers, multipartHeader{name: strings.TrimSpace(line[:colon]), value: strings.TrimSpace(line[colon+1:])})
	}
	return headers, b[pos:]
}

var (
	responseLine = regexp.MustCompile(`(?i)^(HTTP/\d(?:\.\d)?)\s+(\d{3})(?:\s+(.*))?$`)
	requestLine  = regexp.MustCompile(`^([A-Z]+)\s+(\S+)(?:\s+HTTP/\d(?:\.\d)?)?$`)
)

func parseHTTPMessage(b []byte) *httpMessage {
	start := 0
	for start < len(b) && (b[start] == '\r' || b[start] == '\n') {
		start++
	}
	lf := bytes.IndexByte(b[start:], '\n')
	lineEnd := len(b)
	if lf >= 0 {
		lf += start
		lineEnd = lf
	}
	contentEnd := lineEnd
	if lineEnd > start && b[lineEnd-1] == '\r' {
		contentEnd = lineEnd - 1
	}
	startLine := strings.TrimSpace(latin1(b[start:contentEnd]))
	response := responseLine.FindStringSubmatch(startLine)
	request := requestLine.FindStringSubmatch(startLine)
	if response == nil && request == nil {
		return nil
	}
	var rest []byte
	if lf >= 0 {
		rest = b[lf+1:]
	}
	headers, body := splitHeaders(rest)
	message := &httpMessage{startLine: startLine, headers: headers, body: body}
	if response != nil {
		message.status, _ = strconv.Atoi(response[2])
		message.statusReason = strings.TrimSpace(response[3])
	} else {
		message.method, message.url = request[1], request[2]
	}
	return message
}

func decodeHeaderLine(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return latin1(b)
}

func headerValue(headers []multipartHeader, name string) (string, bool) {
	for _, h := range headers {
		if strings.EqualFold(h.name, name) {
			return h.value, true
		}
	}
	return "", false
}

func mediaTypeOf(contentType string) string {
	essence, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(essence))
}

func headerParams(header string) map[string]string {
	params := map[string]string{}
	i := strings.IndexByte(header, ';')
	if i < 0 {
		return params
	}
	for i < len(header) {
		for i < len(header) && (header[i] == ';' || header[i] == ' ' || header[i] == '\t') {
			i++
		}
		nameStart := i
		for i < len(header) && header[i] != '=' && header[i] != ';' {
			i++
		}
		name := strings.ToLower(strings.TrimSpace(header[nameStart:i]))
		if i >= len(header) || header[i] != '=' {
			continue
		}
		i++
		for i < len(header) && (header[i] == ' ' || header[i] == '\t') {
			i++
		}
		var value strings.Builder
		if i < len(header) && header[i] == '"' {
			i++
			for i < len(header) && header[i] != '"' {
				if header[i] == '\\' && i+1 < len(header) {
					i++
				}
				value.WriteByte(header[i])
				i++
			}
			for i < len(header) && header[i] != ';' {
				i++
			}
		} else {
			valueStart := i
			for i < len(header) && header[i] != ';' {
				i++
			}
			value.WriteString(strings.TrimSpace(header[valueStart:i]))
		}
		if _, seen := params[name]; name != "" && !seen {
			params[name] = value.String()
		}
	}
	return params
}

var extendedValue = regexp.MustCompile(`^([^']*)'[^']*'(.*)$`)

func decodeExtendedValue(value string) string {
	m := extendedValue.FindStringSubmatch(value)
	if m == nil {
		return value
	}
	encoded := m[2]
	var raw []byte
	for i := 0; i < len(encoded); i++ {
		if encoded[i] == '%' && i+2 < len(encoded) && isHex(encoded[i+1]) && isHex(encoded[i+2]) {
			v, _ := strconv.ParseUint(encoded[i+1:i+3], 16, 8)
			raw = append(raw, byte(v))
			i += 2
			continue
		}
		raw = append(raw, encoded[i])
	}
	label := strings.TrimSpace(m[1])
	if label == "" {
		label = "utf-8"
	}
	text, ok := decodeCharset(raw, label)
	if !ok {
		return encoded
	}
	return text
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func decodeCharset(b []byte, label string) (string, bool) {
	enc, err := htmlindex.Get(label)
	if err != nil {
		return "", false
	}
	if name, _ := htmlindex.Name(enc); name == "utf-8" {
		return strings.ToValidUTF8(string(b), "�"), true
	}
	text, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return "", false
	}
	return string(text), true
}

func isProbablyBinary(b []byte) bool {
	sample := b[:min(len(b), 8192)]
	for _, c := range sample {
		if c == 0 {
			return true
		}
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != 0x0c && c != 0x1b {
			return true
		}
	}
	return !utf8.Valid(b)
}
