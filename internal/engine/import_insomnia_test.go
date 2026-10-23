package engine

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestInsomniaFixtures(t *testing.T) {
	inputs, _ := filepath.Glob("testdata/insomnia/*.input.*")
	if len(inputs) == 0 {
		t.Fatal("no fixtures")
	}
	for _, input := range inputs {
		t.Run(filepath.Base(input), func(t *testing.T) {
			data, err := os.ReadFile(input) // #nosec G304 -- test fixtures.
			if err != nil {
				t.Fatal(err)
			}
			output := strings.SplitN(input, ".input.", 2)[0] + ".output.json"
			expected, err := os.ReadFile(output) // #nosec G304 -- test fixtures.
			if err != nil {
				t.Fatal(err)
			}
			var want struct{ Resources map[string][]map[string]any }
			if err := json.Unmarshal(expected, &want); err != nil {
				t.Fatal(err)
			}
			models, err := ParseImport(data)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]map[string]any{}
			for _, m := range models {
				for collection, kind := range resourceKinds {
					if str(m, "model") == kind {
						got[collection] = append(got[collection], normalizeJSON(t, m))
					}
				}
			}
			for collection, items := range want.Resources {
				for _, item := range items {
					if item["bodyType"] == "other" {
						item["bodyType"] = "text/plain"
					}
				}
				if len(items) == 0 && len(got[collection]) == 0 {
					continue
				}
				if !reflect.DeepEqual(got[collection], items) {
					g, _ := json.Marshal(got[collection], json.Deterministic(true))
					w, _ := json.Marshal(items, json.Deterministic(true))
					t.Errorf("%s:\n got %s\nwant %s", collection, g, w)
				}
			}
		})
	}
}

func normalizeJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInsomniaTemplates(t *testing.T) {
	ids := map[string]bool{"GENERATE_ID::req_login": true}
	convert := func(text string) string { return convertTemplateSyntax(text, ids).(string) }
	enc := insomniaArgument
	for _, input := range []string{"{{ _.token }}", "{{ token }}", "{{ _['token'] }}", `{{ _ [ "token" ] }}`} {
		if got := convert(input); got != "${[ token ]}" {
			t.Errorf("%s: %s", input, got)
		}
	}
	for _, input := range []string{
		"{{ _.token | default('fallback') }}", "{{ _['two words'] }}", "{{ _['a']['b'] }}", "{{ _.a + _.b }}", "{{ true }}",
		"{% uuid 'v7' %}", "{% faker 'name' %}", "{% dopplersecret 'project', 'config', 'secret' %}",
		"{% response 'body', 'req_missing', '$.token', 'always', 60 %}", "{% response 'body', 'req_login', '$.token', 'unknown', 60 %}",
		"{% response 'body', 'req_login', '$.token', 'always', -1 %}", "{% response 'body' garbage, 'req_login', '$.token', 'always', 60 %}",
		"{% response 'body', 'req_login', 'b64::!!::nonce', 'always', 60 %}", "{% response 'body', 'req_login', '$.token', 'always', 60, %}",
		"{% response 'body', 'req_login', '$.tokens[*]', 'always', 60 %}", "{% response 'body', 'req_login', '$..token', 'always', 60 %}",
		"{% response 'body', 'req_login', '/root/token', 'always', 60 %}", "{% response 'body', 'req_login', '', 'never', 0 %}",
		"{% response 'body', 'req_login', '$.token', 'when-expired', 0 %}", "{% response 'url', 'req_login', '', 'never', 0 %}",
		"{% os 'arch' %}", "{% file '/tmp/token.txt' %}", "{% jsonpath '{}', '$.a' %}", "{% request 'url' %}", "{% now 'quarter' %}",
		"{% uuid 'v4', 'extra' %}", "{% base64 'encode' %}", "{% cookie 'https://example.com' %}", "{% prompt %}", "{% timestamp 'ms' %}",
	} {
		if got := convert(input); got != input {
			t.Errorf("preserves %s: %s", input, got)
		}
	}
	login := enc("GENERATE_ID::req_login")
	for input, want := range map[string]string{
		"{% uuid 'v1' %}": "${[ uuid.v1() ]}", "{% uuid 'v4' %}": "${[ uuid.v4() ]}", "{% faker 'randomUUID' %}": "${[ uuid.v4() ]}",
		"{% uuid %}": "${[ uuid.v4() ]}",
		"{% now %}":  "${[ timestamp.iso8601() ]}", "{% now 'iso-8601' %}": "${[ timestamp.iso8601() ]}", "{% now 'ISO-8601' %}": "${[ timestamp.iso8601() ]}",
		"{% now 'unix' %}": "${[ timestamp.unix() ]}", "{% now 'seconds' %}": "${[ timestamp.unix() ]}", "{% now 's' %}": "${[ timestamp.unix() ]}",
		"{% now 'millis' %}": "${[ timestamp.unixMillis() ]}", "{% now 'ms' %}": "${[ timestamp.unixMillis() ]}", "{% timestamp %}": "${[ timestamp.unixMillis() ]}",
		"{% now 'custom', 'YYYY-MM-DD HH:mm:ss' %}":                          "${[ timestamp.format(format=" + enc("YYYY-MM-DD HH:mm:ss") + ") ]}",
		"{% now 'custom' %}":                                                 "${[ timestamp.format(format=" + enc("") + ") ]}",
		"{% base64 'encode', 'normal', 'hello' %}":                           "${[ base64.encode(encoding='base64', value=" + enc("hello") + ") ]}",
		"{% base64 'encode', 'url', 'hello' %}":                              "${[ base64.encode(encoding='base64url', value=" + enc("hello") + ") ]}",
		"{% base64 'encode', 'hex', 'hello' %}":                              "${[ base64.encode(encoding='base64', value=" + enc("hello") + ") ]}",
		"{% base64 'encode', 'hello' %}":                                     "${[ base64.encode(encoding='base64', value=" + enc("hello") + ") ]}",
		"{% base64 'decode', 'normal', 'hello' %}":                           "${[ base64.decode(value=" + enc("hello") + ") ]}",
		"{% base64 'decode', 'hello' %}":                                     "${[ base64.decode(value=" + enc("hello") + ") ]}",
		"{% base64 'encode', 'normal' %}":                                    "${[ base64.encode(encoding='base64', value=" + enc("") + ") ]}",
		"{% base64 'encode', 'normal', '{{ _.secret }}' %}":                  "${[ base64.encode(encoding='base64', value=" + enc("${[ secret ]}") + ") ]}",
		"{% hash 'md5', 'hex', 'text' %}":                                    "${[ hash.md5(input=" + enc("text") + ", encoding='hex') ]}",
		"{% hash 'sha1', 'hex', 'text' %}":                                   "${[ hash.sha1(input=" + enc("text") + ", encoding='hex') ]}",
		"{% hash 'sha256', 'hex', 'text' %}":                                 "${[ hash.sha256(input=" + enc("text") + ", encoding='hex') ]}",
		"{% hash 'sha512', 'hex', 'text' %}":                                 "${[ hash.sha512(input=" + enc("text") + ", encoding='hex') ]}",
		"{% hash 'sha256', 'base64', 'text' %}":                              "${[ hash.sha256(input=" + enc("text") + ", encoding='base64') ]}",
		"{% hash %}":                                                         "${[ hash.md5(input=" + enc("") + ", encoding='hex') ]}",
		"{% hash 'sha1' %}":                                                  "${[ hash.sha1(input=" + enc("") + ", encoding='hex') ]}",
		"{% hash 'sha384', 'hex', 'text' %}":                                 "${[ hash.sha256(input=" + enc("text") + ", encoding='hex') ]}",
		"{% cookie 'https://api.example.com/login', 'session' %}":            "${[ cookie.value(name=" + enc("session") + ", domain=" + enc("api.example.com") + ") ]}",
		"{% cookie '{{ _.base_url }}', 'session' %}":                         "${[ cookie.value(name=" + enc("session") + ", domain=" + enc("${[ base_url ]}") + ") ]}",
		"{% cookie '', 'session' %}":                                         "${[ cookie.value(name=" + enc("session") + ") ]}",
		"{% prompt 'Password' %}":                                            "${[ prompt.text(label=" + enc("Password") + ", title=" + enc("Password") + ") ]}",
		"{% prompt 'Login', 'Username', 'admin', 'user-key', false, true %}": "${[ prompt.text(label=" + enc("Username") + ", store='forever', namespace=" + enc("${[ctx.workspace()]}") + ", key=" + enc("user-key") + ", title=" + enc("Login") + ", defaultValue=" + enc("admin") + ") ]}",
		"{% prompt 'Login', 'Password', 'hunter2', 'pw-key', true, true %}":  "${[ prompt.text(label=" + enc("Password") + ", title=" + enc("Login") + ", defaultValue=" + enc("hunter2") + ", password=true) ]}",
		"{% prompt 'Login', 'Password', '', '', true %}":                     "${[ prompt.text(label=" + enc("Password") + ", title=" + enc("Login") + ", password=true) ]}",
		"{% prompt 'Login', 'Password', '', '', false, true %}":              "${[ prompt.text(label=" + enc("Password") + ", title=" + enc("Login") + ") ]}",
		"{% response 'raw', 'req_login' %}":                                  "${[ response.body.raw(request=" + login + ", behavior='never', ttl='0') ]}",
	} {
		if got := convert(input); got != want {
			t.Errorf("%s:\n got %s\nwant %s", input, got, want)
		}
	}
	for source, target := range map[string]string{"never": "never", "no-history": "smart", "always": "always", "when-expired": "ttl"} {
		want := "${[ response.body.path(request=" + login + ", behavior='" + target + "', ttl='60', path=" + enc("$.token") + ", result='first') ]}"
		if got := convert("{% response 'body', 'req_login', '$.token', '" + source + "', 60 %}"); got != want {
			t.Errorf("%s: %s", source, got)
		}
	}
	path := "$.clé[0].token"
	b64 := "JC5jbMOpWzBdLnRva2Vu"
	for _, input := range []string{"{% response 'body', 'req_login', 'b64::" + b64 + "::nonce', 'never', 0 %}", "{% response 'body', 'req_login', '" + path + "', 'never', 0 %}"} {
		if got := convert(input); !strings.Contains(got, "path="+enc(path)) {
			t.Errorf("%s: %s", input, got)
		}
	}
	if got := convert("{% response 'raw', 'req_login', '', 'never', 0 %}"); !strings.Contains(got, "response.body.raw(") {
		t.Error(got)
	}
	if got := convert(`{% response 'header', 'req_login', 'x-\'quoted,header', 'never', 0 %}`); !strings.Contains(got, "header="+enc("x-'quoted,header")) {
		t.Error(got)
	}
	input := Object{"authentication": Object{"token": "{{ _['token'] }}"}, "body": Object{"form": []any{Object{"value": "{% uuid 'v4' %}"}}}} // #nosec G101 -- Insomnia template text, no credential.
	got := convertTemplateSyntax(input, nil).(map[string]any)
	if str(obj(got, "authentication"), "token") != "${[ token ]}" || str(objects(array(obj(got, "body"), "form"))[0], "value") != "${[ uuid.v4() ]}" || str(obj(input, "authentication"), "token") != "{{ _['token'] }}" {
		t.Fatal(got)
	}
}

