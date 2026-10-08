package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestCertificateEditorPersistsHostPortAndFormats(t *testing.T) {
	a, e := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	a.modalTab = 4
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Add Certificate"); err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct{ label, value string }{{"Certificate host 1", "api.example.com"}, {"Certificate port 1", "8443"}, {"Certificate file 1", "/tmp/client.pem"}, {"Private key file 1", "/tmp/client.key"}, {"Certificate passphrase 1", "fixture-pass"}} {
		if err := tt.Click(field.label); err != nil {
			t.Fatal(err)
		}
		tt.Type(field.value)
	}
	settings, err := e.Store.Get(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	certs := oslice(settings, "clientCertificates")
	if len(certs) != 1 || s(certs[0], "host") != "api.example.com" || n(certs[0], "port") != 8443 || s(certs[0], "keyFile") != "/tmp/client.key" {
		t.Fatal(certs)
	}
	if err = tt.Click("Certificate format 1"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("PFX / PKCS#12"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("PFX file 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("/tmp/client.p12")
	settings, err = e.Store.Get(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	certs = oslice(settings, "clientCertificates")
	if s(certs[0], "pfxFile") != "/tmp/client.p12" || s(certs[0], "keyFile") != "" || s(certs[0], "crtFile") != "" {
		t.Fatal(certs)
	}
	if err = tt.Click("Enable certificate 1"); err != nil {
		t.Fatal(err)
	}
	settings, err = e.Store.Get(t.Context(), "default")
	if err != nil || b(oslice(settings, "clientCertificates")[0], "enabled") {
		t.Fatal(settings, err)
	}
}

func TestProxyEditorAuthenticationAndTabSwitch(t *testing.T) {
	a, e := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	a.modalTab = 3
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Proxy mode"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Custom"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("HTTP proxy value"); err != nil {
		t.Fatal(err)
	}
	tt.Batch(func() {
		tt.Type("localhost:9090")
		if err := tt.Click("Certificates"); err != nil {
			t.Fatal(err)
		}
	})
	settings, err := e.Store.Get(t.Context(), "default")
	if err != nil || s(o(settings, "proxy"), "http") != "localhost:9090" {
		t.Fatal(settings, err)
	}
	if err = tt.Click("Proxy"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("Proxy authentication"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("Proxy username"); err != nil {
		t.Fatal(err)
	}
	tt.Type("fixture-user")
	if err = tt.Click("Proxy password"); err != nil {
		t.Fatal(err)
	}
	tt.Type("fixture-pass")
	if err = tt.Click("Enable proxy"); err != nil {
		t.Fatal(err)
	}
	settings, err = e.Store.Get(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	proxy := o(settings, "proxy")
	if !b(proxy, "disabled") || s(o(proxy, "auth"), "user") != "fixture-user" || s(o(proxy, "auth"), "password") != "fixture-pass" {
		t.Fatal(proxy)
	}
	a.saveSetting("editorFontSize", 14)
	settings, err = e.Store.Get(t.Context(), "default")
	if err != nil || s(o(settings, "proxy"), "http") != "localhost:9090" {
		t.Fatal("unrelated preference changed proxy", settings, err)
	}
}

func TestWorkspaceDNSAndCAEditors(t *testing.T) {
	a, e := cookieApp(t)
	a.prompt("workspace_settings", "Workspace Settings", "", a.workspace)
	a.modalTab = 1
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Add DNS Override"); err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct{ label, value string }{{"DNS hostname 1", "api.test"}, {"IPv4 addresses 1", "127.0.0.1, 127.0.0.2"}, {"IPv6 addresses 1", "::1"}} {
		if err := tt.Click(field.label); err != nil {
			t.Fatal(err)
		}
		tt.Type(field.value)
	}
	workspace, err := e.Store.Get(t.Context(), a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	rows := oslice(workspace, "settingDnsOverrides")
	if len(rows) != 1 || s(rows[0], "hostname") != "api.test" || len(networkArray(rows[0], "ipv4")) != 2 || len(networkArray(rows[0], "ipv6")) != 1 {
		t.Fatal(rows)
	}
	if err = tt.Click("CA Certificates"); err != nil {
		t.Fatal(err)
	}
	if err = tt.Click("CA file path"); err != nil {
		t.Fatal(err)
	}
	tt.Type("/tmp/private-ca.pem")
	workspace, err = e.Store.Get(t.Context(), a.workspace)
	if err != nil || s(workspace, "caFile") != "/tmp/private-ca.pem" {
		t.Fatal(workspace, err)
	}
	if err = tt.Click("Clear CA file path"); err != nil {
		t.Fatal(err)
	}
	workspace, err = e.Store.Get(t.Context(), a.workspace)
	if err != nil || s(workspace, "caFile") != "" {
		t.Fatal(workspace, err)
	}
}

func TestRequestNetworkSettingsInheritAndOverride(t *testing.T) {
	a, p := templateTestApp()
	a.models["workspace"] = engine.Object{"settingRequestTimeout": 1200, "settingHttpVersion": "http1"}
	d := newDraft(engine.Object{"model": "http_request", "id": "request", "workspaceId": "workspace"})
	tt := ui.NewTester(func(c *ui.Context) { a.requestSettings(c, p, d) }, 700, 650)
	if err := tt.Click("Override Timeout (milliseconds)"); err != nil {
		t.Fatal(err)
	}
	if n(o(d.Model, "settingRequestTimeout"), "value") != 1200 || !b(o(d.Model, "settingRequestTimeout"), "enabled") {
		t.Fatal(d.Model)
	}
	if err := tt.Click("Request HTTP version"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("HTTP/2"); err != nil {
		t.Fatal(err)
	}
	if s(o(d.Model, "settingHttpVersion"), "value") != "http2" {
		t.Fatal(d.Model)
	}
	for _, text := range tt.Texts() {
		if strings.Contains(strings.ToLower(text), "subscription") {
			t.Fatal("subscription information in request settings")
		}
	}
}
