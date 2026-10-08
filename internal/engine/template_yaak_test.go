package engine

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Ported from Yaak's crates/yaak-templates/src/{parser,renderer}.rs tests.

func tv(kind, text string) *templateValue { return &templateValue{kind: kind, text: text} }

func fn(name string, args ...any) *templateValue {
	v := &templateValue{kind: "function", text: name, args: map[string]templateValue{}}
	for i := 0; i < len(args); i += 2 {
		key := args[i].(string)
		v.order = append(v.order, key)
		v.args[key] = *args[i+1].(*templateValue)
	}
	return v
}

func raw(text string) templateToken      { return templateToken{raw: text} }
func tag(v *templateValue) templateToken { return templateToken{tag: v} }

func TestTemplateParserMatchesYaak(t *testing.T) {
	for _, c := range []struct {
		name, source string
		want         []templateToken
	}{
		{"escaped", `\${[ foo ]}`, []templateToken{raw("${[ foo ]}")}},
		{"escaped tricky", `\\${[ foo ]}`, []templateToken{raw(`\\`), tag(tv("variable", "foo"))}},
		{"var simple", "${[ foo ]}", []templateToken{tag(tv("variable", "foo"))}},
		{"var dashes", "${[ a-b ]}", []templateToken{tag(tv("variable", "a-b"))}},
		{"var underscores", "${[ a_b ]}", []templateToken{tag(tv("variable", "a_b"))}},
		{"var dots", "${[ a.b ]}", []templateToken{tag(tv("variable", "a.b"))}},
		{"var prefixes are invalid", "${[ -a ]}${[ $a ]}", []templateToken{raw("${[ -a ]}${[ $a ]}")}},
		{"var underscore prefix", "${[ _a ]}", []templateToken{tag(tv("variable", "_a"))}},
		{"var boolean", "${[ true ]}${[ false ]}", []templateToken{tag(tv("boolean", "true")), tag(tv("boolean", "false"))}},
		{"multiple names invalid", "${[ foo bar ]}", []templateToken{raw("${[ foo bar ]}")}},
		{"tag string", `${[ 'foo \'bar\' baz' ]}`, []templateToken{tag(tv("string", "foo 'bar' baz"))}},
		{"tag b64 string", `${[ b64'Zm9vICdiYXInIGJheg' ]}`, []templateToken{tag(tv("string", "foo 'bar' baz"))}},
		{"var surrounded", "Hello ${[ foo ]}!", []templateToken{raw("Hello "), tag(tv("variable", "foo")), raw("!")}},
		{"fn simple", "${[ foo() ]}", []templateToken{tag(fn("foo"))}},
		{"fn dot name", "${[ foo.bar.baz() ]}", []templateToken{tag(fn("foo.bar.baz"))}},
		{"fn ident arg", "${[ foo(a=bar) ]}", []templateToken{tag(fn("foo", "a", tv("variable", "bar")))}},
		{"fn ident args", "${[ foo(a=bar,b = baz, c =qux ) ]}", []templateToken{tag(fn("foo", "a", tv("variable", "bar"), "b", tv("variable", "baz"), "c", tv("variable", "qux")))}},
		{"fn mixed args", `${[ foo(aaa=bar,bb='baz \'hi\'', c=qux, z=true ) ]}`, []templateToken{tag(fn("foo", "aaa", tv("variable", "bar"), "bb", tv("string", "baz 'hi'"), "c", tv("variable", "qux"), "z", tv("boolean", "true")))}},
		{"fn nested", "${[ foo(b=bar()) ]}", []templateToken{tag(fn("foo", "b", fn("bar")))}},
		{"fn nested args", `${[ outer(a=inner(a=foo, b='i'), c='o') ]}`, []templateToken{tag(fn("outer", "a", fn("inner", "a", tv("variable", "foo"), "b", tv("string", "i")), "c", tv("string", "o")))}},
		{"null", "${[ null ]}", []templateToken{tag(tv("null", ""))}},
	} {
		got, err := parseTemplate(c.source)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v, %v", c.name, got, err)
		}
	}
}

