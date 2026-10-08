package engine

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/lexer"
)

type GraphQLSuggestion struct {
	Label, Insert, Detail, Description, TypeName, ParentType string
	Deprecated                                               bool
	// Caret is relative to Insert, in runes. -1 puts it at the end.
	Caret int
}
type GraphQLCompletion struct {
	Start, End int
	Options    []GraphQLSuggestion
}
type graphQLContext struct {
	hasDefault bool
	kind       string
	parent     *ast.Definition
	typ        *ast.Type
	args       ast.ArgumentDefinitionList
	used       map[string]bool
	location   ast.DirectiveLocation
	dollar     bool
}
type graphQLCursor struct {
	variableDefaults map[string]bool
	index            *GraphQLIndex
	tokens           []lexer.Token
	at               int
	context          graphQLContext
	variables        map[string]*ast.Type
	depth            int
}

func graphQLWord(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func graphQLTokens(source string, caret int) ([]lexer.Token, bool) {
	l := lexer.New(&ast.Source{Input: source})
	tokens := []lexer.Token{}
	for {
		token, err := l.ReadToken()
		if token.Pos.Start < caret && caret <= token.Pos.End && (token.Kind == lexer.Comment || token.Kind == lexer.String || token.Kind == lexer.BlockString || err != nil) {
			return nil, false
		}
		if token.Pos.Start >= caret || token.Kind == lexer.EOF || err != nil {
			break
		}
		if token.Kind != lexer.Comment {
			tokens = append(tokens, token)
		}
	}
	return tokens, true
}

// Complete tolerates unfinished selections, argument lists, input objects, and
// variable declarations. It never requires the user to finish a valid query
// before names from its schema become available.
func (g *GraphQLIndex) Complete(source string, caret int) GraphQLCompletion {
	runes := []rune(source)
	caret = max(0, min(caret, len(runes)))
	start, end := caret, caret
	for start > 0 && graphQLWord(runes[start-1]) {
		start--
	}
	for end < len(runes) && graphQLWord(runes[end]) {
		end++
	}
	result := GraphQLCompletion{Start: start, End: end}
	if _, ok := graphQLTokens(source, caret); !ok {
		return result
	}
	tokens, ok := graphQLTokens(string(runes[:start]), start)
	if !ok {
		return result
	}
	p := graphQLCursor{index: g, tokens: tokens, variables: map[string]*ast.Type{}, variableDefaults: map[string]bool{}}
	p.document()
	options := p.suggestions(source)
	prefix := strings.ToLower(string(runes[start:caret]))
	for _, option := range options {
		if strings.HasPrefix(strings.ToLower(strings.TrimPrefix(option.Label, "$")), prefix) {
			result.Options = append(result.Options, option)
		}
	}
	slices.SortStableFunc(result.Options, func(a, b GraphQLSuggestion) int {
		return cmp.Or(cmp.Compare(boolInt(a.Deprecated), boolInt(b.Deprecated)), cmp.Compare(boolInt(strings.HasPrefix(a.Label, "__")), boolInt(strings.HasPrefix(b.Label, "__"))), strings.Compare(a.Label, b.Label))
	})
	return result
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func (p *graphQLCursor) peek() lexer.Type {
	if p.at >= len(p.tokens) {
		return lexer.EOF
	}
	return p.tokens[p.at].Kind
}
func (p *graphQLCursor) take(kind lexer.Type) bool {
	if p.peek() != kind {
		return false
	}
	p.at++
	return true
}
func (p *graphQLCursor) name() string {
	if p.peek() != lexer.Name {
		return ""
	}
	value := p.tokens[p.at].Value
	p.at++
	return value
}
func (p *graphQLCursor) lookup(name string) *ast.Definition {
	if p.index == nil {
		return nil
	}
	return p.index.Schema.Types[name]
}
func (p *graphQLCursor) root(operation string) *ast.Definition {
	if p.index == nil {
		return nil
	}
	switch operation {
	case "mutation":
		return p.index.Schema.Mutation
	case "subscription":
		return p.index.Schema.Subscription
	default:
		return p.index.Schema.Query
	}
}
func (p *graphQLCursor) stop(context graphQLContext) bool {
	if p.peek() == lexer.EOF {
		p.context = context
		return true
	}
	return false
}

func (p *graphQLCursor) document() {
	for p.peek() != lexer.EOF {
		if p.peek() == lexer.BraceL {
			if p.selection(p.root("query")) {
				return
			}
			continue
		}
		name := p.name()
		switch name {
		case "fragment":
			p.name()
			if p.peek() == lexer.EOF {
				p.context = graphQLContext{kind: "on"}
				return
			}
			p.name()
			if p.stop(graphQLContext{kind: "types"}) {
				return
			}
			typ := p.lookup(p.name())
			if p.directives(ast.LocationFragmentDefinition) || p.selection(typ) {
				return
			}
		case "query", "mutation", "subscription":
			p.variables = map[string]*ast.Type{}
			p.variableDefaults = map[string]bool{}
			p.name()
			if p.take(lexer.ParenL) && p.variableDefinitions() {
				return
			}
			if p.directives(ast.DirectiveLocation(strings.ToUpper(name))) || p.selection(p.root(name)) {
				return
			}
		default:
			if p.peek() != lexer.EOF {
				p.at++
			}
		}
	}
	p.context = graphQLContext{kind: "document"}
}
func (p *graphQLCursor) selection(parent *ast.Definition) bool {
	if p.stop(graphQLContext{kind: "selection", parent: parent}) {
		return true
	}
	if !p.take(lexer.BraceL) {
		return false
	}
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		p.context = graphQLContext{}
		p.at = len(p.tokens)
		return true
	}
	for {
		if p.stop(graphQLContext{kind: "fields", parent: parent}) {
			return true
		}
		if p.take(lexer.BraceR) {
			return false
		}
		if p.take(lexer.Spread) {
			if p.stop(graphQLContext{kind: "fragments", parent: parent}) {
				return true
			}
			if p.peek() == lexer.At || p.peek() == lexer.BraceL {
				if p.directives(ast.LocationInlineFragment) || p.selection(parent) {
					return true
				}
				continue
			}
			if p.name() == "on" {
				if p.stop(graphQLContext{kind: "types", parent: parent}) {
					return true
				}
				typ := p.lookup(p.name())
				if p.directives(ast.LocationInlineFragment) || p.selection(typ) {
					return true
				}
			} else if p.directives(ast.LocationFragmentSpread) {
				return true
			}
			continue
		}
		before := p.at
		name := p.name()
		if p.take(lexer.Colon) {
			if p.stop(graphQLContext{kind: "fields", parent: parent}) {
				return true
			}
			name = p.name()
		}
		var field *ast.FieldDefinition
		if parent != nil {
			field = parent.Fields.ForName(name)
		}
		if name == "__typename" {
			field = &ast.FieldDefinition{Name: name, Type: ast.NamedType("String", nil)}
		}
		var arguments ast.ArgumentDefinitionList
		var child *ast.Definition
		if field != nil {
			arguments = field.Arguments
			child = p.lookup(field.Type.Name())
		}
		if p.take(lexer.ParenL) && p.arguments(arguments) {
			return true
		}
		if p.directives(ast.LocationField) {
			return true
		}
		if p.peek() == lexer.BraceL && p.selection(child) {
			return true
		}
		if p.at == before {
			p.at++
		}
	}
}
func (p *graphQLCursor) arguments(args ast.ArgumentDefinitionList) bool {
	used := map[string]bool{}
	for {
		if p.stop(graphQLContext{kind: "arguments", args: args, used: used}) {
			return true
		}
		if p.take(lexer.ParenR) {
			return false
		}
		key := p.name()
		used[key] = true
		if key == "" {
			p.at++
			continue
		}
		if !p.take(lexer.Colon) {
			continue
		}
		var typ *ast.Type
		hasDefault := false
		if arg := args.ForName(key); arg != nil {
			typ = arg.Type
			hasDefault = arg.DefaultValue != nil
		}
		if p.value(typ, hasDefault) {
			return true
		}
	}
}
func (p *graphQLCursor) value(typ *ast.Type, defaults ...bool) bool {
	hasDefault := len(defaults) > 0 && defaults[0]
	if p.stop(graphQLContext{kind: "value", typ: typ, hasDefault: hasDefault}) {
		return true
	}
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		p.context = graphQLContext{}
		p.at = len(p.tokens)
		return true
	}
	if p.take(lexer.Dollar) {
		if p.stop(graphQLContext{kind: "variables", typ: typ, dollar: true, hasDefault: hasDefault}) {
			return true
		}
		p.name()
		return false
	}
	if p.take(lexer.BracketL) {
		if typ != nil && typ.Elem != nil {
			typ = typ.Elem
		}
		for !p.take(lexer.BracketR) {
			before := p.at
			if p.value(typ) {
				return true
			}
			if p.at == before {
				p.at++
			}
		}
		return false
	}
	if p.take(lexer.BraceL) {
		var parent *ast.Definition
		if typ != nil {
			parent = p.lookup(typ.Name())
		}
		used := map[string]bool{}
		for {
			if p.stop(graphQLContext{kind: "inputFields", parent: parent, used: used}) {
				return true
			}
			if p.take(lexer.BraceR) {
				return false
			}
			key := p.name()
			if key == "" {
				p.at++
				continue
			}
			used[key] = true
			if !p.take(lexer.Colon) {
				continue
			}
			var fieldType *ast.Type
			fieldDefault := false
			if parent != nil {
				if field := parent.Fields.ForName(key); field != nil {
					fieldType = field.Type
					fieldDefault = field.DefaultValue != nil
				}
			}
			if p.value(fieldType, fieldDefault) {
				return true
			}
		}
	}
	p.at++
	return false
}
func (p *graphQLCursor) variableDefinitions() bool {
	for {
		if p.stop(graphQLContext{kind: "variableDeclaration"}) {
			return true
		}
		if p.take(lexer.ParenR) {
			return false
		}
		if !p.take(lexer.Dollar) {
			p.at++
			continue
		}
		name := p.name()
		if !p.take(lexer.Colon) {
			continue
		}
		typ, stopped := p.variableType()
		if stopped {
			return true
		}
		p.variables[name] = typ
		if p.take(lexer.Equals) {
			p.variableDefaults[name] = p.peek() != lexer.EOF && (p.peek() != lexer.Name || p.tokens[p.at].Value != "null")
			if p.value(typ) {
				return true
			}
		}
		if p.directives(ast.LocationVariableDefinition) {
			return true
		}
	}
}
func (p *graphQLCursor) variableType() (*ast.Type, bool) {
	if p.stop(graphQLContext{kind: "inputTypes"}) {
		return nil, true
	}
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 128 {
		p.context = graphQLContext{}
		p.at = len(p.tokens)
		return nil, true
	}
	var typ *ast.Type
	if p.take(lexer.BracketL) {
		child, stop := p.variableType()
		if stop {
			return nil, true
		}
		if child == nil {
			return nil, false
		}
		p.take(lexer.BracketR)
		typ = ast.ListType(child, nil)
	} else {
		value := p.name()
		if value == "" {
			p.at++
			return nil, false
		}
		typ = ast.NamedType(value, nil)
	}
	if p.take(lexer.Bang) {
		typ.NonNull = true
	}
	return typ, false
}
func (p *graphQLCursor) directives(location ast.DirectiveLocation) bool {
	for p.take(lexer.At) {
		if p.stop(graphQLContext{kind: "directives", location: location}) {
			return true
		}
		name := p.name()
		var args ast.ArgumentDefinitionList
		if p.index != nil {
			if directive := p.index.Schema.Directives[name]; directive != nil {
				args = directive.Arguments
			}
		}
		if p.take(lexer.ParenL) && p.arguments(args) {
			return true
		}
	}
	return false
}

