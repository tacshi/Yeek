package filter

import (
	"encoding/json/v2"
	"reflect"
	"strings"
	"testing"
)

// Ported from Yaak's crates/yaak-filter/tests/json_path.rs, itself from the
// @yaak/filter-jsonpath plugin's tests.

func decoded(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return v
}

// matches is the filter's matches, decoded.
func matches(t *testing.T, doc, path string) any {
	t.Helper()
	content, err := FilterJSONPath(doc, path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return decoded(t, content)
}

func assertMatches(t *testing.T, doc, path, want string) {
	t.Helper()
	if got := matches(t, doc, path); !reflect.DeepEqual(got, decoded(t, want)) {
		t.Errorf("%s = %v, want %s", path, got, want)
	}
}

func filterError(t *testing.T, doc, path string) string {
	t.Helper()
	content, err := FilterJSONPath(doc, path)
	if content != "" || err == nil {
		t.Fatalf("%s should fail, got %q", path, content)
	}
	if !strings.HasPrefix(err.Error(), "Invalid filter: ") {
		t.Fatalf("%s: %v", path, err)
	}
	return err.Error()
}

func jsonText(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// crumb is the path the response breadcrumb builds for a key
// (segmentsToJsonPath in the client).
func crumb(key string) string {
	bare := key != "$" && key != "" && (isASCIILetter(rune(key[0])) || key[0] == '_' || key[0] == '$')
	for i := 0; bare && i < len(key); i++ {
		bare = isASCIIAlnum(key[i]) || key[i] == '_' || key[i] == '$'
	}
	if bare {
		return "." + key
	}
	return "[" + jsonText(key) + "]"
}

func TestBreadcrumbSelectsTheLiteralKeyNotAnOperatorOrExpression(t *testing.T) {
	for _, key := range []string{
		"identifier", "a.b", "space key", "", `quote"key`, "single'key", `back\slash`, "bracket]key",
		`combined"\]'key`, "*", "..", "$", "$foo", "^", "~", "a,b", "0:2", "?(true)", "(@.length-1)",
		"@number()", "`key", "x)]y", "x)']y", "semi;colon", "percent%@%key", "line\nbreak\t\x00",
		"emoji 🤔", "__proto__", "constructor",
	} {
		doc := `{` + jsonText(key) + `:"needle-value","decoy":"wrong-value"}`
		assertMatches(t, doc, "$"+crumb(key), `["needle-value"]`)
	}
}

func TestNestedUnusualKeysAndArraysFilterAtEveryCrumb(t *testing.T) {
	inner := `{"inner'\\]":{"":"needle-value"}}`
	doc := `{"outer":{"quote\"key":["decoy",` + inner + `]}}`
	crumbs := []string{crumb("outer"), crumb(`quote"key`), "[1]", crumb(`inner'\]`), crumb("")}
	path := func(count int) string { return "$" + strings.Join(crumbs[:count], "") }
	assertMatches(t, doc, path(5), `["needle-value"]`)
	assertMatches(t, doc, path(4), `[{"":"needle-value"}]`)
	assertMatches(t, doc, path(3), `[`+inner+`]`)
	assertMatches(t, doc, path(2), `[["decoy",`+inner+`]]`)
	assertMatches(t, doc, path(1), `[{"quote\"key":["decoy",`+inner+`]}]`)
	assertMatches(t, doc, path(0), `[`+doc+`]`)
}

func TestRootArraysAndQuotedNumericObjectKeysRemainDistinct(t *testing.T) {
	doc := `["decoy",{"01":{"1":"needle-value"}}]`
	assertMatches(t, doc, `$[1]["01"]["1"]`, `["needle-value"]`)
	assertMatches(t, doc, `$[1]["01"]["missing"]`, `[]`)
}

func TestRetainsTheMatchedValue(t *testing.T) {
	for _, value := range []string{"null", "false", "0", `""`, "[1,2]", `{"value":3}`} {
		assertMatches(t, `{"quote\"key":`+value+`}`, `$["quote\"key"]`, `[`+value+`]`)
	}
}

func TestOnlyFollowsOwnPropertiesWhileAllowingPrototypeNamedKeys(t *testing.T) {
	doc := `{"a.b":{"__proto__":{"constructor":"own-value"}}}`
	assertMatches(t, doc, `$["a.b"].__proto__.constructor`, `["own-value"]`)
	assertMatches(t, doc, `$["a.b"].constructor`, `[]`)
	assertMatches(t, doc, `$["a.b"].__proto__.toString`, `[]`)
	assertMatches(t, doc, `$["a.b"].__proto__.constructor.__proto__`, `[]`)
}

const itemsDoc = `{"items":[{"id":1},{"id":2},{"id":3}],"space key":4}`

func TestExistingJSONPathExpressions(t *testing.T) {
	for _, c := range [][2]string{
		{"$", `[` + itemsDoc + `]`},
		{"$.items[1].id", "[2]"},
		{`$["space key"]`, "[4]"},
		{"$['space key']", "[4]"},
		{"$.items[*].id", "[1,2,3]"},
		{"$..id", "[1,2,3]"},
		{"$.items[0:2].id", "[1,2]"},
		{"$.items[0,2].id", "[1,3]"},
		{"$.items[-1].id", "[3]"},
		{"$.items[?(@.id > 1)].id", "[2,3]"},
		{"$.items[?@.id > 1].id", "[2,3]"},
		{`$.items[?(@["id"] > 1)].id`, "[2,3]"},
		{`$["items"][*].id`, "[1,2,3]"},
		{"$.items[?length(@) == 1].id", "[1,2,3]"},
		{"$.missing", "[]"},
	} {
		assertMatches(t, itemsDoc, c[0], c[1])
	}
}

func TestJSONPathPlusExtensionsWork(t *testing.T) {
	items := `[{"id":1},{"id":2},{"id":3}]`
	for _, c := range [][2]string{
		{"$.items[(@.length-1)].id", "[3]"},
		{`$["items"].$`, `[` + items + `]`},
		{"$.items[?(@.id.toString() == '1')]", `[{"id":1}]`},
		{"$.items[?(@property == 1)].id", "[2]"},
		{"$.items[?(@.id > 1)]^", `[` + items + `,` + items + `]`},
		{"$.items[*]~", "[0,1,2]"},
		{"$..*@number()", "[4,1,2,3]"},
		{"$.items[?(@.id == '2')].id", "[2]"},
		{"$.items[?(/^[13]$/.test(@.id))].id", "[1,3]"},
		{"$.`space key", "[4]"},
	} {
		assertMatches(t, itemsDoc, c[0], c[1])
	}
}

func TestCallsOutsideTheAllowlistAndStatementsAreErrors(t *testing.T) {
	doc := `{"items":[{"id":1,"name":"a"}],"none":[]}`
	for _, c := range [][2]string{
		{"$.items[?(@.name.padStart(2) == ' a')]", "padStart"},
		{"$.items[?(@.name['padStart'](2) == ' a')]", "padStart"},
		{"$.items[?(@.hasOwnProperty('id'))]", "hasOwnProperty"},
		{"$.items[?(@.id; true)]", "multiple expressions"},
		{"$.items[?(var x = 1; x)]", "var"},
		{"$.items[?(@.id = 2)]", "assignment"},
		{"$.items[?(@.id++)]", "++"},
		{"$.items[?(@.id ** 2)]", "**"},
		{"$.items[?(foo(@.id))]", "foo()"},
	} {
		if message := filterError(t, doc, c[0]); !strings.Contains(message, c[1]) {
			t.Errorf("%s: %s", c[0], message)
		}
	}
}

func TestDeviationEagerParseRejectsFiltersNoNodeReaches(t *testing.T) {
	doc := `{"none":[]}`
	if m := filterError(t, doc, "$.none[?(@.a ==)]"); !strings.Contains(m, "Expected expression") {
		t.Error(m)
	}
	if m := filterError(t, doc, "$.none[?(@.a.padStart(1))]"); !strings.Contains(m, "padStart") {
		t.Error(m)
	}
}

func TestDeviationRFCNegativeIndexAndStep(t *testing.T) {
	doc := `{"a":[0,1,2,3]}`
	assertMatches(t, doc, "$.a[-1]", "[3]")
	assertMatches(t, doc, "$.a[-5]", "[]")
	assertMatches(t, doc, "$.a[::-1]", "[3,2,1,0]")
	assertMatches(t, doc, "$.a[3:0:-2]", "[3,1]")
	// Parts of a union keep jsonpath-plus's reading, so its non-empty results stand.
	assertMatches(t, doc, "$.a[0,-1]", "[0]")
}

func TestDeviationRFCFilterWithoutParentheses(t *testing.T) {
	doc := `{"a":[{"x":1},{"x":0},{"y":2}]}`
	assertMatches(t, doc, "$.a[?@.x]", `[{"x":1},{"x":0}]`)
	assertMatches(t, doc, "$.a[?@.x == '1']", "[]")
	assertMatches(t, doc, "$.a[?@.x < 1 || @.y]", `[{"x":0},{"y":2}]`)
	if m := filterError(t, doc, "$.a[?@.* == 1]"); !strings.Contains(m, "cannot be compared") {
		t.Error(m)
	}
}

func TestDeviationRFCFunctions(t *testing.T) {
	doc := `{"a":[{"n":"ab","t":[1,2]},{"n":"b","t":[]}]}`
	assertMatches(t, doc, "$.a[?length(@.n) == 2].n", `["ab"]`)
	assertMatches(t, doc, "$.a[?(count(@.t[*]) == 0)].n", `["b"]`)
	assertMatches(t, doc, "$.a[?match(@.n, 'a.')].n", `["ab"]`)
	assertMatches(t, doc, "$.a[?(search(@.n, 'b'))].n", `["ab","b"]`)
	assertMatches(t, doc, "$.a[?value(@.t[0]) == 1].n", `["ab"]`)
	if m := filterError(t, doc, "$.a[?length(@.n)]"); !strings.Contains(m, "compare it") {
		t.Error(m)
	}
}

func TestDeviationFalsyRootIsADocument(t *testing.T) {
	for _, root := range []string{"0", `""`, "false", "null"} {
		if got, err := FilterJSONPath(root, "$"); err != nil || got != "[\n  "+root+"\n]" {
			t.Errorf("%s: %q %v", root, got, err)
		}
	}
}

func TestDeviationLoneSurrogateBecomesReplacementCharacter(t *testing.T) {
	assertMatches(t, `{"s":"🤔"}`, "$.s[0]", `["\uFFFD"]`)
}

func TestIntegerLikeKeysComeFirstAsInJavaScript(t *testing.T) {
	if got, _ := FilterJSONPath(`{"b":1,"10":2,"a":3,"2":4}`, "$.*~"); got != "[\n  \"2\",\n  \"10\",\n  \"b\",\n  \"a\"\n]" {
		t.Errorf("%q", got)
	}
}

func TestOutputIsPrettyPrintedAndKeepsKeyOrder(t *testing.T) {
	got, err := FilterJSONPath(`{"b":{"z":1,"a":[true,null]}}`, "$.b")
	if err != nil || got != "[\n  {\n    \"z\": 1,\n    \"a\": [\n      true,\n      null\n    ]\n  }\n]" {
		t.Errorf("%q %v", got, err)
	}
}

func TestInvalidJSONIsAnError(t *testing.T) {
	for _, doc := range []string{"{not json", "{\"a\":\"\\é\"}"} {
		if got, err := FilterJSONPath(doc, "$"); got != "" || err == nil || !strings.HasPrefix(err.Error(), "Invalid JSON: ") {
			t.Errorf("%s: %q %v", doc, got, err)
		}
	}
}

func TestContentTypePicksTheLanguage(t *testing.T) {
	for contentType, want := range map[string]Language{
		"application/json": JSONPath, "text/plain": JSONPath, "": JSONPath,
		"application/xml": XPath, "application/rss+XML": XPath, "text/html; charset=utf-8": XPath,
	} {
		if got := LanguageFor(contentType); got != want {
			t.Errorf("%s: %v", contentType, got)
		}
	}
	if got, _ := Filter(JSONPath, "[1]", "$[0]"); got != "[\n  1\n]" {
		t.Errorf("%q", got)
	}
}

func TestComparingAnArrayToAnObjectIsFalse(t *testing.T) {
	if got, _ := FilterJSONPath(`[{"a":[],"b":{}},{"a":[1],"b":[1]}]`, "$[?@.a == @.b].a"); got != "[\n  [\n    1\n  ]\n]" {
		t.Errorf("%q", got)
	}
}

func TestDeeplyNestedFiltersAreErrors(t *testing.T) {
	nested := func(open, inner, close string) string {
		return strings.Repeat(open, 5000) + inner + strings.Repeat(close, 5000)
	}
	for _, q := range []string{
		"$[?" + nested("length(", "@", ")") + " == 1]",
		"$[?" + nested("(", "@", ")") + "]",
		"$[?(" + nested("(", "@", ")") + " == 1)]",
		nested("$[?@", "", "]"),
	} {
		if _, err := FilterJSONPath("[[1]]", q); err == nil || !strings.HasPrefix(err.Error(), "Invalid filter: ") {
			t.Errorf("%s: %v", q[:20], err)
		}
	}
}
