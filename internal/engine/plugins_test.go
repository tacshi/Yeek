package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MoeForge/moejs"
	api "yeek/plugin"
)

func writePlugin(t *testing.T, name, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func installPlugin(t *testing.T, e *Engine, name, source string) PluginInfo {
	t.Helper()
	info, err := e.InstallPlugin(t.Context(), writePlugin(t, name, source))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestPluginTemplateAndRequestHook(t *testing.T) {
	e := testEngine(t)
	info := installPlugin(t, e, "plugin.js", `export default {
  name: "Test Extension", version: "1.0.0", description: "Tests",
  templates: [{ name: "example.upper", description: "Upper", fields: [{ name: "value", label: "Value", default: "x", secret: true, options: ["a"] }], run(ctx, args) { return args.value.toUpperCase() } }],
  beforeSend(ctx, request) { request.headers["X-Plugin"] = ["present"] },
}`)
	if info.Name != "Test Extension" || info.Version != "1.0.0" || info.Description != "Tests" {
		t.Fatal(info)
	}
	field := e.pluginList()[0].definition.Templates[0].Fields[0]
	if field.Name != "value" || field.Label != "Value" || field.Default != "x" || !field.Secret || len(field.Options) != 1 {
		t.Fatal(field)
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
	installPlugin(t, e, "plugin.js", `const append = (body, text) => new TextDecoder().decode(body) + text
export default {
  name: "Upload Mutation", version: "1",
  beforeSend(ctx, request) { request.body = append(request.body, "/before") },
  authentication: [{ name: "body-auth", label: "Body Auth", apply(ctx, values, request) { request.body = new TextEncoder().encode(append(request.body, "/auth")) } }],
}`)
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

func TestTypeScriptPluginWithAsyncHooks(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.ts", `type Args = { value: string }
const wait = (ms: number) => new Promise<void>((done) => setTimeout(done, ms))
export default {
  name: "Typed",
  templates: [{ name: "typed.later", async run(ctx: unknown, args: Args): Promise<string> { await wait(5); return args.value + "!" } }],
}`)
	got, err := e.pluginTemplate(t.Context(), "typed.later", map[string]string{"value": "hi"}, "")
	if err != nil || got != "hi!" {
		t.Fatal(got, err)
	}
}

func TestPluginEveryKindOfContribution(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `let seen = null
export default {
  name: "Everything",
  prefix: "<",
  actions: [{ name: "act", label: "Act", run(ctx) { ctx.setValue("ran", ctx.workspaceId + "/" + ctx.requestId) } }],
  themes: [{ name: "Dusk", dark: true, colors: { background: "#000000" } }],
  import(ctx, content) { return content === "mine" ? [{ model: "workspace", name: "Imported", count: 2 }] : [] },
  filter(ctx, content, expression) { return this.prefix + content + expression },
  afterReceive(ctx, response) { ctx.setValue("status", String(response.status)); ctx.setValue("body", new TextDecoder().decode(response.body)) },
}`)
	if err := e.RunPluginAction(t.Context(), "act", "w1", "r1"); err != nil {
		t.Fatal(err)
	}
	p := e.pluginList()[0]
	values := e.pluginContext(t.Context(), p.info.Name, "", "")
	if got, err := values.GetValue("ran"); err != nil || got != "w1/r1" {
		t.Fatal(got, err)
	}
	if themes := e.PluginThemes(); len(themes) != 1 || themes[0].Name != "Dusk" || !themes[0].Dark || themes[0].Colors["background"] != "#000000" {
		t.Fatal(themes)
	}
	if got, err := e.Filter(t.Context(), "a", "b", "", "Everything", ""); err != nil || got != "<ab" {
		t.Fatal(got, err)
	}
	importer, models, err := e.pluginImport(t.Context(), "mine")
	if err != nil || importer != "Everything" || len(models) != 1 || str(models[0], "name") != "Imported" || number(models[0], "count") != 2 {
		t.Fatal(models, err)
	}
	if _, _, err = e.pluginImport(t.Context(), "other"); err == nil {
		t.Fatal("an importer that recognizes nothing should fail")
	}
	response := &http.Response{StatusCode: 201, Header: http.Header{"X-A": {"1"}}}
	if err = e.afterReceive(t.Context(), Object{}, response, []byte("done")); err != nil {
		t.Fatal(err)
	}
	if got, _ := values.GetValue("status"); got != "201" {
		t.Fatal(got)
	}
	if got, _ := values.GetValue("body"); got != "done" {
		t.Fatal(got)
	}
}

func TestPluginBinaryBodySurvives(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `export default { name: "Binary", beforeSend(ctx, request) { request.body = new Uint8Array([...request.body, 0xff]) } }`)
	p := e.pluginList()[0]
	request := api.Request{Method: "POST", URL: "https://example.com", Headers: map[string][]string{"A": {"1", "2"}}, Body: []byte{0, 0xfe}}
	if err := p.definition.BeforeSend(e.pluginContext(t.Context(), "Binary", "", ""), &request); err != nil {
		t.Fatal(err)
	}
	if string(request.Body) != "\x00\xfe\xff" || len(request.Headers["A"]) != 2 {
		t.Fatal(request)
	}
}

func TestPluginsSeeNoFilesOrHostAccess(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `export default { name: "Sandbox", templates: [{ name: "sandbox.globals", run() {
  return [typeof require, typeof process, typeof Deno, typeof fetch].join(",")
} }] }`)
	got, err := e.pluginTemplate(t.Context(), "sandbox.globals", nil, "")
	if err != nil || got != "undefined,undefined,undefined,function" {
		t.Fatal(got, err)
	}
	if _, err = e.InstallPlugin(t.Context(), writePlugin(t, "imports.js", `import fs from "node:fs"
export default { name: "Imports", run: fs }`)); err == nil {
		t.Fatal("a plugin should not import modules")
	}
}

func TestPluginFetchUsesTheNetwork(t *testing.T) {
	e := testEngine(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "token") }))
	defer server.Close()
	installPlugin(t, e, "plugin.js", `export default { name: "Fetch", templates: [{ name: "fetch.get", async run(ctx, args) {
  const response = await fetch(args.url)
  return await response.text()
} }] }`)
	got, err := e.pluginTemplate(t.Context(), "fetch.get", map[string]string{"url": server.URL}, "")
	if err != nil || got != "token" {
		t.Fatal(got, err)
	}
}

func TestPluginFailuresReachTheCallerAndLeaveItUsable(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `let calls = 0
export default { name: "Failing", templates: [
  { name: "fail.throw", run() { throw new Error("thrown") } },
  { name: "fail.reject", async run() { throw new Error("rejected") } },
  { name: "fail.never", run() { return new Promise(() => {}) } },
  { name: "fail.spin", run() { for (;;) {} } },
  { name: "fail.ok", run() { calls++; return String(calls) } },
] }`)
	for name, want := range map[string]string{"fail.throw": "thrown", "fail.reject": "rejected", "fail.never": "never finished"} {
		if _, err := e.pluginTemplate(t.Context(), name, nil, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatal(name, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := e.pluginTemplate(ctx, "fail.spin", nil, ""); err == nil || strings.Contains(err.Error(), "was stopped after") {
		t.Fatal("a loop the caller cancels should stop with the caller's error", err)
	}
	if got, err := e.pluginTemplate(t.Context(), "fail.ok", nil, ""); err != nil || got != "1" {
		t.Fatal(got, err)
	}
}

func TestPluginMemoryIsLimited(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `export default { name: "Greedy", templates: [
  { name: "greedy.grow", run() { const kept = []; for (;;) kept.push(new Array(4096).fill(1)) } },
  { name: "greedy.ok", run() { return "ok" } },
] }`)
	if _, err := e.pluginTemplate(t.Context(), "greedy.grow", nil, ""); !errors.Is(err, moejs.ErrMemoryLimit) || err.Error() != "plugin Greedy was stopped for using more than 256 MB of memory" {
		t.Fatal(err)
	}
	if got, err := e.pluginTemplate(t.Context(), "greedy.ok", nil, ""); err != nil || got != "ok" {
		t.Fatal(got, err)
	}
}

func TestPluginLoadErrors(t *testing.T) {
	e := testEngine(t)
	for name, source := range map[string]string{
		"syntax.js":  "export default {",
		"nothing.js": "export const a = 1",
		"unnamed.js": "export default {}",
		"old.go":     "package extension",
		"data.txt":   "export default { name: 'x' }",
	} {
		if _, err := e.InstallPlugin(t.Context(), writePlugin(t, name, source)); err == nil {
			t.Fatal(name)
		}
	}
}

func TestLegacyGoPluginShowsWhyItStopped(t *testing.T) {
	e := testEngine(t)
	path := writePlugin(t, "old.go", "package extension")
	saveTest(t, e, Object{"model": "plugin", "directory": path, "enabled": true, "source": "filesystem"})
	if err := e.LoadPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if plugins := e.Plugins(); len(plugins) != 1 || !strings.Contains(plugins[0].Error, "no longer supported") {
		t.Fatal(plugins)
	}
}

func TestPluginReloadKeepsWorking(t *testing.T) {
	e := testEngine(t)
	info := installPlugin(t, e, "plugin.js", `export default { name: "Reload", templates: [{ name: "reload.ok", run() { return "ok" } }] }`)
	if err := e.LoadPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := e.pluginTemplate(t.Context(), "reload.ok", nil, ""); err != nil || got != "ok" {
		t.Fatal(got, err)
	}
	if err := e.PluginEnabled(t.Context(), info.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pluginTemplate(t.Context(), "reload.ok", nil, ""); err == nil {
		t.Fatal("a disabled plugin should not run")
	}
}
