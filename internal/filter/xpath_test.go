package filter

import (
	"strings"
	"testing"
)

const (
	xmlItems = `<root><item id="1">A</item><item id="2">B &amp; C</item><empty/></root>`
	xmlFeed  = `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:m="urn:m"><entry><title>T</title><m:thumb url="x"/></entry></feed>`
	xmlMixed = `<?xml version="1.0"?><!-- c --><a x="1&quot;2"><!--note--><b><![CDATA[<raw>]]></b></a>`
)

func xpathContent(t *testing.T, xml, query string) string {
	t.Helper()
	content, err := FilterXPath(xml, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return content
}

func assertXPath(t *testing.T, xml, query, want string) {
	t.Helper()
	if got := xpathContent(t, xml, query); got != want {
		t.Errorf("%s = %q, want %q", query, got, want)
	}
}

func TestNodeSetsSerializeOneNodePerLine(t *testing.T) {
	assertXPath(t, xmlItems, "//item", "<item id=\"1\">A</item>\n<item id=\"2\">B &amp; C</item>")
	assertXPath(t, xmlItems, "//item/text()", "A\nB &amp; C")
	assertXPath(t, xmlItems, "//empty", "<empty/>")
	assertXPath(t, xmlItems, "//missing", "")
	assertXPath(t, xmlItems, "//item[@id='2']/..", xmlItems)
	assertXPath(t, xmlMixed, "//comment()", "<!-- c -->\n<!--note-->")
}

func TestAttributesSerializeAsNameAndValue(t *testing.T) {
	assertXPath(t, xmlItems, "//item/@id", "id=\"1\"\nid=\"2\"")
	assertXPath(t, xmlMixed, "/a/@x", `x="1&quot;2"`)
}

func TestScalarResultsPrintLikeJavaScript(t *testing.T) {
	assertXPath(t, xmlItems, "count(//item)", "2")
	assertXPath(t, xmlItems, "string(//item[2])", "B & C")
	assertXPath(t, xmlItems, "sum(//item/@id) div 4", "0.75")
	assertXPath(t, xmlItems, "boolean(//empty)", "true")
	assertXPath(t, xmlItems, "1 div 0", "Infinity")
	assertXPath(t, xmlItems, "0 div 0", "NaN")
	assertXPath(t, xmlItems, "-0", "0")
}

func TestNamespacesAreDeclaredWhereTheyAreUsed(t *testing.T) {
	assertXPath(t, xmlFeed, "//*[local-name()='entry']", `<entry xmlns="http://www.w3.org/2005/Atom"><title>T</title><m:thumb url="x" xmlns:m="urn:m"/></entry>`)
	assertXPath(t, xmlFeed, "//*[local-name()='thumb']", `<m:thumb url="x" xmlns:m="urn:m"/>`)
	assertXPath(t, xmlFeed, "//m:thumb", `<m:thumb url="x" xmlns:m="urn:m"/>`)
}

func TestRootSerializesTheWholeDocument(t *testing.T) {
	assertXPath(t, xmlMixed, "/", `<!-- c --><a x="1&quot;2"><!--note--><b>&lt;raw&gt;</b></a>`)
	assertXPath(t, xmlMixed, "//b/text()", "&lt;raw&gt;")
	assertXPath(t, "<a>x<![CDATA[<y>]]>&amp;z</a>", "/a/text()", "x&lt;y&gt;&amp;z")
}

func TestDoctypeIsAccepted(t *testing.T) {
	assertXPath(t, "<!DOCTYPE note><note>hi</note>", "/note", "<note>hi</note>")
	assertXPath(t, `<?xml version="1.0"?><!DOCTYPE note><note/>`, "/note", "<note/>")
}

func TestInvalidQueriesAndDocumentsAreErrors(t *testing.T) {
	for _, c := range [][3]string{
		{xmlItems, "//item[", "Invalid filter: "},
		{xmlItems, "", "Invalid filter: "},
		{xmlItems, "unknown-fn()", "Invalid filter: "},
		{"<html><body><p>one<br></p></body></html>", "//p", "Invalid XML: "},
		{"<a><b>", "/a", "Invalid XML: "},
	} {
		content, err := FilterXPath(c[0], c[1])
		if content != "" || err == nil || !strings.HasPrefix(err.Error(), c[2]) {
			t.Errorf("%s: %q %v", c[1], content, err)
		}
	}
}

func TestFilterDispatchesToXPath(t *testing.T) {
	if got, _ := Filter(XPath, xmlItems, "count(//item)"); got != "2" {
		t.Errorf("%q", got)
	}
}

func TestNumbersSwitchToExponentsWhereJavaScriptDoes(t *testing.T) {
	assertXPath(t, "<a>0.0000001</a>", "number(/a)", "1e-7")
	assertXPath(t, "<a>0.000001</a>", "number(/a)", "0.000001")
	assertXPath(t, "<a>1000000000000000000000</a>", "number(/a)", "1e+21")
	assertXPath(t, "<a>100000000000000000000</a>", "number(/a)", "100000000000000000000")
}
