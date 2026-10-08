package engine

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// decodeContent wraps a response body in the decoder for its
// Content-Encoding, like Yaak's decompress module: gzip (and x-gzip),
// deflate, br and zstd. Anything else passes through unchanged.
func decodeContent(body io.Reader, encoding string) (io.Reader, func(), error) {
	noop := func() {}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip", "x-gzip":
		r, err := gzip.NewReader(body)
		if err != nil {
			return nil, noop, err
		}
		return r, func() { _ = r.Close() }, nil
	case "deflate":
		// RFC 9110 deflate is zlib-wrapped, but some servers send raw
		// DEFLATE (which is what Yaak decodes); accept both.
		buffered := bufio.NewReader(body)
		if header, err := buffered.Peek(2); err == nil && header[0]&0x0f == 8 && (uint16(header[0])<<8|uint16(header[1]))%31 == 0 {
			r, err := zlib.NewReader(buffered)
			if err != nil {
				return nil, noop, err
			}
			return r, func() { _ = r.Close() }, nil
		}
		r := flate.NewReader(buffered)
		return r, func() { _ = r.Close() }, nil
	case "br":
		return brotli.NewReader(body), noop, nil
	case "zstd":
		r, err := zstd.NewReader(body)
		if err != nil {
			return nil, noop, err
		}
		return r, r.Close, nil
	}
	return body, noop, nil
}
