package engine

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func shortPluginLimits(t *testing.T, limit time.Duration) {
	t.Helper()
	load, call := pluginLoadTimeout, pluginCallTimeout
	pluginLoadTimeout, pluginCallTimeout = limit, limit
	t.Cleanup(func() { pluginLoadTimeout, pluginCallTimeout = load, call })
}

func within(t *testing.T, limit time.Duration, work func()) {
	t.Helper()
	start := time.Now()
	work()
	if took := time.Since(start); took > limit {
		t.Fatalf("took %v, want under %v", took, limit)
	}
}

func TestPluginLoadTimeLimit(t *testing.T) {
	e := testEngine(t)
	shortPluginLimits(t, 200*time.Millisecond)
	for name, source := range map[string]string{
		"spin.js":    "for (;;) {}\nexport default { name: 'Spin' }",
		"timer.js":   "await new Promise((done) => setTimeout(done, 60000))\nexport default { name: 'Timer' }",
		"pending.js": "await new Promise(() => {})\nexport default { name: 'Pending' }",
	} {
		within(t, 5*time.Second, func() {
			_, err := e.InstallPlugin(t.Context(), writePlugin(t, name, source))
			if err == nil {
				t.Fatal(name, "should not load")
			}
			if name != "pending.js" && (err.Error() != "plugin "+name+" was stopped after 0.2 seconds" || !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatal(name, err)
			}
		})
	}
	if plugins := e.Plugins(); len(plugins) != 0 {
		t.Fatal(plugins)
	}
}

func TestPluginCallTimeLimit(t *testing.T) {
	e := testEngine(t)
	shortPluginLimits(t, 200*time.Millisecond)
	installPlugin(t, e, "plugin.js", `let calls = 0
export default { name: "Slow", templates: [
  { name: "slow.spin", run() { for (;;) {} } },
  { name: "slow.timer", async run() { await new Promise((done) => setTimeout(done, 60000)); return "late" } },
  { name: "slow.ok", run() { calls++; return String(calls) } },
] }`)
	for _, name := range []string{"slow.spin", "slow.timer"} {
		within(t, 5*time.Second, func() {
			if _, err := e.pluginTemplate(t.Context(), name, nil, ""); err == nil || err.Error() != "plugin Slow was stopped after 0.2 seconds" {
				t.Fatal(name, err)
			}
		})
		if got, err := e.pluginTemplate(t.Context(), "slow.ok", nil, ""); err != nil || got != "1" {
			t.Fatal(name, got, err)
		}
	}
}

func TestPluginCallsFromManyGoroutines(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `let calls = 0
const wait = () => new Promise((done) => setTimeout(done, 1))
export default { name: "Counter", templates: [{ name: "counter.next", async run() {
  const mine = ++calls
  await wait()
  return String(mine)
} }] }`)
	const workers, each = 16, 20
	var mu sync.Mutex
	var seen []int
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range each {
				got, err := e.pluginTemplate(t.Context(), "counter.next", nil, "")
				if err != nil {
					t.Error(err)
					return
				}
				n, err := strconv.Atoi(got)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				seen = append(seen, n)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	slices.Sort(seen)
	if len(seen) != workers*each || seen[0] != 1 || seen[len(seen)-1] != workers*each {
		t.Fatal(len(seen), seen[:min(len(seen), 5)])
	}
	if compact := slices.Compact(slices.Clone(seen)); len(compact) != len(seen) {
		t.Fatal("two calls saw the same count")
	}
}

func TestPluginCallsDuringReload(t *testing.T) {
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `export default { name: "Steady", templates: [{ name: "steady.ok", async run() { await null; return "ok" } }] }`)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for !stop.Load() {
				got, err := e.pluginTemplate(t.Context(), "steady.ok", nil, "")
				if err == nil && got != "ok" {
					t.Error(got)
				}
				if err != nil && !strings.Contains(err.Error(), "was reloaded") {
					t.Error(err)
				}
			}
		})
	}
	for range 10 {
		if err := e.LoadPlugins(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if got, err := e.pluginTemplate(t.Context(), "steady.ok", nil, ""); err != nil || got != "ok" {
		t.Fatal(got, err)
	}
}

const pluginFetchSource = `export default { name: "Fetch", templates: [{ name: "fetch.get", async run(ctx, args) {
  const response = await fetch(args.url)
  return await response.text()
} }] }`

func TestPluginFetchUsesTheProxySetting(t *testing.T) {
	var proxyCalls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		_, _ = io.WriteString(w, "proxy")
	}))
	defer proxy.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "origin") }))
	defer origin.Close()
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", pluginFetchSource)
	config := Object{"type": "enabled", "http": strings.TrimPrefix(proxy.URL, "http://"), "https": "", "bypass": ""}
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": config})
	if got, err := e.pluginTemplate(t.Context(), "fetch.get", map[string]string{"url": origin.URL}, ""); err != nil || got != "proxy" {
		t.Fatal(got, err)
	}
	config["bypass"] = "127.0.0.1"
	saveTest(t, e, Object{"model": "settings", "id": "default", "proxy": config})
	if got, err := e.pluginTemplate(t.Context(), "fetch.get", map[string]string{"url": origin.URL}, ""); err != nil || got != "origin" || proxyCalls.Load() != 1 {
		t.Fatal(got, err, proxyCalls.Load())
	}
}

