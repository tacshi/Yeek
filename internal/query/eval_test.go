package query

import (
	"maps"
	"strings"
	"testing"
)

var sidebarColumns = []string{"method", "url", "name", "type", "folder", "grpc_service", "grpc_method"}

func sidebarSchema() Schema {
	return NewSchema(sidebarColumns, FreeTextFuzzy)
}

type row struct {
	text   string
	fields map[string]string
}

func request(method, name, url, folder string) row {
	fields := map[string]string{"method": strings.ToUpper(method), "url": url, "name": name, "type": "http"}
	if folder != "" {
		fields["folder"] = folder
	}
	return row{text: method + " " + name, fields: fields}
}

func grpcRow(name, service, method string) row {
	return row{text: name, fields: map[string]string{"grpc_method": method, "grpc_service": service, "url": "localhost:50051", "name": name, "type": "grpc"}}
}

func textRow(text string) row { return row{text: text} }

func fieldRow(fields map[string]string) row { return row{fields: maps.Clone(fields)} }

func matches(query string, schema Schema, r row) bool {
	return NewMatcher(query, schema).Match(r.text, r.fields)
}

func expectMatches(t *testing.T, schema Schema, r row, want bool, queries ...string) {
	t.Helper()
	for _, q := range queries {
		if got := matches(q, schema, r); got != want {
			t.Errorf("%q on %+v: got %v, want %v", q, r, got, want)
		}
	}
}

func TestEmptyQueryMatchesEverything(t *testing.T) {
	expectMatches(t, sidebarSchema(), row{}, true, "", "  ")
	if !NewMatcher("  ", sidebarSchema()).IsEmpty() || NewMatcher("x", sidebarSchema()).IsEmpty() {
		t.Error("only a blank query is empty")
	}
}

func TestBareWordsAreFuzzyInTheSidebar(t *testing.T) {
	r := request("GET", "My Request", "https://example.app", "")
	expectMatches(t, sidebarSchema(), r, true, "my", "REQ", "mr", "get")
	expectMatches(t, sidebarSchema(), r, false, "rq", "example")
}

func TestBareWordsAreSubstringsInSubstringMode(t *testing.T) {
	s := NewSchema(nil, FreeTextSubstring)
	r := textRow("GET /v1/charges 200")
	expectMatches(t, s, r, true, "rges", "v1 charges")
	expectMatches(t, s, r, false, "gv")
}

func TestPhrasesAreCaseInsensitiveSubstrings(t *testing.T) {
	r := request("GET", "My Request", "", "")
	expectMatches(t, sidebarSchema(), r, true, `"my req"`, `"MY REQ"`)
	expectMatches(t, sidebarSchema(), r, false, `"mr"`, `""`)
}

func TestEvaluateFields(t *testing.T) {
	post := request("post", "Create user", "https://api.example.app/users", "Users")
	get := request("get", "List users", "https://api.example.app/users", "Admin")
	rpc := grpcRow("Say hello", "helloworld.Greeter", "SayHello")
	s := sidebarSchema()

	expectMatches(t, s, post, true, "@method:post", "@METHOD:Po", "@url:example.app/users", "@folder:users", "-@type:grpc", "-@grpc_method:hello")
	expectMatches(t, s, post, false, "@grpc_method:hello", "method:post", "@nope:post", "@method:")
	expectMatches(t, s, get, false, "@method:post", "@folder:users")
	expectMatches(t, s, get, true, "@folder:users OR @folder:admin")
	expectMatches(t, s, rpc, true, "@type:grpc", "@grpc_service:greeter @grpc_method:hello")
	expectMatches(t, s, rpc, false, "-@type:grpc")
	expectMatches(t, s, textRow("localhost:50051"), true, "localhost:50051")
}

func TestGroupsAndNegation(t *testing.T) {
	s := sidebarSchema()
	a, b, ac := textRow("alpha"), textRow("beta"), textRow("alpha charlie")
	expectMatches(t, s, a, true, "(alpha OR beta) -charlie")
	expectMatches(t, s, b, true, "(alpha OR beta) -charlie")
	expectMatches(t, s, ac, false, "(alpha OR beta) -charlie")
	expectMatches(t, s, textRow("delta"), true, "-(alpha OR beta)")
	expectMatches(t, s, ac, true, "alpha charlie")
	expectMatches(t, s, a, false, "alpha charlie")
}

