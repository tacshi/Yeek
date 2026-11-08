package engine

import "testing"

func TestPostmanVariablesBecomeTemplateTags(t *testing.T) {
	source := `{"info":{"name":"Vars","schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
"variable":[{"key":"base_url","value":"https://api.test"}],
"item":[{"name":"Get","event":[{"listen":"prerequest","script":{"exec":["pm.environment.set('x', '{{keep}}')"]}}],"request":{"method":"POST",
"url":{"raw":"{{base_url}}/users?n={{$randomInt}}","query":[{"key":"n","value":"{{$randomInt}}"}]},
"header":[{"key":"X-Id","value":"{{  $guid  }}"},{"key":"X-Faker","value":"{{$randomFirstName}}"},{"key":"X-Secret","value":"{{vault:token}}"},{"key":"X-Env","value":"{{_.env}}"}],
"body":{"mode":"raw","raw":"{\"at\": {{$timestamp}}}"}}}]}`
	models, err := ParseImport([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	request := models[len(models)-1]
	if str(request, "url") != "${[base_url]}/users" {
		t.Fatalf("url = %q", str(request, "url"))
	}
	want := map[string]string{"X-Id": "${[uuid.v4()]}", "X-Faker": "${[$randomFirstName]}", "X-Secret": "${[token]}", "X-Env": "${[env]}"}
	for _, h := range objects(array(request, "headers")) {
		if str(h, "value") != want[str(h, "name")] {
			t.Fatalf("%s = %q", str(h, "name"), str(h, "value"))
		}
	}
	if got := str(objects(array(request, "urlParameters"))[0], "value"); got != "${[random.range(min='0',max='1000',decimals='0')]}" {
		t.Fatalf("query = %q", got)
	}
	if got := str(obj(request, "body"), "text"); got != `{"at": ${[timestamp.unix()]}}` {
		t.Fatalf("body = %q", got)
	}
	script := objects(array(request, "importScripts"))[0]
	if exec := array(obj(script, "script"), "exec"); exec[0] != "pm.environment.set('x', '{{keep}}')" {
		t.Fatalf("script changed: %v", exec)
	}
	environment, err := ParseImport([]byte(`{"name":"Env","_postman_variable_scope":"environment","values":[{"key":"url","value":"{{host}}/v1","enabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := str(objects(array(environment[1], "variables"))[0], "value"); got != "${[host]}/v1" {
		t.Fatalf("environment value = %q", got)
	}
}

func TestBrunoVariablesAndApostrophes(t *testing.T) {
	source := `meta {
  name: Don't panic
  type: http
}

post {
  url: {{host}}/users/{{ id }}
}

headers {
  Authorization: Bearer {{token}}
  X-Env: {{process.env.TOKEN}}
}

body:text {
  it's here } and {{true}}
}

docs {
  Don't forget {{host}}
}
`
	models, err := ParseImport([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	request := models[len(models)-1]
	if str(request, "url") != "${[ host ]}/users/${[ id ]}" {
		t.Fatalf("url = %q", str(request, "url"))
	}
	headers := objects(array(request, "headers"))
	if str(headers[0], "value") != "Bearer ${[ token ]}" || str(headers[1], "value") != "{{process.env.TOKEN}}" {
		t.Fatalf("headers = %v", headers)
	}
	if got := str(obj(request, "body"), "text"); got != "it's here } and {{true}}" {
		t.Fatalf("body = %q", got)
	}
	if str(request, "description") != "Don't forget {{host}}" || str(request, "name") != "Don't panic" {
		t.Fatalf("docs = %q, name = %q", str(request, "description"), str(request, "name"))
	}
}
