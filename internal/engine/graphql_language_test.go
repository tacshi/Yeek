package engine

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

const languageSchema = `
schema {query: Query mutation: Mutation subscription: Subscription}
directive @cached(ttl: Int!) on FIELD
interface Node {id: ID!}
type User implements Node {id: ID!, name: String!, oldName: String @deprecated(reason: "Use name"), friend: User}
type Team implements Node {id: ID!, title: String!}
union SearchResult = User | Team
enum Status {ACTIVE INACTIVE}
input Nested {code: Int!}
input Filter {status: Status!, nested: Nested, labels: [String!]}
type Query {user(id: ID!): User, search(filter: Filter, limit: Int = 20): [SearchResult!]!}
type Mutation {rename(id: ID!, name: String!): User}
type Subscription {updates: User}
`

func languageIndex(t *testing.T) *GraphQLIndex {
	t.Helper()
	g, err := LoadGraphQLSchema([]byte(languageSchema))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGraphQLContextCompletion(t *testing.T) {
	g := languageIndex(t)
	for _, tt := range []struct{ query, want, absent string }{
		{`{ us| }`, "user", "name"},
		{`{ user(id:"x") { na| } }`, "name", "user"},
		{`{ person: user(id:"x") { fr| } }`, "friend", "search"},
		{`{ user(id:"x") { friend { na|`, "name", "user"},
		{`mutation Rename { ren|`, "rename", "search"},
		{`subscription { up|`, "updates", "user"},
		{`{ search(li|) { __typename } }`, "limit", "id"},
		{`{ search(filter:{st|}){__typename} }`, "status", "limit"},
		{`{ search(filter:{status:AC|}){__typename} }`, "ACTIVE", "INACTIVE"},
		{`{ search(filter:{status:ACTIVE ne|}){__typename} }`, "nested", "status"},
		{`{ search(filter:{nested:{co|}}){__typename} }`, "code", "status"},
		{`query Get($who: ID!, $count: Int) {user(id:$w|){name}}`, "$who", "$count"},
		{`query Get($who: ID = "1") {user(id:$w|){name}}`, "$who", ""},
		{`query Get($who: ID!, $count: Int) {user(id:|){name}}`, "$who", "$count"},
		{`query Get($filter: Fi|) {search(filter:$filter){__typename}}`, "Filter", "User"},
		{`query Get($ids: [I|]) {user(id:"x"){name}}`, "ID", "User"},
		{`{ user(id:"x") @ca| {name} }`, "cached", "deprecated"},
		{`{ user(id:"x") @cached(tt|) {name} }`, "ttl", "id"},
		{`{search{... on Us| {name}}}`, "User", "Query"},
		{`{search{... us|}} fragment userFields on User {name}`, "userFields", "Team"},
		{`fragment userFields on User { na| }`, "name", "user"},
		{`{user(id:"1"){... @skip(if:false){na|}}}`, "name", "user"},
		{`{ user(id:"日|本") {name}}`, "", "name"},
		{"{\n # us|\n}", "", "user"},
	} {
		t.Run(tt.want+tt.query, func(t *testing.T) {
			before, after, _ := strings.Cut(tt.query, "|")
			source := before + after
			completion := g.Complete(source, utf8.RuneCountInString(before))
			labels := []string{}
			for _, option := range completion.Options {
				labels = append(labels, option.Label)
			}
			if tt.want != "" && !slices.Contains(labels, tt.want) || tt.absent != "" && slices.Contains(labels, tt.absent) {
				t.Fatalf("options=%v", labels)
			}
			if tt.want == "" && len(labels) > 0 {
				t.Fatalf("completion inside string/comment: %v", labels)
			}
		})
	}
}

func TestGraphQLIncompleteQueriesRemainSafe(t *testing.T) {
	g := languageIndex(t)
	for _, source := range []string{`query Get($id: ID!, $f: [Filter!]) {user(id:$id){name} search(filter:{nested:{code:2},status:ACTIVE}){... on User{name}}}`, `query($x: [) = {] "bad`, strings.Repeat("{", 150), `fragment X on User @skip(if:true) {friend{name}}`} {
		r := []rune(source)
		for i := 0; i <= len(r); i++ {
			result := g.Complete(string(r[:i]), i)
			if result.Start < 0 || result.End > i || result.Start > result.End {
				t.Fatalf("invalid completion range at %d: %+v", i, result)
			}
		}
	}
}

func TestGraphQLSchemaValidationAndVariables(t *testing.T) {
	g := languageIndex(t)
	query := `query Get($who: ID!) { user(id:$who) { name } }`
	if got := GraphQLDiagnostics(g, query, `{"who":"1"}`, "Get"); len(got) > 0 {
		t.Fatal(got)
	}
	for _, tt := range []struct {
		query, variables, operation, contains string
		vars                                  bool
	}{
		{`{ user(id:"日") { unknown } }`, `{}`, "", "Cannot query field", false},
		{query, `{}`, "Get", "must be defined", true},
		{query, `[]`, "Get", "JSON object", true},
		{query, `null`, "Get", "JSON object", true},
		{query, `{"who":"1"}`, "Missing", "does not exist", false},
		{`query { user( }`, `{}`, "", "Expected", false},
	} {
		issues := GraphQLDiagnostics(g, tt.query, tt.variables, tt.operation)
		if len(issues) == 0 || !strings.Contains(issues[0].Message, tt.contains) || issues[0].Variables != tt.vars {
			t.Fatalf("%q => %+v", tt.query, issues)
		}
		if !tt.vars && issues[0].End > utf8.RuneCountInString(tt.query) {
			t.Fatal("byte offsets used for native diagnostics")
		}
	}
}

func TestGraphQLIntrospectionIndex(t *testing.T) {
	content := []byte(`{"data":{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[{"name":"hello","description":"A greeting","args":[{"name":"who","type":{"kind":"NON_NULL","ofType":{"kind":"SCALAR","name":"String"}}}],"type":{"kind":"SCALAR","name":"String"}}]}],"directives":[]}}}`)
	g, err := LoadGraphQLSchema(content)
	if err != nil {
		t.Fatal(err)
	}
	if field := g.Schema.Query.Fields.ForName("hello"); field == nil || field.Arguments.ForName("who").Type.String() != "String!" || field.Description != "A greeting" {
		t.Fatal(field)
	}
	if _, err = LoadGraphQLSchema([]byte(`{"types":[{"kind":"OBJECT","name":"bad name"}]}`)); err == nil {
		t.Fatal("invalid schema accepted")
	}
	for _, source := range []string{`{"__schema":{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[{"name":"hello","type":{"kind":"SCALAR","name":"String"}}]}]}}`, `{"queryType":{"name":"Query"},"types":[{"kind":"OBJECT","name":"Query","fields":[{"name":"hello","type":{"kind":"SCALAR","name":"String"}}]}]}`} {
		if _, err := LoadGraphQLSchema([]byte(source)); err != nil {
			t.Fatal("alternate introspection JSON shape", err)
		}
	}
}