func (p *graphQLCursor) suggestions(source string) []GraphQLSuggestion {
	ctx := p.context
	options := []GraphQLSuggestion{}
	add := func(label, insert, detail, description string) {
		options = append(options, GraphQLSuggestion{Label: label, Insert: insert, Detail: detail, Description: description, Caret: -1})
	}
	field := func(f *ast.FieldDefinition, input bool) {
		if ctx.used[f.Name] {
			return
		}
		insert := f.Name
		if input {
			insert += ": "
		}
		add(f.Name, insert, f.Type.String(), f.Description)
		last := &options[len(options)-1]
		last.Deprecated = f.Directives.ForName("deprecated") != nil
		last.TypeName = f.Type.Name()
		if ctx.parent != nil {
			last.ParentType = ctx.parent.Name
		}
	}
	variables := func() {
		for name, typ := range p.variables {
			if typ == nil {
				continue
			}
			if ctx.typ != nil {
				expected := *ctx.typ
				if ctx.hasDefault || p.variableDefaults[name] {
					expected.NonNull = false
				}
				if !typ.IsCompatible(&expected) {
					continue
				}
			}
			insert := "$" + name
			if ctx.dollar {
				insert = name
			}
			add("$"+name, insert, typ.String(), "Operation variable")
		}
	}
	switch ctx.kind {
	case "document":
		for _, keyword := range []string{"query", "mutation", "subscription", "fragment"} {
			add(keyword, keyword+" ", "operation", "")
		}
	case "selection":
		add("{", "{\n  \n}", "selection set", "")
		options[0].Caret = 4
	case "fields", "inputFields":
		if ctx.parent != nil {
			for _, f := range ctx.parent.Fields {
				field(f, ctx.kind == "inputFields")
			}
		}
		if ctx.kind == "fields" && ctx.parent != nil {
			add("__typename", "__typename", "String!", "Runtime object type")
		}
	case "arguments":
		for _, arg := range ctx.args {
			if ctx.used[arg.Name] {
				continue
			}
			add(arg.Name, arg.Name+": ", arg.Type.String(), arg.Description)
		}
	case "variables":
		variables()
	case "value":
		variables()
		if ctx.typ == nil {
			break
		}
		typ := p.lookup(ctx.typ.Name())
		if !ctx.typ.NonNull {
			add("null", "null", ctx.typ.String(), "")
		}
		if ctx.typ.Elem != nil {
			add("[]", "[]", ctx.typ.String(), "")
			options[len(options)-1].Caret = 1
		}
		if typ == nil {
			break
		}
		switch typ.Kind {
		case ast.Enum:
			for _, value := range typ.EnumValues {
				add(value.Name, value.Name, typ.Name, value.Description)
				options[len(options)-1].Deprecated = value.Directives.ForName("deprecated") != nil
			}
		case ast.InputObject:
			add("{}", "{}", typ.Name, typ.Description)
			options[len(options)-1].Caret = 1
		case ast.Scalar:
			switch typ.Name {
			case "Boolean":
				add("true", "true", typ.Name, "")
				add("false", "false", typ.Name, "")
			case "String", "ID":
				add(`""`, `""`, typ.Name, "")
				options[len(options)-1].Caret = 1
			case "Int", "Float":
				add("0", "0", typ.Name, "")
			}
		}
	case "types", "inputTypes":
		if p.index == nil {
			break
		}
		for _, typ := range p.index.Schema.Types {
			if strings.HasPrefix(typ.Name, "__") {
				continue
			}
			if ctx.kind == "inputTypes" && !typ.IsInputType() || ctx.kind == "types" && (!typ.IsCompositeType() || !p.overlap(ctx.parent, typ)) {
				continue
			}
			add(typ.Name, typ.Name, string(typ.Kind), typ.Description)
		}
	case "fragments":
		add("on", "on ", "inline fragment", "")
		tokens, _ := graphQLTokens(source, utf8.RuneCountInString(source)+1)
		for i := 0; i+3 < len(tokens); i++ {
			if tokens[i].Value == "fragment" && tokens[i+2].Value == "on" {
				typ := p.lookup(tokens[i+3].Value)
				if typ != nil && p.overlap(ctx.parent, typ) {
					add(tokens[i+1].Value, tokens[i+1].Value, typ.Name, "Fragment")
				}
			}
		}
	case "on":
		add("on", "on ", "type condition", "")
	case "directives":
		if p.index != nil {
			for _, directive := range p.index.Schema.Directives {
				if slices.Contains(directive.Locations, ctx.location) {
					add(directive.Name, directive.Name, "directive", directive.Description)
				}
			}
		}
	}
	return options
}
func (p *graphQLCursor) overlap(a, b *ast.Definition) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	if a.Name == b.Name {
		return true
	}
	if p.index == nil {
		return false
	}
	for _, left := range p.index.Schema.GetPossibleTypes(a) {
		for _, right := range p.index.Schema.GetPossibleTypes(b) {
			if left.Name == right.Name {
				return true
			}
		}
	}
	return false
}
