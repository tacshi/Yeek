package engine

import (
	"bytes"
	"cmp"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

// GraphQLIndex is an immutable schema shared by documentation, completion, and
// validation. Construct it once for each fetched or imported schema.
type GraphQLIndex struct{ Schema *ast.Schema }
type GraphQLDiagnostic struct {
	Message                  string
	Line, Column, Start, End int
	Variables                bool
}

func LoadGraphQLSchema(content []byte) (*GraphQLIndex, error) {
	if len(content) > 16<<20 {
		return nil, errors.New("GraphQL schema exceeds 16 MiB")
	}
	content = bytes.TrimSpace(content)
	if len(content) == 0 {
		return nil, errors.New("GraphQL schema is empty")
	}
	if content[0] == '{' {
		var document Object
		if err := json.Unmarshal(content, &document); err != nil {
			return nil, fmt.Errorf("schema JSON: %w", err)
		}
		schema := obj(obj(document, "data"), "__schema")
		if len(schema) == 0 {
			schema = obj(document, "__schema")
		}
		if len(schema) == 0 && document["types"] != nil {
			schema = document
		}
		if len(schema) == 0 {
			return nil, errors.New("JSON has no GraphQL introspection schema")
		}
		sdl, err := graphQLSchemaSDL(schema)
		if err != nil {
			return nil, err
		}
		content = []byte(sdl)
	}
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema", Input: string(content)})
	if err != nil {
		return nil, fmt.Errorf("GraphQL schema: %w", err)
	}
	if schema.Query == nil {
		return nil, errors.New("GraphQL schema has no query root")
	}
	return &GraphQLIndex{Schema: schema}, nil
}

func graphQLName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !graphQLWord(r) || i == 0 && r >= '0' && r <= '9' {
			return false
		}
	}
	return true
}

func graphQLSchemaSDL(schema Object) (string, error) {
	var out strings.Builder
	var failure error
	name := func(value string) string {
		if !graphQLName(value) && failure == nil {
			failure = fmt.Errorf("invalid GraphQL name %q", value)
		}
		return value
	}
	description := func(value string) {
		if value != "" {
			quoted, _ := json.Marshal(value)
			out.Write(quoted)
			out.WriteByte('\n')
		}
	}
	var typeName func(Object, int) string
	typeName = func(typ Object, depth int) string {
		if depth > 32 {
			failure = errors.New("schema type nesting exceeds 32 levels")
			return ""
		}
		switch str(typ, "kind") {
		case "NON_NULL":
			return typeName(obj(typ, "ofType"), depth+1) + "!"
		case "LIST":
			return "[" + typeName(obj(typ, "ofType"), depth+1) + "]"
		default:
			return name(str(typ, "name"))
		}
	}
	deprecated := func(field Object) {
		if boolean(field, "isDeprecated") {
			out.WriteString(" @deprecated(reason: " + strconv.Quote(cmp.Or(str(field, "deprecationReason"), "No longer supported")) + ")")
		}
	}
	defaultValue := func(field Object) {
		if value, ok := field["defaultValue"].(string); ok && value != "" {
			out.WriteString(" = " + value)
		}
	}
	args := func(fields []Object) {
		if len(fields) == 0 {
			return
		}
		out.WriteByte('(')
		for i, field := range fields {
			if i > 0 {
				out.WriteString(", ")
			}
			description(str(field, "description"))
			out.WriteString(name(str(field, "name")) + ": " + typeName(obj(field, "type"), 0))
			defaultValue(field)
		}
		out.WriteByte(')')
	}
	out.WriteString("schema {\n")
	for _, operation := range []string{"query", "mutation", "subscription"} {
		if typ := str(obj(schema, operation+"Type"), "name"); typ != "" {
			out.WriteString(operation + ": " + name(typ) + "\n")
		}
	}
	out.WriteString("}\n")
	for _, typ := range objects(array(schema, "types")) {
		typeID := str(typ, "name")
		if strings.HasPrefix(typeID, "__") || slices.Contains([]string{"String", "Int", "Float", "Boolean", "ID"}, typeID) {
			continue
		}
		description(str(typ, "description"))
		keyword := map[string]string{"OBJECT": "type", "INTERFACE": "interface", "INPUT_OBJECT": "input", "ENUM": "enum", "UNION": "union", "SCALAR": "scalar"}[str(typ, "kind")]
		if keyword == "" {
			return "", fmt.Errorf("unknown schema kind %q", str(typ, "kind"))
		}
		out.WriteString(keyword + " " + name(typeID))
		interfaces := objects(array(typ, "interfaces"))
		if len(interfaces) > 0 {
			out.WriteString(" implements ")
			for i, v := range interfaces {
				if i > 0 {
					out.WriteString(" & ")
				}
				out.WriteString(name(str(v, "name")))
			}
		}
		switch keyword {
		case "scalar":
			out.WriteByte('\n')
		case "union":
			out.WriteString(" = ")
			for i, v := range objects(array(typ, "possibleTypes")) {
				if i > 0 {
					out.WriteString(" | ")
				}
				out.WriteString(name(str(v, "name")))
			}
			out.WriteByte('\n')
		case "enum":
			out.WriteString(" {\n")
			for _, v := range objects(array(typ, "enumValues")) {
				description(str(v, "description"))
				out.WriteString(name(str(v, "name")))
				deprecated(v)
				out.WriteByte('\n')
			}
			out.WriteString("}\n")
		default:
			out.WriteString(" {\n")
			key := "fields"
			if keyword == "input" {
				key = "inputFields"
			}
			for _, field := range objects(array(typ, key)) {
				description(str(field, "description"))
				out.WriteString(name(str(field, "name")))
				args(objects(array(field, "args")))
				out.WriteString(": " + typeName(obj(field, "type"), 0))
				defaultValue(field)
				deprecated(field)
				out.WriteByte('\n')
			}
			out.WriteString("}\n")
		}
	}
	for _, directive := range objects(array(schema, "directives")) {
		description(str(directive, "description"))
		out.WriteString("directive @" + name(str(directive, "name")))
		args(objects(array(directive, "args")))
		if boolean(directive, "isRepeatable") {
			out.WriteString(" repeatable")
		}
		out.WriteString(" on ")
		for i, location := range array(directive, "locations") {
			if i > 0 {
				out.WriteString(" | ")
			}
			out.WriteString(name(fmt.Sprint(location)))
		}
		out.WriteByte('\n')
	}
	return out.String(), failure
}

