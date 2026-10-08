package engine

import (
	"encoding/json/v2"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func openAPIModels(t *testing.T, source string) []Object {
	t.Helper()
	models, err := ParseImport([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	return models
}
func importedNamed(t *testing.T, models []Object, kind, name string) Object {
	t.Helper()
	for _, model := range models {
		if str(model, "model") == kind && (name == "" || str(model, "name") == name) {
			return model
		}
	}
	t.Fatalf("missing %s %q", kind, name)
	return nil
}

func TestOpenAPIParameterStylesAndOverrides(t *testing.T) {
	models := openAPIModels(t, `{"openapi":"3.1.0","info":{"title":"Parameters"},"servers":[{"url":"https://example.test/"}],"paths":{
"/things/{id}/{identity}/file.{format}/{tags}/{filter}":{"parameters":[{"name":"id","in":"path","schema":{"type":"string","default":"old"}}],"get":{"summary":"Read","parameters":[
{"name":"id","in":"path","schema":{"type":"string","default":"a/b"}},
{"name":"identity","in":"path","schema":{"type":"string","default":"full"}},
{"name":"format","in":"path","schema":{"type":"string","default":"json"}},
{"name":"tags","in":"path","style":"label","explode":true,"schema":{"type":"array","default":["red","blue"]}},
{"name":"filter","in":"path","style":"matrix","explode":true,"schema":{"type":"object","default":{"a":1,"b":2}}},
{"name":"q","in":"query","required":true,"schema":{"type":"array","default":["a","b"]}},
{"name":"filter","in":"query","required":true,"style":"deepObject","schema":{"type":"object","default":{"color":"red","limit":2}}},
{"name":"disabled","in":"query","schema":{"type":"string","default":"omit"}},
{"name":"Accept","in":"header","required":true,"example":"ignore"}
],"responses":{"200":{"content":{"application/json":{}}}}}}}}`)
	r := importedNamed(t, models, "http_request", "Read")
	if len(objects(array(r, "urlParameters"))) != 7 {
		t.Fatal(r)
	}
	r["url"] = strings.ReplaceAll(str(r, "url"), "${[ baseUrl ]}", "https://example.test")
	u, err := buildURL(r)
	if err != nil {
		t.Fatal(err)
	}
	if u.EscapedPath() != "/things/a%2Fb/full/file.json/.red.blue/;a=1;b=2" || !slices.Equal(u.Query()["q"], []string{"a", "b"}) || u.Query().Get("filter[color]") != "red" || u.Query().Has("disabled") || u.Query().Has(":id") {
		t.Fatal(u)
	}
	if str(objects(array(r, "headers"))[0], "value") != "application/json" {
		t.Fatal(r["headers"])
	}
}

func TestOpenAPISecurityOverridesReachWire(t *testing.T) {
	var protected, public http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/protected" {
			protected = r.Header.Clone()
		} else {
			public = r.Header.Clone()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	e := testEngine(t)
	source := `{"openapi":"3.0.3","info":{"title":"Auth"},"servers":[{"url":"SERVER"}],"components":{"securitySchemes":{"bearer":{"type":"http","scheme":"bearer"},"key":{"type":"apiKey","in":"header","name":"X-Key"}}},"security":[{"bearer":[],"key":[]}],"paths":{"/protected":{"get":{"summary":"Protected"}},"/public":{"get":{"summary":"Public","security":[],"parameters":[{"in":"cookie","name":"locale","required":true,"example":"en"},{"in":"cookie","name":"mode","required":true,"example":"compact"}]}}}}`
	source = strings.ReplaceAll(source, "SERVER", server.URL)
	models, err := e.Import(t.Context(), []byte(source), "")
	if err != nil {
		t.Fatal(err)
	}
	base := importedNamed(t, models, "environment", "Global Variables")
	for _, row := range objects(array(base, "variables")) {
		if strings.HasPrefix(str(row, "name"), "auth_") {
			row["value"] = "fixture-credential"
		}
	}
	saveTest(t, e, base)
	for _, name := range []string{"Protected", "Public"} {
		request := importedNamed(t, models, "http_request", name)
		if _, err = e.SendHTTP(t.Context(), str(request, "id"), SendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if protected.Get("Authorization") != "Bearer fixture-credential" || protected.Get("X-Key") != "fixture-credential" {
		t.Fatal(protected)
	}
	if public.Get("Authorization") != "" || public.Get("X-Key") != "" || public.Get("Cookie") != "locale=en; mode=compact" || len(public.Values("Cookie")) != 1 {
		t.Fatal("security override or cookie merging failed", public)
	}
}

func TestOpenAPICookieAPIKeyPreservesParameterCookies(t *testing.T) {
	var cookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie = r.Header.Get("Cookie")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	e := testEngine(t)
	source := strings.ReplaceAll(`{"openapi":"3.1.0","info":{"title":"Cookies"},"servers":[{"url":"SERVER"}],"components":{"securitySchemes":{"session":{"type":"apiKey","in":"cookie","name":"session"}}},"security":[{"session":[]}],"paths":{"/cookies":{"get":{"parameters":[{"name":"locale","in":"cookie","required":true,"example":"en"}]}}}}`, "SERVER", server.URL)
	models, err := e.Import(t.Context(), []byte(source), "")
	if err != nil {
		t.Fatal(err)
	}
	base := importedNamed(t, models, "environment", "Global Variables")
	for _, row := range objects(array(base, "variables")) {
		if str(row, "name") == "auth_session_key" {
			row["value"] = "fixture-session"
		}
	}
	saveTest(t, e, base)
	request := importedNamed(t, models, "http_request", "")
	if _, err = e.SendHTTP(t.Context(), str(request, "id"), SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if cookie != "locale=en; session=fixture-session" {
		t.Fatal(cookie)
	}
}

func TestOpenAPIMultipartImportIsSendable(t *testing.T) {
	var gotName, gotFile, gotType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		if err := r.ParseMultipartForm(1 << 20); err != nil { // #nosec G120 -- the loopback fixture caps the entire request body at 2 MiB immediately above.
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		gotName = r.FormValue("name")
		f, _, err := r.FormFile("upload")
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Error(err)
		}
		gotFile = string(data)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	e := testEngine(t)
	source := strings.ReplaceAll(`{"openapi":"3.0.3","info":{"title":"Upload"},"servers":[{"url":"SERVER"}],"paths":{"/upload":{"post":{"requestBody":{"content":{"multipart/form-data":{"schema":{"type":"object","required":["name","upload"],"properties":{"name":{"type":"string","example":"Widget"},"upload":{"type":"string","format":"binary"},"id":{"type":"string","readOnly":true}}}}}}}}}}`, "SERVER", server.URL)
	models, err := e.Import(t.Context(), []byte(source), "")
	if err != nil {
		t.Fatal(err)
	}
	request := importedNamed(t, models, "http_request", "")
	form := objects(array(obj(request, "body"), "form"))
	if len(form) != 2 {
		t.Fatal(form)
	}
	file := filepath.Join(t.TempDir(), "upload.txt")
	if err = os.WriteFile(file, []byte("file contents"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, row := range form {
		if str(row, "name") == "upload" {
			row["file"] = file
		}
	}
	saveTest(t, e, request)
	if _, err = e.SendHTTP(t.Context(), str(request, "id"), SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if gotName != "Widget" || gotFile != "file contents" || !strings.Contains(gotType, "boundary=") {
		t.Fatal(gotName, gotFile, gotType)
	}
}

func TestOpenAPISchemasAndReferenceExamples(t *testing.T) {
	models := openAPIModels(t, `{
  "openapi": "3.1.0",
  "info": {"title": "Schemas"},
  "examples": [{"value": {"from": "array-reference"}}],
  "paths": {
    "/items": {
      "post": {
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {
                "type": "object",
                "allOf": [
                  {"properties": {"name": {"type": "string", "example": 42}, "id": {"type": "integer", "readOnly": true}}},
                  {"properties": {"enabled": {"type": "boolean", "default": false}}}
                ],
                "properties": {"details": {"$ref": "#/components/schemas/Details", "example": {"overridden": true}}}
              }
            }
          }
        }
      }
    },
    "/example": {"get": {"parameters": [{"name": "filter", "in": "query", "required": true, "content": {"application/json": {"examples": {"default": {"$ref": "#/examples/0"}}}}}]}}
  },
  "components": {"schemas": {"Details": {"type": "object", "properties": {"unused": {"type": "string"}}}}}
}`)
	request := importedNamed(t, models, "http_request", "POST /items")
	var body Object
	if err := json.Unmarshal([]byte(str(obj(request, "body"), "text")), &body); err != nil {
		t.Fatal(err)
	}
	if str(body, "name") != "42" || body["id"] != nil || body["enabled"] != false || obj(body, "details")["overridden"] != true {
		t.Fatal(body)
	}
	request = importedNamed(t, models, "http_request", "GET /example")
	if value := str(objects(array(request, "urlParameters"))[0], "value"); value != `{"from":"array-reference"}` {
		t.Fatal(value)
	}
}

func TestOpenAPIOAuthServersAndExtendedMethods(t *testing.T) {
	e := testEngine(t)
	source := `{"openapi":"3.2.0","info":{"title":"OAuth"},"servers":[{"url":"https://{region}.example.test/v1/","description":"Production","variables":{"region":{"default":"us"}}},{"url":"https://staging.example.test/v2","description":"Staging"}],"components":{"securitySchemes":{"OAuth":{"type":"oauth2","flows":{"authorizationCode":{"authorizationUrl":"/authorize","tokenUrl":"tokens"}}}}},"security":[{"OAuth":["read","write"]}],"paths":{"/items":{"query":{"summary":"Search","tags":["Items"]},"additionalOperations":{"PROPFIND":{"summary":"Properties","security":[],"servers":[{"url":"https://dav.example.test/"}]}}}}}`
	models, err := e.Import(t.Context(), []byte(source), "")
	if err != nil {
		t.Fatal(err)
	}
	request := importedNamed(t, models, "http_request", "Search")
	staging := importedNamed(t, models, "environment", "Staging")
	resolved, err := e.resolve(t.Context(), request, str(staging, "id"))
	if err != nil {
		t.Fatal(err)
	}
	auth := obj(resolved.Model, "authentication")
	if str(resolved.Model, "method") != "QUERY" || str(resolved.Model, "url") != "https://staging.example.test/v2/items" || str(auth, "authorizationUrl") != "https://staging.example.test/authorize" || str(auth, "accessTokenUrl") != "https://staging.example.test/v2/tokens" || str(auth, "scope") != "read write" {
		t.Fatal(resolved.Model)
	}
	resolved, err = e.resolve(t.Context(), importedNamed(t, models, "http_request", "Properties"), str(staging, "id"))
	if err != nil || str(resolved.Model, "url") != "https://dav.example.test/items" || str(resolved.Model, "authenticationType") != "none" {
		t.Fatal(resolved.Model, err)
	}
}

func TestOpenAPISwaggerAndXML(t *testing.T) {
	models := openAPIModels(t, `swagger: 2.0
info: {title: Legacy}
host: example.test
basePath: /v1
schemes: [http]
consumes: [application/xml]
produces: [text/plain]
securityDefinitions:
  basic: {type: basic}
security: [{basic: []}]
paths:
  /items:
    post:
      parameters:
        - in: body
          name: item
          schema:
            type: object
            xml: {name: envelope, prefix: e, namespace: 'urn:example'}
            properties:
              id: {type: integer, example: 7, xml: {attribute: true}}
              names:
                type: array
                xml: {name: names, wrapped: true}
                items: {type: string, example: 'A&B', xml: {name: name}}
`)
	request := importedNamed(t, models, "http_request", "POST /items")
	text := str(obj(request, "body"), "text")
	if !strings.Contains(text, `<e:envelope xmlns:e="urn:example" id="7">`) || !strings.Contains(text, "<names>") || !strings.Contains(text, "<name>A&amp;B</name>") {
		t.Fatal(text)
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	for {
		if _, err := decoder.Token(); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
	}
	workspace := importedNamed(t, models, "workspace", "Legacy")
	if str(workspace, "authenticationType") != "basic" {
		t.Fatal(workspace)
	}
}

func TestOpenAPIRelativeServersUseDocumentURL(t *testing.T) {
	var root Object
	if err := json.Unmarshal([]byte(`{"openapi":"3.0.3","info":{"title":"Relative"},"servers":[{"url":"../api/"}],"paths":{"/items":{"get":{}}}}`), &root); err != nil {
		t.Fatal(err)
	}
	models, err := parseOpenAPIAt(root, "https://example.test/docs/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	base := importedNamed(t, models, "environment", "Global Variables")
	for _, v := range objects(array(base, "variables")) {
		if str(v, "name") == "baseUrl" && str(v, "value") != "https://example.test/api" {
			t.Fatal(v)
		}
	}
	delete(root, "servers")
	models, err = parseOpenAPIAt(root, "https://example.test/docs/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	base = importedNamed(t, models, "environment", "Global Variables")
	for _, v := range objects(array(base, "variables")) {
		if str(v, "name") == "baseUrl" && str(v, "value") != "https://example.test" {
			t.Fatal(v)
		}
	}
}
