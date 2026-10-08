package desktop

import (
	"slices"
	"strings"
	"testing"

	"yeek/internal/engine"
)

var testMessage = &engine.GRPCMessage{Name: "pkg.Request", Fields: []engine.GRPCField{
	{Name: "name", ProtoName: "name", Kind: "string"},
	{Name: "pageSize", ProtoName: "page_size", Kind: "number"},
	{Name: "active", Kind: "boolean"},
	{Name: "kind", Kind: "enum", Enum: []string{"KIND_UNSPECIFIED", "KIND_USER"}},
	{Name: "tags", Kind: "string", Repeated: true},
	{Name: "labels", Kind: "string", Map: true},
	{Name: "owner", Kind: "object", Message: &engine.GRPCMessage{Name: "pkg.User", Fields: []engine.GRPCField{{Name: "id", Kind: "string"}, {Name: "role", Kind: "enum", Enum: []string{"ADMIN", "GUEST"}}}}},
	{Name: "members", Kind: "object", Repeated: true, Message: &engine.GRPCMessage{Name: "pkg.User", Fields: []engine.GRPCField{{Name: "id", Kind: "string"}}}},
}}

func optionNames(options []templateOption) []string {
	var names []string
	for _, o := range options {
		names = append(names, o.name)
	}
	return names
}

// at completes where | is.
func at(text string) []templateOption {
	caret := len([]rune(text[:strings.Index(text, "|")]))
	return jsonSchemaOptions(testMessage, strings.Replace(text, "|", "", 1), caret)
}

func TestJSONSchemaCompletion(t *testing.T) {
	for text, want := range map[string][]string{
		`{|}`:                    {"name", "pageSize", "active", "kind", "tags", "labels", "owner", "members"},
		`{"na|"}`:                {"name"},
		`{"name": "x", p|}`:      {"pageSize"},
		`{"name": "x", "|`:       {"pageSize", "active", "kind", "tags", "labels", "owner", "members"},
		`{"kind": |}`:            {"KIND_UNSPECIFIED", "KIND_USER"},
		`{"kind": "KIND_U|"}`:    {"KIND_UNSPECIFIED", "KIND_USER"},
		`{"active": |}`:          {"true", "false"},
		`{"owner": {|}}`:         {"id", "role"},
		`{"owner": {"role": |}}`: {"ADMIN", "GUEST"},
		`{"members": [{|}]}`:     {"id"},
		`{"labels": {|}}`:        nil,
		`{"name": |}`:            nil,
		`{"nope": {|}}`:          nil,
	} {
		if got := optionNames(at(text)); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", text, got, want)
		}
	}
	// A property name is quoted, with its colon, over what was typed.
	options := at(`{"na|"}`)
	if o := options[0]; o.apply != `"name": ` || o.from != 1 || o.end != 5 || o.detail != "string" {
		t.Fatalf("%+v", o)
	}
	if o := at(`{"na|": 1}`)[0]; o.apply != `"name"` {
		t.Fatalf("colon added twice: %+v", o)
	}
	if o := at(`{|}`); o[4].detail != "string[]" || o[6].detail != "User" || o[5].detail != "map<string>" {
		t.Fatal(o[4].detail, o[5].detail, o[6].detail)
	}
}

func TestJSONSchemaChecks(t *testing.T) {
	problems := checkJSONMessage(testMessage, `{"name": 1, "page_size": "20", "nope": true, "kind": "OTHER", "tags": "x", "owner": {"id": "${[ id ]}", "extra": 1}, "labels": {"a": 2}, "active": null}`)
	var messages []string
	for _, p := range problems {
		messages = append(messages, p.message)
	}
	want := []string{"Expected string", `Property "nope" is not expected`, "Expected one of KIND_UNSPECIFIED, KIND_USER", "Expected array", `Property "extra" is not expected`, "Expected string"}
	if !slices.Equal(messages, want) {
		t.Fatalf("%q", messages)
	}
	if problems[1].start != len([]rune(`{"name": 1, "page_size": "20", `)) {
		t.Fatal(problems[1])
	}
	if checkJSONMessage(testMessage, `{"name": `) != nil {
		t.Fatal("an unfinished message has problems of its own")
	}
}
