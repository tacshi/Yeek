package query

const MaxDepth = 32

func Parse(input string) *Node {
	p := &parser{toks: lex(input)}
	var parts []*Node
	for {
		if node := p.parseOr(0); node != nil {
			parts = append(parts, node)
		}
		if p.peek() == nil {
			break
		}
		p.i++
	}
	return join(parts, NodeAnd)
}

type parser struct {
	toks []tok
	i    int
}

func (p *parser) peek() *tok {
	if p.i < len(p.toks) {
		return &p.toks[p.i]
	}
	return nil
}

func (p *parser) parseOr(depth int) *Node {
	var parts []*Node
	for {
		if node := p.parseAnd(depth); node != nil {
			parts = append(parts, node)
		}
		t := p.peek()
		if t == nil || t.kind != tokOr {
			break
		}
		p.i++
	}
	return join(parts, NodeOr)
}

func (p *parser) parseAnd(depth int) *Node {
	var parts []*Node
	for t := p.peek(); t != nil; t = p.peek() {
		switch t.kind {
		case tokOr, tokRParen:
			return join(parts, NodeAnd)
		case tokAnd:
			p.i++
		default:
			if node := p.parseUnary(depth); node != nil {
				parts = append(parts, node)
			}
		}
	}
	return join(parts, NodeAnd)
}

func (p *parser) parseUnary(depth int) *Node {
	t := p.peek()
	if t == nil {
		return nil
	}
	if t.kind != tokMinus {
		return p.parsePrimary(depth)
	}
	p.i++
	next := p.peek()
	if next == nil || next.span.Start != t.span.End || next.kind == tokRParen || next.kind == tokOr || next.kind == tokAnd {
		return nil
	}
	if depth >= MaxDepth {
		return nil
	}
	node := p.parseUnary(depth + 1)
	if node == nil {
		return nil
	}
	return &Node{Kind: NodeNot, Operand: node, Span: t.span.to(node.Span)}
}

func (p *parser) parsePrimary(depth int) *Node {
	t := p.peek()
	if t == nil {
		return nil
	}
	p.i++
	switch t.kind {
	case tokWord:
		return &Node{Kind: NodeTerm, Text: t.text, Span: t.span}
	case tokPhrase:
		return &Node{Kind: NodePhrase, Text: t.text, Span: t.span}
	case tokField:
		return &Node{
			Kind:      NodeField,
			Path:      t.path,
			Value:     t.value,
			Span:      t.span,
			PathSpan:  t.pathSpan,
			ValueSpan: t.valueSpan,
		}
	case tokLParen:
		if depth >= MaxDepth {
			return nil
		}
		inner := p.parseOr(depth + 1)
		if next := p.peek(); next != nil && next.kind == tokRParen {
			p.i++
		}
		return inner
	}
	return nil
}

func join(parts []*Node, kind NodeKind) *Node {
	switch len(parts) {
	case 0:
		return nil
	case 1:
		return parts[0]
	}
	return &Node{Kind: kind, Children: parts, Span: parts[0].Span.to(parts[len(parts)-1].Span)}
}
