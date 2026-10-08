package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func TestWorkspaceSettingsEditHeadersAuthAndSettings(t *testing.T) {
	a, e := cookieApp(t)
	wid := a.workspace
	a.prompt("workspace_settings", "Workspace Settings", "", wid)
	tt := ui.NewTester(a.View, 1360, 860)
	shot := func(name string) {
		if dir := os.Getenv("YEEK_WS_SETTINGS"); dir != "" {
			tt.Frame()
			f, _ := os.Create(dir + "/" + name + ".png") // #nosec G304 G703 -- the test runner explicitly chooses the snapshot directory.
			_ = png.Encode(f, tt.Image())
			_ = f.Close()
		}
	}
	for _, tab := range []string{"Workspace", "Settings", "Headers", "Auth", "DNS"} {
		if !tt.HasText(tab) {
			t.Fatalf("tab %q missing: %v", tab, tt.Texts())
		}
	}
	shot("0")
	d := a.workspaceDraft

	// Headers: the defaults are inherited, and a new header saves to the workspace.
	if err := tt.Click("Headers"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Defaults") || !tt.HasText("User-Agent") {
		t.Fatal("default headers not shown", tt.Texts())
	}
	if err := tt.Click("Header 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("X-Team")
	if err := tt.Click("Header value 1"); err != nil {
		t.Fatal(err)
	}
	tt.Type("core")
	tt.Frame()
	shot("2")
	saved, _ := e.Store.Get(t.Context(), wid)
	if headers := oslice(saved, "headers"); len(headers) != 1 || s(headers[0], "name") != "X-Team" || s(headers[0], "value") != "core" {
		t.Fatalf("workspace headers = %v", saved["headers"])
	}

	// Auth: a workspace offers No Auth instead of inheriting.
	if err := tt.Click("Auth"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Auth"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); contains(menu, "Inherit from Parent") || !contains(menu, "No Auth") {
		t.Fatal(menu)
	}
	if err := tt.ChooseMenuItem("Bearer Token"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	shot("3")
	saved, _ = e.Store.Get(t.Context(), wid)
	if s(saved, "authenticationType") != "bearer" {
		t.Fatalf("workspace auth = %v", saved["authenticationType"])
	}

	// Settings: workspace values are plain, not overrides. ("Settings" also
	// names the header's gear button, so choose the tab directly.)
	d.Tab = 1
	tt.Frame()
	shot("1")
	for _, title := range []string{"Requests", "Cookies", "Request Timeout", "Follow redirects", "Automatically send cookies", "Local directory sync", "Workspace encryption"} {
		if !tt.HasText(title) {
			t.Fatalf("setting %q missing: %v", title, tt.Texts())
		}
	}
	if err := tt.Click("Enable Follow redirects"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	saved, _ = e.Store.Get(t.Context(), wid)
	if saved["settingFollowRedirects"] != false {
		t.Fatalf("follow redirects = %v", saved["settingFollowRedirects"])
	}
	if err := tt.Click("DNS"); err != nil {
		t.Fatal(err)
	}
	shot("4")
	if d.Tab != 4 {
		t.Fatal(d.Tab)
	}
}

func TestRequestSettingOverrideAndReset(t *testing.T) {
	a, d, tt := composerApp(t, "", engine.Object{})
	d.Tab = 4
	tt.Frame()
	if tt.HasText("Reset override") {
		t.Fatal("reset shown without an override")
	}
	if err := tt.Click("Enable Follow redirects"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if override := o(d.Model, "settingFollowRedirects"); !b(override, "enabled") || override["value"] != false {
		t.Fatalf("override = %v", override)
	}
	if overriddenSettings(d) != 1 || !tt.HasText("Reset override") {
		t.Fatal("override not counted or reset missing")
	}
	if err := tt.Click("Reset override"); err != nil {
		t.Fatal(err)
	}
	if b(o(d.Model, "settingFollowRedirects"), "enabled") {
		t.Fatal("override not reset")
	}
	_ = a
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// A scrolling list's scrollbar must not cover the controls at its right edge.
func TestRowActionsClickableWhenListScrolls(t *testing.T) {
	a, e := cookieApp(t)
	params := []any{}
	for i := range 40 {
		params = append(params, engine.Object{"name": "p" + string(rune('a'+i%26)), "value": "v", "enabled": true})
	}
	m, _ := e.Save(t.Context(), engine.Object{"model": "http_request", "workspaceId": a.workspace, "name": "Many", "urlParameters": params})
	a.applyModel(m)
	a.openRequest(s(m, "id"))
	a.drafts[a.active].Tab = 1
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Actions for Parameter 1"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !contains(menu, "Delete") {
		t.Fatalf("row menu did not open: %v", menu)
	}
}
