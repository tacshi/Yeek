package engine

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeeplyNestedTemplateFailsToParse(t *testing.T) {
	text := "${[ " + strings.Repeat("a(b=", 1_000_000) + "1 ]}"
	if _, err := parseTemplate(text); !errors.Is(err, errTemplateStackExceeded) {
		t.Fatalf("err = %v", err)
	}
	if got := remapTemplateText(text, map[string]string{}); got != text {
		t.Fatal("unparseable tag was rewritten")
	}
}

func TestExponentialVariablesStopAtOutputLimit(t *testing.T) {
	vars := map[string]string{"l0": strings.Repeat("x", 100)}
	prev := "l0"
	for i := 1; i <= 20; i++ {
		name := "v" + strings.Repeat("i", i)
		vars[name] = strings.Repeat("${[ "+prev+" ]}", 10)
		prev = name
	}
	if _, err := renderText("${[ "+prev+" ]}", vars, map[string]bool{}, 0); !errors.Is(err, errTemplateOutputTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestServerValuesAreNotRenderedAgain(t *testing.T) {
	run := func(name string, args map[string]string) (string, error) {
		return "token=${[ SECRET ]}", nil
	}
	vars := map[string]string{"SECRET": "s3cr3t"}
	got, err := renderWith("${[ response.body.raw(request='rq_1') ]}", vars, map[string]bool{}, 0, run)
	if err != nil || got != "token=${[ SECRET ]}" {
		t.Fatalf("response text = %q, %v", got, err)
	}
	got, err = renderWith("${[ secure(value='x') ]}", vars, map[string]bool{}, 0, run)
	if err != nil || got != "token=s3cr3t" {
		t.Fatalf("other functions = %q, %v", got, err)
	}
}

func TestUnclosedTagsScanInLinearTime(t *testing.T) {
	text := strings.Repeat("${[", 200_000)
	started := time.Now()
	if got := remapTemplateText(text, map[string]string{}); got != text {
		t.Fatal("text changed")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("took %v", elapsed)
	}
}