func TestWildcards(t *testing.T) {
	cases := []struct {
		pattern, text string
		want          bool
	}{
		{"*.github.com", "api.github.com", true},
		{"*.github.com", "github.com", false},
		{"a*", "a", true},
		{"*", "", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYYcd", false},
		{"a**c", "ac", true},
		{"ab*ba", "aba", false},
	}
	for _, c := range cases {
		if got := WildcardMatch(c.pattern, c.text); got != c.want {
			t.Errorf("WildcardMatch(%q, %q) = %v", c.pattern, c.text, got)
		}
	}
	s := sidebarSchema()
	r := fieldRow(map[string]string{"url": "https://API.github.com/repos"})
	expectMatches(t, s, r, true, "@url:https://api.github.com/*", "@url:*GITHUB*")
	expectMatches(t, s, r, false, "@url:github*", `@url:"*github*"`)
	expectMatches(t, s, fieldRow(map[string]string{"url": "a*b"}), true, `@url:"*"`)
}

func TestPresence(t *testing.T) {
	s := sidebarSchema()
	with := fieldRow(map[string]string{"folder": "Users", "url": "x"})
	without := fieldRow(map[string]string{"url": "x"})
	expectMatches(t, s, with, true, "@folder:*")
	expectMatches(t, s, without, false, "@folder:*")
	expectMatches(t, s, without, true, "-@folder:*")
	expectMatches(t, s, fieldRow(map[string]string{"url": ""}), false, "@url:*")
	expectMatches(t, s, fieldRow(nil), true, "-@url:*")
}

func TestComparisonsOnTextFields(t *testing.T) {
	s := sidebarSchema()
	expectMatches(t, s, fieldRow(map[string]string{"url": "404"}), true, "@url:>400", "@url:[400 TO 499]")
	expectMatches(t, s, fieldRow(map[string]string{"url": "x"}), false, "@url:>400", "@url:[400 TO 499]")
}

func TestFieldNamesMatchCaseInsensitively(t *testing.T) {
	if !NewMatcher("@url:y", sidebarSchema()).Match("x", map[string]string{"URL": "y"}) {
		t.Error("field names should match case-insensitively")
	}
}

func TestNonASCIITextMatchesCaseInsensitively(t *testing.T) {
	r := row{text: "ÜBER Café", fields: map[string]string{"name": "ÜBER Café"}}
	expectMatches(t, sidebarSchema(), r, true, "über", "@name:café", `"über c"`)
	expectMatches(t, sidebarSchema(), r, false, "@name:cafe")
}

func matchesFormattedURL(value string) bool {
	return matches(FormatField("url", value, false), sidebarSchema(), fieldRow(map[string]string{"url": value}))
}

func TestFormatKeepsURLLikeValuesBare(t *testing.T) {
	if got := FormatField("url", "example.app/foo-bar", false); got != "@url:example.app/foo-bar" {
		t.Errorf("got %s", got)
	}
	if !matchesFormattedURL("example.app/foo-bar") {
		t.Error("did not round-trip")
	}
}

func TestFormatQuotesValuesWithQuerySyntax(t *testing.T) {
	if got := FormatField("url", "exa$&#*@tsrna(*)", false); got != `@url:"exa$&#*@tsrna(*)"` {
		t.Errorf("got %s", got)
	}
	if !matchesFormattedURL("exa$&#*@tsrna(*)") {
		t.Error("quoted value did not round-trip")
	}
	if got := FormatField("url", "exa$&#@tsrna(", false); got != "@url:exa$&#@tsrna(" {
		t.Errorf("got %s", got)
	}
	if !matchesFormattedURL("exa$&#@tsrna(") {
		t.Error("bare value did not round-trip")
	}
}

func TestFormatKeepsValuesThatStartWithAnOperatorTokenBare(t *testing.T) {
	if got := FormatField("url", "-foo", false); got != "@url:-foo" {
		t.Errorf("got %s", got)
	}
	if !matchesFormattedURL("-foo") {
		t.Error("did not round-trip")
	}
}

func TestFormatKeepsBooleanOperatorWordsBare(t *testing.T) {
	for value, want := range map[string]string{"AND": "@url:AND", "or": "@url:or", "Not": "@url:Not"} {
		if got := FormatField("url", value, false); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
	if !matchesFormattedURL("AND") {
		t.Error("did not round-trip")
	}
}

func TestFormatEscapesQuotedValues(t *testing.T) {
	if got := FormatField("url", `say "hi"`, false); got != `@url:"say \"hi\""` {
		t.Errorf("got %s", got)
	}
	if !matchesFormattedURL(`say "hi"`) {
		t.Error("did not round-trip")
	}
}

func TestFormatQuotesValuesThatStartWithAQuote(t *testing.T) {
	if got := FormatField("url", `"hi"`, false); got != `@url:"\"hi\""` {
		t.Errorf("got %s", got)
	}
	if !matchesFormattedURL(`"hi"`) {
		t.Error("did not round-trip")
	}
}

func TestFormattedValuesRoundTrip(t *testing.T) {
	for _, value := range []string{"a b", "*", ">5", "[1 TO 2]", "x)", `back\slash`, `\"`, "tab\there", "é ü", "a*b", "<=3ms", `\`} {
		if !matchesFormattedURL(value) {
			t.Errorf("%q did not round-trip", value)
		}
	}
}