func TestInsomniaBodies(t *testing.T) {
	importBody := func(r Object) Object {
		m := Object{}
		insomniaBodyAndHeaders(m, r)
		return normalizeJSON(t, m)
	}
	graphql := func(text string) Object {
		return importBody(Object{"body": Object{"mimeType": "application/graphql", "text": text}})
	}
	query := "query Get($id: ID!) { node(id: $id) { id } }"
	for _, c := range []struct{ variables, envelope, want string }{
		{"object", `{"query":"` + query + `","variables":{"id":"123"},"operationName":"Get"}`, "{\n  \"id\": \"123\"\n}"},
		{"string", `{"query":"` + query + `","variables":"{\"id\":\"123\"}","operationName":"Get"}`, `{"id":"123"}`},
		{"string with a tag", `{"query":"` + query + `","variables":"{\"id\":{{ _.id }}}","operationName":"Get"}`, `{"id":{{ _.id }}}`},
	} {
		got := graphql(c.envelope)
		want := Object{"bodyType": "graphql", "body": Object{"query": query, "variables": c.want, "operationName": "Get"}, "headers": []any{Object{"name": "Content-Type", "value": "application/json", "enabled": true}}}
		if !reflect.DeepEqual(got, normalizeJSON(t, want)) {
			t.Errorf("%s: %v", c.variables, got)
		}
	}
	for _, text := range []string{"query Get { viewer { id } }", "", "{ malformed", "{ query { me { id } } }"} {
		if got := graphql(text)["body"]; !reflect.DeepEqual(got, map[string]any{"query": text, "variables": ""}) {
			t.Errorf("%q: %v", text, got)
		}
	}
	if got := importBody(Object{"body": Object{"mimeType": "application/graphql; charset=utf-8", "text": `{"query":"{ me { id } }"}`}})["body"]; !reflect.DeepEqual(got, map[string]any{"query": "{ me { id } }", "variables": ""}) {
		t.Errorf("no variables: %v", got)
	}
	if got := graphql(`{"query":"{me{id}}","operationName":"Get","variables":{"id":{{ _.id }}}}`)["body"]; !reflect.DeepEqual(got, map[string]any{"query": "{me{id}}", "variables": `{"id":{{ _.id }}}`, "operationName": "Get"}) {
		t.Errorf("recovered: %v", got)
	}
	if got := graphql(`{"query":"{me{id}}","meta":{{ _.meta }}}`)["body"]; !reflect.DeepEqual(got, map[string]any{"query": "{me{id}}", "variables": ""}) {
		t.Errorf("recovered without variables: %v", got)
	}
	for _, c := range []struct {
		r    Object
		want Object
	}{
		{Object{"body": Object{"mimeType": "application/soap+xml; charset=utf-8", "text": "<soap:Envelope />"}}, Object{"bodyType": "text/xml", "body": Object{"text": "<soap:Envelope />"}, "headers": []any{Object{"enabled": true, "name": "Content-Type", "value": "application/soap+xml; charset=utf-8"}}}},
		{Object{"body": Object{"mimeType": "application/problem+json", "text": `{"message":"Nope"}`}}, Object{"bodyType": "application/json", "body": Object{"text": `{"message":"Nope"}`}, "headers": []any{Object{"enabled": true, "name": "Content-Type", "value": "application/problem+json"}}}},
		{Object{"body": Object{"mimeType": "application/yaml", "text": "message: hello"}}, Object{"bodyType": "other", "body": Object{"text": "message: hello"}, "headers": []any{Object{"enabled": true, "name": "Content-Type", "value": "application/yaml"}}}},
		{Object{"body": Object{"mimeType": "application/yaml", "text": "message: hello"}, "headers": []any{Object{"name": "content-type", "value": "application/x-yaml"}}}, Object{"bodyType": "other", "body": Object{"text": "message: hello"}, "headers": []any{Object{"enabled": true, "name": "content-type", "value": "application/x-yaml"}}}},
		{Object{"body": Object{"text": "hello"}}, Object{"bodyType": "other", "body": Object{"text": "hello"}, "headers": []any{}}},
	} {
		if got := importBody(c.r); !reflect.DeepEqual(got, normalizeJSON(t, c.want)) {
			t.Errorf("%v: %v", c.r, got)
		}
	}
}

