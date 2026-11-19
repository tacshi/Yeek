package engine

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func decodeContent(body io.Reader, encoding string) (io.Reader, func(), error) {
	noop := func() {}
	encoding = strings.ToLower(strings.TrimSpace(encoding))
	if !slices.Contains([]string{"gzip", "x-gzip", "deflate", "br", "zstd"}, encoding) {
		return body, noop, nil
	}
	buffered := bufio.NewReader(body)
	if _, err := buffered.Peek(1); errors.Is(err, io.EOF) {
		return buffered, noop, nil
	}
	switch encoding {
	case "gzip", "x-gzip":
		r, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, noop, err
		}
		return r, func() { _ = r.Close() }, nil
	case "deflate":
		header, _ := buffered.Peek(5)
		switch deflateKind(header) {
		case deflateZlib:
			r, err := zlib.NewReader(buffered)
			if err != nil {
				return nil, noop, err
			}
			return r, func() { _ = r.Close() }, nil
		case deflateEither:
			data, err := io.ReadAll(io.LimitReader(buffered, deflateHeldLimit+1))
			if err != nil {
				return nil, noop, err
			}
			whole := len(data) <= deflateHeldLimit
			source := io.MultiReader(bytes.NewReader(data), buffered)
			if r, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
				out, err := io.ReadAll(io.LimitReader(r, deflateHeldLimit+1))
				switch {
				case err == nil && whole && len(out) <= deflateHeldLimit:
					return bytes.NewReader(out), noop, nil
				case err == nil || !whole && errors.Is(err, io.ErrUnexpectedEOF):
					stream, err := zlib.NewReader(source)
					if err != nil {
						return nil, noop, err
					}
					return stream, func() { _ = stream.Close() }, nil
				}
			}
			r := flate.NewReader(source)
			return r, func() { _ = r.Close() }, nil
		}
		r := flate.NewReader(buffered)
		return r, func() { _ = r.Close() }, nil
	case "br":
		return brotli.NewReader(buffered), noop, nil
	}
	r, err := zstd.NewReader(buffered)
	if err != nil {
		return nil, noop, err
	}
	return r, r.Close, nil
}

const deflateHeldLimit = 1 << 20

const (
	deflateRaw = iota
	deflateZlib
	deflateEither
)

func deflateKind(data []byte) int {
	if len(data) < 2 || data[0]&0x0f != 8 || data[0]>>4 > 7 || (uint16(data[0])<<8|uint16(data[1]))%31 != 0 {
		return deflateRaw
	}
	if len(data) >= 5 && uint16(data[1])|uint16(data[2])<<8 != ^(uint16(data[3])|uint16(data[4])<<8) {
		return deflateZlib
	}
	return deflateEither
}
