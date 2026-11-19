package query

import (
	"strings"
	"testing"
)

func TestResolvesFields(t *testing.T) {
	s := NewSchema([]string{"url", "grpc_service", "req.header"}, FreeTextSubstring)
	for path, want := range map[string]string{"URL": "url", "grpc_service": "grpc_service", "req.Header": "req.header"} {
		if got, ok := s.resolve(strings.Split(path, ".")); !ok || got != want {
			t.Errorf("%q: %q %v", path, got, ok)
		}
	}
	for _, path := range []string{"nope", "url.x", "req"} {
		if _, ok := s.resolve(strings.Split(path, ".")); ok {
			t.Errorf("%q resolved", path)
		}
	}
}
