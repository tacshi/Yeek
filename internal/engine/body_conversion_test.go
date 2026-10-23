package engine

import (
	"reflect"
	"testing"
)

func TestConvertRequestBody(t *testing.T) {
	form := func(rows ...Object) Object {
		list := []any{}
		for _, row := range rows {
			list = append(list, map[string]any(row))
		}
		return Object{"form": list}
	}
	for _, test := range []struct {
		name     string
		from, to string
		body     Object
		want     Object
	}{
		{
			name: "converts imported JSON GraphQL bodies to GraphQL shape",
			from: "application/json", to: "graphql",
			body: Object{"text": `{"query":"query GetUser($id: ID!) { user(id: $id) { name } }","variables":{"id":"123"},"operationName":"GetUser"}`},
			want: Object{
				"query":         "query GetUser($id: ID!) { user(id: $id) { name } }",
				"variables":     "{\n  \"id\": \"123\"\n}",
				"operationName": "GetUser",
			},
		},
		{
			name: "converts GraphQL bodies to JSON text",
			from: "graphql", to: "application/json",
			body: Object{
				"query":         "query GetUser($id: ID!) { user(id: $id) { name } }",
				"variables":     `{ "id": "123" }`,
				"operationName": "GetUser",
			},
			want: Object{"text": "{\n  \"query\": \"query GetUser($id: ID!) { user(id: $id) { name } }\",\n  \"variables\": {\n    \"id\": \"123\"\n  },\n  \"operationName\": \"GetUser\"\n}"},
		},
		{
			name: "converts urlencoded forms to urlencoded text for text-like bodies",
			from: "application/x-www-form-urlencoded", to: "text/plain",
			body: form(
				Object{"enabled": true, "name": "basic", "value": "aaa"},
				Object{"enabled": true, "name": "funky stuff", "value": "*)%&#$)@ *$#)@&"},
				Object{"enabled": false, "name": "disabled", "value": "hidden"},
				Object{"enabled": true, "name": "", "value": "unnamed"},
			),
			want: Object{"text": "basic=aaa&funky+stuff=*%29%25%26%23%24%29%40+*%24%23%29%40%26"},
		},
		{
			name: "converts urlencoded forms to JSON text for JSON bodies",
			from: "application/x-www-form-urlencoded", to: "application/json",
			body: form(
				Object{"enabled": true, "name": "tag", "value": "one"},
				Object{"enabled": true, "name": "tag", "value": "two"},
				Object{"enabled": true, "name": "limit", "value": "10"},
			),
			want: Object{"text": "{\n  \"tag\": [\n    \"one\",\n    \"two\"\n  ],\n  \"limit\": \"10\"\n}"},
		},
		{
			name: "preserves text when converting to form bodies cannot build form pairs",
			from: "text/xml", to: "application/x-www-form-urlencoded",
			body: Object{"text": "a=1&b=two+words"},
			want: Object{"text": "a=1&b=two+words"},
		},
		{
			name: "preserves JSON text that is not a GraphQL envelope",
			from: "application/json", to: "graphql",
			body: Object{"text": `{"name":"Yeek"}`},
			want: Object{"text": `{"name":"Yeek"}`},
		},
		{
			name: "preserves text when converting to binary cannot build a file body",
			from: "application/json", to: "binary",
			body: Object{"text": `{ "name": "Yeek" }`},
			want: Object{"text": `{ "name": "Yeek" }`},
		},
		{
			name: "clears body when converting to no body",
			from: "application/json", to: "",
			body: Object{"text": `{ "name": "Yeek" }`},
			want: Object{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ConvertRequestBody(test.body, test.from, test.to); !reflect.DeepEqual(got, test.want) {
				t.Errorf("got %#v\nwant %#v", got, test.want)
			}
		})
	}
}

func TestConvertRequestBodyPreservesFormFieldNames(t *testing.T) {
	for _, name := range []string{"__proto__", "constructor", "toString"} {
		body := Object{"form": []any{map[string]any{"enabled": true, "name": name, "value": "hello"}}}
		want := Object{"text": "{\n  \"" + name + "\": \"hello\"\n}"}
		if got := ConvertRequestBody(body, "application/x-www-form-urlencoded", "application/json"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v", name, got)
		}
	}
}

func TestConvertRequestBodyPreservesJSONArraysAndPrimitivesForGraphQL(t *testing.T) {
	for _, text := range []string{"[1,2,3]", `"query"`, "123", "null"} {
		if got := ConvertRequestBody(Object{"text": text}, "application/json", "graphql"); !reflect.DeepEqual(got, Object{"text": text}) {
			t.Errorf("%s: got %#v", text, got)
		}
	}
}
