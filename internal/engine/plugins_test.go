package engine

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGoPluginTemplateAndRequestHook(t *testing.T) {
	e := testEngine(t)
	path := filepath.Join(t.TempDir(), "plugin.go")
	source := `package extension
import (
 "strings"
 "yeek/plugin"
)
var Plugin = plugin.Definition{
 Name: "Test Extension", Version: "1.0.0",
 Templates: []plugin.Template{{Name:"example.upper", Run:func(ctx plugin.Context,args map[string]string)(string,error){return strings.ToUpper(args["value"]),nil}}},
 BeforeSend:func(ctx plugin.Context,req *plugin.Request)error{req.Headers["X-Plugin"]=[]string{"present"};return nil},
}`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.InstallPlugin(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	w := saveTest(t, e, Object{"model": "workspace"})
	got, err := e.Render(t.Context(), "${[ example.upper(value='native') ]}", str(w, "id"), "", "")
	if err != nil || got != "NATIVE" {
		t.Fatal(got, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plugin") != "present" {
			t.Error("plugin did not set header")
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	request := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": server.URL})
	if _, err = e.SendHTTP(t.Context(), str(request, "id"), SendOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestPluginBodyChangesMatchUploadHistory(t *testing.T) {
	e := testEngine(t)
	dir := t.TempDir()
	source := `package extension
import "yeek/plugin"
var Plugin = plugin.Definition{
 Name:"Upload Mutation", Version:"1",
 BeforeSend:func(ctx plugin.Context, request *plugin.Request)error{request.Body=append(request.Body,[]byte("/before")...);return nil},
 Authentication:[]plugin.Authentication{{Name:"body-auth",Label:"Body Auth",Apply:func(ctx plugin.Context, values map[string]string, request *plugin.Request)error{request.Body=append(request.Body,[]byte("/auth")...);return nil}}},
}`
	pluginPath := filepath.Join(dir, "plugin.go")
	if err := os.WriteFile(pluginPath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.InstallPlugin(t.Context(), pluginPath); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "input.bin")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		received <- string(data)
		w.WriteHeader(204)
	}))
	defer server.Close()
	w := saveTest(t, e, Object{"model": "workspace"})
	r := saveTest(t, e, Object{"model": "http_request", "workspaceId": str(w, "id"), "url": server.URL, "method": "POST", "authenticationType": "body-auth", "bodyType": "binary", "body": Object{"filePath": file}})
	response, err := e.SendHTTP(t.Context(), str(r, "id"), SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wire := <-received
	snapshot, err := e.bodies.ReadFile(str(response, "id") + ".request")
	if err != nil || wire != "original/before/auth" || string(snapshot) != wire || number(response, "requestContentLength") != float64(len(wire)) {
		t.Fatal(wire, string(snapshot), response, err)
	}
}
