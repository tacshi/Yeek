package filter

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
)

func FilterXPath(text, query string) (out string, err error) {
	if strings.TrimSpace(query) == "" {
		return "", errors.New("Invalid filter: empty expression") //nolint:staticcheck
	}
	expression, err := xpath.Compile(query)
	if err != nil {
		return "", fmt.Errorf("Invalid filter: %w", err) //nolint:staticcheck
	}
	doc, err := xmlquery.Parse(strings.NewReader(text))
	if err != nil {
		return "", fmt.Errorf("Invalid XML: %w", err) //nolint:staticcheck
	}
	mergeAdjacentText(doc)
	defer func() {
		if r := recover(); r != nil {
			out, err = "", fmt.Errorf("Invalid filter: %v", r) //nolint:staticcheck
		}
	}()
	switch v := expression.Evaluate(xmlquery.CreateXPathNavigator(doc)).(type) {
	case *xpath.NodeIterator:
		return serializeNodes(doc, v), nil
	case float64:
		return numberToString(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case string:
		return v, nil
	}
	return "", nil
}

func mergeAdjacentText(node *xmlquery.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		switch child.Type {
		case xmlquery.TextNode, xmlquery.CharDataNode:
			child.Type = xmlquery.TextNode
			for next != nil && (next.Type == xmlquery.TextNode || next.Type == xmlquery.CharDataNode) {
				child.Data += next.Data
				after := next.NextSibling
				xmlquery.RemoveFromTree(next)
				next = after
			}
		case xmlquery.ElementNode:
			mergeAdjacentText(child)
		}
		child = next
	}
}

type selected struct {
	node *xmlquery.Node
	attr int
}

func serializeNodes(doc *xmlquery.Node, nodes *xpath.NodeIterator) string {
	order := map[*xmlquery.Node]int{}
	var number func(*xmlquery.Node)
	number = func(n *xmlquery.Node) {
		order[n] = len(order)
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			number(c)
		}
	}
	number(doc)
	var picked []selected
	for nodes.MoveNext() {
		nav, ok := nodes.Current().(*xmlquery.NodeNavigator)
		if !ok {
			continue
		}
		s := selected{node: nav.Current(), attr: -1}
		if nav.NodeType() == xpath.AttributeNode {
			s.attr = slices.IndexFunc(s.node.Attr, func(a xmlquery.Attr) bool {
				return a.Name.Local == nav.LocalName() && a.Name.Space == nav.Prefix() && a.Value == nav.Value()
			})
		}
		if !slices.Contains(picked, s) {
			picked = append(picked, s)
		}
	}
	slices.SortStableFunc(picked, func(a, b selected) int {
		if order[a.node] != order[b.node] {
			return order[a.node] - order[b.node]
		}
		return a.attr - b.attr
	})
	lines := make([]string, len(picked))
	for i, s := range picked {
		var out strings.Builder
		serializeNode(&out, s)
		lines[i] = out.String()
	}
	return strings.Join(lines, "\n")
}

func serializeNode(out *strings.Builder, s selected) {
	n := s.node
	if s.attr >= 0 {
		a := n.Attr[s.attr]
		writeAttribute(out, qualifiedName(a.Name.Space, a.Name.Local), a.Value)
		return
	}
	switch n.Type {
	case xmlquery.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case xmlquery.ElementNode:
				writeElement(out, c, nsScope{})
			case xmlquery.CommentNode:
				writeComment(out, c.Data)
			case xmlquery.ProcessingInstruction:
				writePI(out, c)
			}
		}
	case xmlquery.ElementNode:
		writeElement(out, n, nsScope{})
	case xmlquery.TextNode, xmlquery.CharDataNode:
		escapeXML(out, n.Data, false)
	case xmlquery.CommentNode:
		writeComment(out, n.Data)
	case xmlquery.ProcessingInstruction:
		writePI(out, n)
	}
}

type nsScope map[string]string

func qualifiedName(prefix, local string) string {
	if prefix == "" {
		return local
	}
	return prefix + ":" + local
}

func isNamespaceDeclaration(a xmlquery.Attr) bool {
	return a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns"
}

func writeElement(out *strings.Builder, n *xmlquery.Node, parent nsScope) {
	scope := nsScope{}
	for k, v := range parent {
		scope[k] = v
	}
	type declaration struct{ prefix, uri string }
	var declarations []declaration
	declare := func(prefix, uri string) {
		if current, ok := scope[prefix]; prefix != "xml" && (!ok || current != uri) {
			scope[prefix] = uri
			declarations = append(declarations, declaration{prefix, uri})
		}
	}
	name := n.Data
	switch {
	case n.NamespaceURI != "":
		declare(n.Prefix, n.NamespaceURI)
		name = qualifiedName(n.Prefix, n.Data)
	case parent[""] != "":
		declare("", "")
	}
	type attribute struct{ name, value string }
	var attributes []attribute
	for _, a := range n.Attr {
		if isNamespaceDeclaration(a) {
			continue
		}
		attrName := a.Name.Local
		if a.NamespaceURI != "" && a.Name.Space != "" {
			declare(a.Name.Space, a.NamespaceURI)
			attrName = qualifiedName(a.Name.Space, a.Name.Local)
		}
		attributes = append(attributes, attribute{attrName, a.Value})
	}
	out.WriteString("<" + name)
	for _, a := range attributes {
		out.WriteByte(' ')
		writeAttribute(out, a.name, a.value)
	}
	for _, d := range declarations {
		out.WriteByte(' ')
		if d.prefix == "" {
			writeAttribute(out, "xmlns", d.uri)
		} else {
			writeAttribute(out, "xmlns:"+d.prefix, d.uri)
		}
	}
	if n.FirstChild == nil {
		out.WriteString("/>")
		return
	}
	out.WriteByte('>')
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xmlquery.ElementNode:
			writeElement(out, c, scope)
		case xmlquery.TextNode, xmlquery.CharDataNode:
			escapeXML(out, c.Data, false)
		case xmlquery.CommentNode:
			writeComment(out, c.Data)
		case xmlquery.ProcessingInstruction:
			writePI(out, c)
		}
	}
	out.WriteString("</" + name + ">")
}

func writeAttribute(out *strings.Builder, name, value string) {
	out.WriteString(name + `="`)
	escapeXML(out, value, true)
	out.WriteByte('"')
}

func writeComment(out *strings.Builder, text string) { out.WriteString("<!--" + text + "-->") }

func writePI(out *strings.Builder, n *xmlquery.Node) {
	out.WriteString("<?" + n.Data)
	if n.ProcInst != nil && n.ProcInst.Inst != "" {
		out.WriteString(" " + n.ProcInst.Inst)
	}
	out.WriteString("?>")
}

func escapeXML(out *strings.Builder, text string, attribute bool) {
	for _, c := range text {
		switch {
		case c == '<':
			out.WriteString("&lt;")
		case c == '>':
			out.WriteString("&gt;")
		case c == '&':
			out.WriteString("&amp;")
		case c == '"' && attribute:
			out.WriteString("&quot;")
		case c == '\t' && attribute:
			out.WriteString("&#9;")
		case c == '\n' && attribute:
			out.WriteString("&#10;")
		case c == '\r' && attribute:
			out.WriteString("&#13;")
		default:
			out.WriteRune(c)
		}
	}
}
