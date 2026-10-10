package engine

import (
	"math"
	"strconv"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
)

// xpathItem is a node an XPath query selected: an attribute (name and
// value) or a node of the document.
type xpathItem struct {
	node        *xmlquery.Node
	name, value string
}

// filterXPath is Yaak's filterXPath (template-function-xml), which its
// xml.xpath, response and request body path functions share: an attribute,
// text or CDATA node gives its value and an element its first child as XML
// (#723); "join" joins those, and "all" joins the nodes as XML with commas,
// as JavaScript's String of an array does. A query that is no node set,
// such as count(), gives its value.
func filterXPath(body, path, result, join string) (string, error) {
	doc, err := xmlquery.Parse(strings.NewReader(body))
	if err != nil {
		return "", err
	}
	expression, err := xpath.Compile(path)
	if err != nil {
		return "", err
	}
	var items []xpathItem
	switch v := expression.Evaluate(xmlquery.CreateXPathNavigator(doc)).(type) {
	case *xpath.NodeIterator:
		for v.MoveNext() {
			nav := v.Current().(*xmlquery.NodeNavigator)
			if nav.NodeType() == xpath.AttributeNode {
				name := nav.LocalName()
				if nav.Prefix() != "" {
					name = nav.Prefix() + ":" + name
				}
				items = append(items, xpathItem{name: name, value: nav.Value()})
			} else {
				items = append(items, xpathItem{node: nav.Current()})
			}
		}
	case float64:
		return jsNumber(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case string:
		return v, nil
	}
	switch result {
	case "join":
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = item.text()
		}
		return strings.Join(texts, join), nil
	case "all":
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = item.xml()
		}
		return strings.Join(texts, ","), nil
	}
	if len(items) == 0 {
		return "", nil
	}
	return items[0].text(), nil
}

// text is Yaak's nodeText.
func (item xpathItem) text() string {
	switch {
	case item.node == nil:
		return item.value
	case item.node.Type == xmlquery.TextNode || item.node.Type == xmlquery.CharDataNode:
		return item.node.Data
	case item.node.FirstChild == nil:
		return ""
	}
	var b strings.Builder
	serializeXML(&b, item.node.FirstChild)
	return b.String()
}

// xml is the item as xmldom's String of a node writes it; an attribute
// with the space before it.
func (item xpathItem) xml() string {
	if item.node == nil {
		return " " + item.name + `="` + escapeXMLAttr(item.value) + `"`
	}
	var b strings.Builder
	serializeXML(&b, item.node)
	return b.String()
}

// serializeXML writes n as xmldom's XMLSerializer does: empty elements
// closed in their tag, text escaping &, < and >, and attributes " too.
func serializeXML(b *strings.Builder, n *xmlquery.Node) {
	switch n.Type {
	case xmlquery.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serializeXML(b, c)
		}
	case xmlquery.DeclarationNode:
		b.WriteString("<?" + n.Data)
		for _, a := range n.Attr {
			b.WriteString(" " + attrName(a) + `="` + escapeXMLAttr(a.Value) + `"`)
		}
		b.WriteString("?>")
	case xmlquery.ProcessingInstruction:
		b.WriteString("<?" + n.Data)
		if n.ProcInst != nil && n.ProcInst.Inst != "" {
			b.WriteString(" " + n.ProcInst.Inst)
		}
		b.WriteString("?>")
	case xmlquery.ElementNode:
		name := n.Data
		if n.Prefix != "" {
			name = n.Prefix + ":" + name
		}
		b.WriteString("<" + name)
		for _, a := range n.Attr {
			b.WriteString(" " + attrName(a) + `="` + escapeXMLAttr(a.Value) + `"`)
		}
		if n.FirstChild == nil {
			b.WriteString("/>")
			return
		}
		b.WriteString(">")
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			serializeXML(b, c)
		}
		b.WriteString("</" + name + ">")
	case xmlquery.TextNode:
		b.WriteString(escapeXMLText(n.Data))
	case xmlquery.CharDataNode:
		b.WriteString("<![CDATA[" + n.Data + "]]>")
	case xmlquery.CommentNode:
		b.WriteString("<!--" + n.Data + "-->")
	}
}

func attrName(a xmlquery.Attr) string {
	if a.Name.Space != "" {
		return a.Name.Space + ":" + a.Name.Local
	}
	return a.Name.Local
}

var (
	xmlTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	xmlAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\t", "&#9;", "\n", "&#10;", "\r", "&#13;")
)

func escapeXMLText(s string) string { return xmlTextEscaper.Replace(s) }
func escapeXMLAttr(s string) string { return xmlAttrEscaper.Replace(s) }

// jsNumber is JavaScript's String of a number.
func jsNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	if a := math.Abs(f); a >= 1e21 || a < 1e-6 {
		s := strconv.FormatFloat(f, 'e', -1, 64)
		mantissa, exponent, _ := strings.Cut(s, "e")
		sign := exponent[:1]
		return mantissa + "e" + sign + strings.TrimLeft(exponent[1:], "0")
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