func TestTemplateStringDisplayMatchesYaak(t *testing.T) {
	for text, want := range map[string]string{
		"Hello You": "'Hello You'", "Hello 'You'": `'Hello \'You\''`, "X-Api-Key": "'X-Api-Key'", "$.token": "'$.token'", "it's": `'it\'s'`,
		`back\slash`: `'back\\slash'`, `{"a":1}`: `'{"a":1}'`, "héllo": "'héllo'", "a]}b": "'a]}b'",
		"line\nbreak": "b64'bGluZQpicmVhaw'", "line\rbreak": "b64'bGluZQ1icmVhaw'", "tab\there": "b64'dGFiCWhlcmU'",
	} {
		if got := formatTemplateString(text); got != want {
			t.Errorf("%q: got %s, want %s", text, got, want)
		}
	}
	for _, text := range []string{"X-Api-Key", "$.token", "hello world", "it's", `back\slash`, `{"a":1}`, "a]}b", "line\nbreak", "héllo", `trailing\`, "'", ""} {
		source := "${[ " + formatTemplateString(text) + " ]}"
		if got, err := parseTemplate(source); err != nil || !reflect.DeepEqual(got, []templateToken{tag(tv("string", text))}) {
			t.Errorf("round trip %q via %s: %#v %v", text, source, got, err)
		}
	}
	tag := FormatTemplateTag(TemplateExpression{Kind: "function", Value: "foo", Arguments: map[string]TemplateExpression{"arg": {Kind: "string", Value: "v 'x'"}, "arg2": {Kind: "variable", Value: "my_var"}}})
	if tag != `${[ foo(arg='v \'x\'', arg2=my_var) ]}` {
		t.Error(tag)
	}
}

func TestTemplateRendererMatchesYaak(t *testing.T) {
	render := func(source string, vars map[string]string, run templateRunner) (string, error) {
		return renderWith(source, vars, map[string]bool{}, 0, run)
	}
	upper := func(name string, args map[string]string) (string, error) {
		switch name {
		case "secret":
			return "abc", nil
		case "upper":
			return `"` + strings.ToUpper(args["foo"]) + `"`, nil
		case "no_op":
			return `"` + args["inner"] + `"`, nil
		case "say_hello":
			return fmt.Sprintf("say_hello: %d, %s %s", len(args), args["a"], args["b"]), nil
		case "error":
			return "", errors.New("Failed to do it!")
		case "identity":
			return args["value"], nil
		}
		return "", nil
	}
	for _, c := range []struct {
		name, source, want string
		vars               map[string]string
	}{
		{"empty", "", "", nil},
		{"text only", "Hello World!", "Hello World!", nil},
		{"simple", "${[ foo ]}", "bar", map[string]string{"foo": "bar"}},
		{"recursive var", "${[ foo ]}", "foo: bar: baz", map[string]string{"foo": "foo: ${[ bar ]}", "bar": "bar: ${[ baz ]}", "baz": "baz"}},
		{"empty var", "${[ foo ]}", "", map[string]string{"foo": ""}},
		{"surrounded", "hello ${[ word ]} world!", "hello cruel world!", map[string]string{"word": "cruel"}},
		{"valid fn", `${[ say_hello(a='John', b='Kate') ]}`, "say_hello: 2, John Kate", nil},
		{"fn arg", `${[ upper(foo='bar') ]}`, `"BAR"`, nil},
		{"fn b64 arg", `${[ upper(foo=b64'Zm9vICdiYXInIGJheg') ]}`, `"FOO 'BAR' BAZ"`, map[string]string{"foo": "bar"}},
		{"fn arg template", `${[ upper(foo='${[ foo ]}') ]}`, `"BAR"`, map[string]string{"foo": "bar"}},
		{"fn return template", `${[ no_op(inner='${[ foo ]}') ]}`, `"bar"`, map[string]string{"foo": "bar"}},
		{"nested fn", `${[ upper(foo=secret()) ]}`, `"ABC"`, nil},
		{"null renders empty", `a${[ null ]}b`, "ab", nil},
		{"unparseable tag stays text", `{"tpl":"${[ not valid ]}"}`, `{"tpl":"${[ not valid ]}"}`, nil},
		{"mustache braces are text", `Hello {{name}}`, `Hello {{name}}`, nil},
	} {
		if got, err := render(c.source, c.vars, upper); err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := render("${[ foo ]}", nil, upper); err == nil || !strings.Contains(err.Error(), `"foo"`) {
		t.Error("missing var", err)
	}
	if _, err := render("${[ foo ]}", map[string]string{"foo": "${[ foo ]}"}, upper); err == nil {
		t.Error("self-referencing var rendered")
	}
	if _, err := render(`hello ${[ error() ]}`, nil, upper); err == nil || err.Error() != "Failed to do it!" {
		t.Error("fn error", err)
	}
	// Yaak builds identity(value=identity(...('ok'))) one level past MAX_DEPTH.
	nested := "'ok'"
	for range maxTemplateDepth + 1 {
		nested = "identity(value=" + nested + ")"
	}
	if _, err := render("${[ "+nested+" ]}", nil, upper); !errors.Is(err, errTemplateStackExceeded) {
		t.Error("nesting beyond max depth", err)
	}
}
