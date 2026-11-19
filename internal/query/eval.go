package query

import "strings"

type Matcher struct {
	plan     *plan
	fuzzy    bool
	usesText bool
}

type planKind int

const (
	planAnd planKind = iota
	planOr
	planNot
	planTerm
	planPhrase
	planField
	planNever
)

type plan struct {
	kind      planKind
	children  []*plan
	operand   *plan
	text      string
	path      string
	condition Condition
}

func NewMatcher(query string, schema Schema) *Matcher {
	m := &Matcher{fuzzy: schema.FreeText == FreeTextFuzzy}
	if root := Parse(query); root != nil {
		m.plan = planNode(root, schema, &m.usesText)
	}
	return m
}

func (m *Matcher) IsEmpty() bool {
	return m.plan == nil
}

func (m *Matcher) Match(text string, fields map[string]string) bool {
	if m.plan == nil {
		return true
	}
	if m.usesText {
		text = strings.ToLower(text)
	}
	return m.eval(m.plan, text, fields, &fuzzyScratch{})
}

func fieldValue(fields map[string]string, path string) string {
	if value, ok := fields[path]; ok {
		return value
	}
	for key, value := range fields {
		if asciiEqualFold(key, path) {
			return value
		}
	}
	return ""
}

func (m *Matcher) eval(p *plan, text string, fields map[string]string, scratch *fuzzyScratch) bool {
	switch p.kind {
	case planAnd:
		for _, c := range p.children {
			if !m.eval(c, text, fields, scratch) {
				return false
			}
		}
		return true
	case planOr:
		for _, c := range p.children {
			if m.eval(c, text, fields, scratch) {
				return true
			}
		}
		return false
	case planNot:
		return !m.eval(p.operand, text, fields, scratch)
	case planTerm:
		if m.fuzzy {
			return fuzzyMatchIn(text, p.text, scratch)
		}
		return strings.Contains(text, p.text)
	case planPhrase:
		return strings.Contains(text, p.text)
	case planField:
		value := fieldValue(fields, p.path)
		return value != "" && p.condition.MatchesText(value)
	}
	return false
}

func planNode(node *Node, schema Schema, usesText *bool) *plan {
	switch node.Kind {
	case NodeAnd, NodeOr:
		kind := planAnd
		if node.Kind == NodeOr {
			kind = planOr
		}
		children := make([]*plan, len(node.Children))
		for i, child := range node.Children {
			children[i] = planNode(child, schema, usesText)
		}
		return &plan{kind: kind, children: children}
	case NodeNot:
		return &plan{kind: planNot, operand: planNode(node.Operand, schema, usesText)}
	case NodeTerm:
		term := strings.ToLower(strings.TrimSpace(node.Text))
		if term == "" {
			return &plan{kind: planNever}
		}
		*usesText = true
		return &plan{kind: planTerm, text: term}
	case NodePhrase:
		if node.Text == "" {
			return &plan{kind: planNever}
		}
		*usesText = true
		return &plan{kind: planPhrase, text: strings.ToLower(node.Text)}
	case NodeField:
		path, ok := schema.resolve(node.Path)
		if !ok {
			return &plan{kind: planNever}
		}
		condition := NewCondition(node.Value)
		if condition.Kind == CondNever {
			return &plan{kind: planNever}
		}
		return &plan{kind: planField, path: path, condition: condition}
	}
	return &plan{kind: planNever}
}