func GraphQLOperations(source string) []string {
	doc, err := parser.ParseQuery(&ast.Source{Input: source})
	if err != nil {
		return nil
	}
	names := []string{}
	for _, op := range doc.Operations {
		if op.Name != "" {
			names = append(names, op.Name)
		}
	}
	return names
}

func GraphQLDiagnostics(index *GraphQLIndex, query, variables, operation string) []GraphQLDiagnostic {
	// Dynamic query fragments cannot be validated before template evaluation.
	if strings.Contains(query, "${[") || strings.Contains(query, "{{") {
		return nil
	}
	source := &ast.Source{Name: "query", Input: query}
	doc, err := parser.ParseQuery(source)
	var issues gqlerror.List
	if err != nil {
		issues = append(issues, gqlerror.WrapIfUnwrapped(err))
	} else if index != nil {
		issues = validator.ValidateWithRules(index.Schema, doc, nil)
	}
	result := []GraphQLDiagnostic{}
	for _, issue := range issues {
		line, column := 1, 1
		if len(issue.Locations) > 0 {
			line, column = issue.Locations[0].Line, issue.Locations[0].Column
		}
		start, end := graphQLDiagnosticRange(query, line, column)
		result = append(result, GraphQLDiagnostic{Message: issue.Message, Line: line, Column: column, Start: start, End: end})
	}
	if doc == nil || len(issues) > 0 {
		return result
	}
	if operation != "" && doc.Operations.ForName(operation) == nil {
		return append(result, GraphQLDiagnostic{Message: "Operation " + strconv.Quote(operation) + " does not exist", Line: 1, Column: 1, End: min(1, utf8.RuneCountInString(query))})
	}
	if strings.TrimSpace(variables) == "" {
		variables = "{}"
	}
	if strings.Contains(variables, "${[") || strings.Contains(variables, "{{") {
		return result
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(variables), &values); err != nil {
		return append(result, GraphQLDiagnostic{Message: "Variables must be a JSON object: " + err.Error(), Line: 1, Column: 1, Variables: true})
	}
	if values == nil {
		return append(result, GraphQLDiagnostic{Message: "Variables must be a JSON object", Line: 1, Column: 1, Variables: true})
	}
	if index != nil {
		op := doc.Operations.ForName(operation)
		if op == nil && len(doc.Operations) == 1 {
			op = doc.Operations[0]
		}
		if op != nil {
			if _, err := validator.VariableValues(index.Schema, op, values); err != nil {
				result = append(result, GraphQLDiagnostic{Message: err.Error(), Line: 1, Column: 1, Variables: true})
			}
		}
	}
	return result
}

func graphQLDiagnosticRange(source string, line, column int) (int, int) {
	r := []rune(source)
	at, row := 0, 1
	for at < len(r) && row < line {
		if r[at] == '\n' {
			row++
		}
		at++
	}
	at = min(len(r), at+max(0, column-1))
	end := at
	for end < len(r) && (r[end] == '_' || r[end] >= 'a' && r[end] <= 'z' || r[end] >= 'A' && r[end] <= 'Z' || r[end] >= '0' && r[end] <= '9') {
		end++
	}
	return at, min(len(r), max(at+1, end))
}
