package filter

import "testing"

func TestTemplateJSONPath(t *testing.T) {
	body := `{"items":[{"z":1,"a":"x"},{"z":2.50,"a":true}]}`
	for _, c := range []struct {
		path, result, join string
		formatted          bool
		want               string
	}{
		{"$.items[0]", "first", "", false, `{"z":1,"a":"x"}`},
		{"$.items[0]", "", "", true, "{\n  \"z\": 1,\n  \"a\": \"x\"\n}"},
		{"$.items[*].a", "first", "", false, "x"},
		{"$.items[*].z", "all", "", false, "[1,2.5]"},
		{"$.items[*].a", "join", ", ", false, "x, true"},
		{"$.missing", "first", "", false, ""},
		{"$.missing", "all", "", false, "[]"},
	} {
		got, err := TemplateJSONPath(body, c.path, c.result, c.join, c.formatted)
		if err != nil || got != c.want {
			t.Errorf("%s %s: %q %v, want %q", c.path, c.result, got, err, c.want)
		}
	}
	if _, err := TemplateJSONPath("{", "$", "first", "", false); err == nil {
		t.Error("invalid JSON accepted")
	}
}