func TestInsomniaMigration(t *testing.T) {
	requests := []any{
		Object{"_id": "req_query", "meta": Object{"id": "req_query"}, "_type": "request", "parentId": "wrk_test", "name": "Query", "method": "POST", "url": "{{ _['host'] }}/graphql",
			"authentication": Object{"type": "bearer", "token": "{% response 'body', 'req_login', 'b64::JC50b2tlbg==::abc', 'when-expired', 60 %}"}, // #nosec G101 -- Insomnia template text, no credential.
			"headers":        []any{Object{"name": "X-Request-ID", "value": "{% uuid 'v4' %}"}},
			"parameters":     []any{Object{"name": "id", "value": `{{ _["id"] }}`}},
			"body":           Object{"mimeType": "application/graphql", "text": `{"query":"query Get($id: ID!) { node(id: $id) { id } }","variables":"{\"id\":\"{{ _.id }}\"}","operationName":"Get"}`}},
		Object{"_id": "req_login", "meta": Object{"id": "req_login"}, "_type": "request", "parentId": "wrk_test", "name": "Login", "method": "POST", "url": "{{ _.host }}/login"},
	}
	data := Object{"host": "https://example.com", "id": "123"}
	for version, source := range map[int]Object{
		5: {"type": "collection.insomnia.rest/5.0", "name": "Migration", "meta": Object{"id": "wrk_test"}, "environments": Object{"meta": Object{"id": "env_base"}, "data": data}, "collection": requests},
		4: {"_type": "export", "__export_format": 4, "resources": append([]any{Object{"_type": "workspace", "_id": "wrk_test", "name": "Migration"}, Object{"_type": "environment", "_id": "env_base", "parentId": "wrk_test", "data": data}}, requests...)},
	} {
		text, _ := json.Marshal(source)
		models, err := ParseImport(text)
		if err != nil {
			t.Fatal(version, err)
		}
		var query, login Object
		for _, m := range models {
			switch str(m, "name") {
			case "Query":
				query = m
			case "Login":
				login = m
			}
		}
		if !reflect.DeepEqual(obj(query, "body"), Object{"query": "query Get($id: ID!) { node(id: $id) { id } }", "variables": `{"id":"${[ id ]}"}`, "operationName": "Get"}) {
			t.Errorf("v%d body: %v", version, query["body"])
		}
		if str(query, "url") != "${[ host ]}/graphql" || str(objects(array(query, "urlParameters"))[0], "value") != "${[ id ]}" {
			t.Errorf("v%d url %v %v", version, query["url"], query["urlParameters"])
		}
		headers := normalizeJSON(t, Object{"h": query["headers"]})["h"]
		for _, want := range []any{map[string]any{"name": "X-Request-ID", "value": "${[ uuid.v4() ]}", "enabled": true}, map[string]any{"name": "Content-Type", "value": "application/json", "enabled": true}} {
			if !slices.ContainsFunc(headers.([]any), func(h any) bool { return reflect.DeepEqual(h, want) }) {
				t.Errorf("v%d headers %v", version, headers)
			}
		}
		token := "${[ response.body.path(request=" + insomniaArgument(str(login, "id")) + ", behavior='ttl', ttl='60', path=b64'JC50b2tlbg', result='first') ]}"
		if str(obj(query, "authentication"), "token") != token {
			t.Errorf("v%d token %s", version, str(obj(query, "authentication"), "token"))
		}
		if str(login, "id") != "GENERATE_ID::req_login" || str(query, "id") != "GENERATE_ID::req_query" {
			t.Errorf("v%d ids %s %s", version, str(login, "id"), str(query, "id"))
		}
	}
}

