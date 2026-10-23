package engine

import "testing"

func TestFilterXPath(t *testing.T) {
	for _, c := range []struct{ body, path, result, want string }{
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/name`, "first", `Alice`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/@id`, "first", `42`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/@id`, "join", `42, 7`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/name/text()`, "first", `Alice`},
		{`<a><![CDATA[x<y]]></a>`, `/a/text()`, "first", `x<y`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user`, "first", `<name>Alice</name>`},
		{`<a><b>1</b><c>2</c></a>`, `/a`, "first", `<b>1</b>`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/name`, "all", `<name>Alice</name>,<name>Bob</name>`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/@id`, "all", ` id="42", id="7"`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user`, "join", `<name>Alice</name>, <name>Bob</name>`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `count(//user)`, "first", `2`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `count(//user) > 1`, "first", `true`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `string(//user/@id)`, "first", `42`},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `sum(//user/@id) div 4`, "first", `12.25`},
		{`<a>x &amp; y &lt; z "q"</a>`, `/a`, "first", `x &amp; y &lt; z "q"`},
		{`<a>x &amp; y</a>`, `/a/text()`, "first", `x & y`},
		{`<a><!--c--><b/></a>`, `/a`, "first", `<!--c-->`},
		{`<a><![CDATA[x<y]]></a>`, `/a`, "first", `<![CDATA[x<y]]>`},
		{`<a/>`, `/a`, "first", ``},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//missing`, "first", ``},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//missing`, "all", ``},
		{`<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`, `//user/name/text()`, "all", `Alice,Bob`},
		{`<a k='v&amp;w'/>`, `/a/@k`, "all", ` k="v&amp;w"`},
		{`<r><a>1</a><a>2</a></r>`, `//a`, "join", `1, 2`},
		{"<r><a>a &gt; b ]]&gt; c</a></r>", "/r", "first", "<a>a &gt; b ]]&gt; c</a>"},
		{"<r><a k=\"x&gt;y &quot;q&quot; &#9;t&#10;n\">v</a></r>", "/r", "first", "<a k=\"x&gt;y &quot;q&quot; &#9;t&#10;n\">v</a>"},
		{`<r><x:a xmlns:x="urn:x" x:k="1"><b/></x:a></r>`, "/r", "all", `<r><x:a xmlns:x="urn:x" x:k="1"><b/></x:a></r>`},
		{`<r><?pi data?><a/></r>`, "/r", "first", `<?pi data?>`},
		{`<r>text<a/></r>`, "/r", "first", `text`},
		{`<r> <a/></r>`, "/r", "first", ` `},
		{`<?xml version="1.0"?><r/>`, "/", "all", `<?xml version="1.0"?><r/>`},
		{`<r><a/><a/></r>`, "//a", "all", `<a/>,<a/>`},
		{`<r>1</r>`, "number(/r)", "first", `1`},
		{`<r>1</r>`, "1 div 3", "first", `0.3333333333333333`},
		{`<r>1</r>`, "1 div 0", "first", `Infinity`},
		{`<r>1</r>`, "0 div 0", "first", `NaN`},
		{`<r>1</r>`, "100000000000000000000000", "first", `1e+23`},
	} {
		got, err := filterXPath(c.body, c.path, c.result, ", ")
		if err != nil || got != c.want {
			t.Errorf("%s %s on %s = %q, %v; want %q", c.result, c.path, c.body, got, err, c.want)
		}
	}
}

func TestTemplatePathXMLUsesTheFilter(t *testing.T) {
	users := `<users><user id="42"><name>Alice</name></user><user id="7"><name>Bob</name></user></users>`
	for _, c := range []struct{ result, want string }{{"", "42"}, {"first", "42"}, {"join", "42 | 7"}, {"all", ` id="42", id="7"`}} {
		got, err := templatePath(users, "//user/@id", map[string]string{"result": c.result, "join": " | "})
		if err != nil || got != c.want {
			t.Errorf("%q: %q, %v; want %q", c.result, got, err, c.want)
		}
	}
}
