package query

import "testing"

func TestFormatBareOrQuoted(t *testing.T) {
	for value, want := range map[string]string{
		"post":     "post",
		"a b":      `"a b"`,
		"":         `""`,
		"*":        `"*"`,
		"a*":       `"a*"`,
		">5":       `">5"`,
		">x":       ">x",
		"[1 TO 2]": `"[1 TO 2]"`,
		"[x":       `"[x"`,
		"a)":       `"a)"`,
		"a(b":      "a(b",
		`a"b`:      `a"b`,
		`a\b`:      `a\b`,
		`x\ y`:     `"x\\ y"`,
	} {
		if got := FormatValue(value); got != want {
			t.Errorf("FormatValue(%q) = %s, want %s", value, got, want)
		}
	}
}

func TestFormatNegated(t *testing.T) {
	if got := FormatField("type", "grpc", true); got != "-@type:grpc" {
		t.Errorf("got %s", got)
	}
	if got := FormatField("name", "My Request", true); got != `-@name:"My Request"` {
		t.Errorf("got %s", got)
	}
}

func TestFormatPaths(t *testing.T) {
	for _, p := range []string{"method", "req.header.x-cache"} {
		if !IsValidPath(p) {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []string{"", "a..b", "a b"} {
		if IsValidPath(p) {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestQuote(t *testing.T) {
	if got := Quote(`say "hi" \o/`); got != `"say \"hi\" \\o/"` {
		t.Errorf("got %s", got)
	}
}
