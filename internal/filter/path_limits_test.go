package filter

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLongPathsParseQuickly(t *testing.T) {
	for _, path := range []string{"$." + strings.Repeat("a.", 100000) + "a", "$" + strings.Repeat("'.", 100000)} {
		start := time.Now()
		if _, err := parsePath(path); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("%d bytes took %v", len(path), elapsed)
		}
	}
}

func TestPathsThatNeverFinishMatchingAreRefused(t *testing.T) {
	if _, err := parsePath("$" + strings.Repeat("'(", 20000)); !errors.Is(err, errPathTooComplex) {
		t.Fatal(err)
	}
}

func TestBoundariesSplitOutsideBracketsOnly(t *testing.T) {
	for in, want := range map[string]string{
		"$.a.b":       "$;a;b",
		"$['a.b'].c":  "$;a.b'];c",
		"$[0].x":      "$;0];x",
		`$."a".b`:     "$;a;b",
		"$..a":        "$;;a",
		"$[?(@.a)].b": "$;?(@.a)];b",
		"$.a[?(@.b)]": "$;a;?(@.b)]",
		"x.y]":        "x.y]",
	} {
		if got := replaceBoundaries(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