func TestPluginFetchFollowsWorkspaceCertificates(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "secure") }))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", pluginFetchSource)
	args := map[string]string{"url": server.URL}
	strict := saveTest(t, e, Object{"model": "workspace", "settingValidateCertificates": true})
	for _, workspace := range []string{"", str(strict, "id")} {
		if got, err := e.pluginTemplate(t.Context(), "fetch.get", args, workspace); err == nil {
			t.Fatal(workspace, "an untrusted certificate was accepted", got)
		}
	}
	trusting := saveTest(t, e, Object{"model": "workspace", "settingValidateCertificates": true, "caFile": ca})
	relaxed := saveTest(t, e, Object{"model": "workspace", "settingValidateCertificates": false})
	for _, workspace := range []string{str(trusting, "id"), str(relaxed, "id")} {
		if got, err := e.pluginTemplate(t.Context(), "fetch.get", args, workspace); err != nil || got != "secure" {
			t.Fatal(workspace, got, err)
		}
	}
}

func TestPluginFetchStopsWhenTheCallIsCancelled(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	e := testEngine(t)
	installPlugin(t, e, "plugin.js", `export default { name: "Fetch", templates: [
  { name: "fetch.get", async run(ctx, args) { return await (await fetch(args.url)).text() } },
  { name: "fetch.ok", run() { return "ok" } },
] }`)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	within(t, 5*time.Second, func() {
		if _, err := e.pluginTemplate(ctx, "fetch.get", map[string]string{"url": server.URL}, ""); err == nil {
			t.Fatal("a cancelled fetch should fail")
		}
	})
	if got, err := e.pluginTemplate(t.Context(), "fetch.ok", nil, ""); err != nil || got != "ok" {
		t.Fatal(got, err)
	}
}

func TestPluginValuesConsoleListsAndRemoval(t *testing.T) {
	e := testEngine(t)
	info := installPlugin(t, e, "plugin.js", `export default {
  name: "Listed",
  templates: [
    { name: "listed.remember", run(ctx, args) { ctx.setValue("kept", args.value); return "" } },
    { name: "listed.recall", run(ctx) { console.log("recall", { kept: ctx.getValue("kept") }, 1); return ctx.getValue("kept") + "|" + ctx.getValue("missing") } },
  ],
  authentication: [{ name: "listed.auth", label: "Listed", fields: [{ name: "token" }], apply(ctx, values, request) { request.headers.Authorization = "Bearer " + values.token } }],
  actions: [{ name: "listed.action", label: "Act", run() {} }],
  filter(ctx, content) { return content },
}`)
	var logged bytes.Buffer
	output, flags := log.Writer(), log.Flags()
	log.SetOutput(&logged)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(output); log.SetFlags(flags) })
	if _, err := e.pluginTemplate(t.Context(), "listed.remember", map[string]string{"value": "v1"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.LoadPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := e.pluginTemplate(t.Context(), "listed.recall", nil, ""); err != nil || got != "v1|" {
		t.Fatal(got, err)
	}
	if got := logged.String(); got != "plugin Listed: recall {\"kept\":\"v1\"} 1\n" {
		t.Fatalf("%q", got)
	}
	if auth := e.PluginAuthentication(); len(auth) != 1 || auth[0].Name != "listed.auth" || auth[0].Label != "Listed" || len(auth[0].Fields) != 1 {
		t.Fatal(auth)
	}
	request, err := http.NewRequest("GET", "https://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if handled, err := e.pluginAuth(t.Context(), "listed.auth", Object{"token": "t"}, request, ""); !handled || err != nil || request.Header.Get("Authorization") != "Bearer t" {
		t.Fatal(handled, err, request.Header)
	}
	if actions := e.PluginActions(); len(actions) != 1 || actions[0].Label != "Act" {
		t.Fatal(actions)
	}
	if filters := e.PluginFilterNames(); !slices.Equal(filters, []string{"Listed"}) {
		t.Fatal(filters)
	}
	if err := e.RemovePlugin(t.Context(), info.ID); err != nil {
		t.Fatal(err)
	}
	if plugins := e.Plugins(); len(plugins) != 0 {
		t.Fatal(plugins)
	}
	if len(e.PluginAuthentication()) != 0 || len(e.PluginActions()) != 0 || len(e.PluginFilterNames()) != 0 {
		t.Fatal("a removed plugin still contributes")
	}
	if _, err := e.pluginTemplate(t.Context(), "listed.recall", nil, ""); err == nil {
		t.Fatal("a removed plugin should not run")
	}
}

func BenchmarkPluginTemplate(b *testing.B) {
	e, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = e.Close() })
	path := filepath.Join(b.TempDir(), "plugin.js")
	if err = os.WriteFile(path, []byte(`export default { name: "Bench", templates: [
  { name: "bench.sync", run(ctx, args) { return args.value.toUpperCase() } },
  { name: "bench.async", async run(ctx, args) { await null; return args.value } },
] }`), 0600); err != nil {
		b.Fatal(err)
	}
	if _, err = e.InstallPlugin(b.Context(), path); err != nil {
		b.Fatal(err)
	}
	args := map[string]string{"value": "abc"}
	for _, name := range []string{"bench.sync", "bench.async"} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := e.pluginTemplate(b.Context(), name, args, ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("render", func(b *testing.B) {
		for b.Loop() {
			if _, err := e.Render(b.Context(), "${[ bench.sync(value='abc') ]}", "", "", ""); err != nil {
				b.Fatal(err)
			}
		}
	})
}