func TestInsomniaKeysStayAcrossRenames(t *testing.T) {
	collection := func(name string) []Object {
		text := "type: collection.insomnia.rest/5.0\nname: Keys\nmeta: {id: wrk_1}\nenvironments: {meta: {id: env_1}, name: Base, data: {}}\ncollection:\n  - meta: {id: fld_1}\n    name: Folder\n    children:\n      - meta: {id: req_1}\n        name: " + name + "\n        method: GET\n        url: https://api.example.com\n"
		models, err := ParseImport([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return models
	}
	for _, name := range []string{"Original", "Renamed"} {
		ids := map[string]string{}
		for _, m := range collection(name) {
			ids[str(m, "model")] = str(m, "id")
		}
		if ids["http_request"] != "GENERATE_ID::req_1" || ids["folder"] != "GENERATE_ID::fld_1" || ids["workspace"] != "GENERATE_ID::wrk_1" {
			t.Errorf("%s: %v", name, ids)
		}
	}
}

func TestJSStringifyMatchesNode(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"{\"a\":{\"b\":[1,2.50,{\"c\":null}],\"d\":[]},\"e\":{},\"f\":\"x\\\"y\\\\z\\n\\u0001\u00e9\u2028</>\",\"g\":1e21,\"h\":-0,\"i\":0.1,\"j\":true,\"k\":[[]]}", "{\n  \"a\": {\n    \"b\": [\n      1,\n      2.5,\n      {\n        \"c\": null\n      }\n    ],\n    \"d\": []\n  },\n  \"e\": {},\n  \"f\": \"x\\\"y\\\\z\\n\\u0001\u00e9\u2028</>\",\n  \"g\": 1e+21,\n  \"h\": 0,\n  \"i\": 0.1,\n  \"j\": true,\n  \"k\": [\n    []\n  ]\n}"},
		{"5", "5"},
		{"\"s\"", "\"s\""},
		{"[ ]", "[]"},
		{"{\"dup\":1,\"dup\":2}", "{\n  \"dup\": 2\n}"},
		{"1.0e-7", "1e-7"},
		{"123456789012345678901234567890", "1.2345678901234568e+29"},
	} {
		if got := jsStringify(jsontext.Value(c.input)); got != c.want {
			t.Errorf("%s:\n%s\nwant\n%s", c.input, got, c.want)
		}
	}
}

func TestDetectImportSource(t *testing.T) {
	e := testEngine(t)
	if format, err := e.DetectImportSource(t.Context(), ImportInput{Kind: "text", Content: "<html>hello</html>"}); err == nil || err.Error() != "Not a supported format" {
		t.Fatalf("%q %v", format, err)
	}
	if _, err := e.DetectImportSource(t.Context(), ImportInput{Origin: filepath.Join(t.TempDir(), "missing.json")}); err == nil || err.Error() == "Not a supported format" {
		t.Fatal(err)
	}
	data, _ := os.ReadFile("testdata/insomnia/version-5.input.yaml")
	if format, err := e.DetectImportSource(t.Context(), ImportInput{Kind: "text", Content: string(data)}); err != nil || format != "Insomnia" {
		t.Fatalf("%q %v", format, err)
	}
}
