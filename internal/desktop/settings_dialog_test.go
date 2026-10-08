package desktop

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/yeekui"
)

func TestSettingsDialogTabsInYaakOrder(t *testing.T) {
	a, _ := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	tt := ui.NewTester(a.View, 1360, 860)
	for _, want := range []string{"General", "Theme", "Interface", "Shortcuts", "Plugins", "Certificates", "Proxy", "App Info", "Data Directory"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q", want)
		}
	}
	if err := tt.Click("Theme"); err != nil {
		t.Fatal(err)
	}
	if a.modalTab != settingsTheme || !tt.HasText("Preview") || !tt.HasText("Appearance") {
		t.Fatal(a.modalTab, tt.Texts())
	}
	if err := tt.Click("Interface"); err != nil {
		t.Fatal(err)
	}
	wrap := b(a.settings, "editorSoftWrap")
	if err := tt.Click("Enable Wrap editor lines"); err != nil {
		t.Fatal(err)
	}
	if b(a.settings, "editorSoftWrap") == wrap {
		t.Fatal(a.settings)
	}
}

func TestSettingsShortcutRecordAndReset(t *testing.T) {
	a, e := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	a.modalTab = settingsShortcuts
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Shortcut for New Request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyJ)
	settings, err := e.Store.Get(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := o(settings, "hotkeys")["model.create"].([]any)
	if len(keys) != 1 || a.recordingHotkey != "" {
		t.Fatal(o(settings, "hotkeys"), a.recordingHotkey)
	}
	combo, ok := parseHotkey(keys[0].(string))
	if !ok || combo.mods != ui.Cmd|ui.Shift || combo.key != ui.KeyJ {
		t.Fatal(keys)
	}
	if err = tt.Click("Reset New Request"); err != nil {
		t.Fatal(err)
	}
	if _, custom := o(a.settings, "hotkeys")["model.create"]; custom {
		t.Fatal(a.settings)
	}
}

func TestInterfaceZoomHotkeys(t *testing.T) {
	a, _ := cookieApp(t)
	tt := ui.NewTester(a.View, 1200, 800)
	before, _ := tt.Find("Settings")
	tt.Key(ui.Cmd, ui.KeyEqual)
	tt.Frame()
	if got := n(a.settings, "interfaceScale"); got < 1.09 || got > 1.11 {
		t.Fatal(got)
	}
	after, _ := tt.Find("Settings")
	// The gear sits at the right edge, which a zoom brings nearer in DIPs.
	if after.X >= before.X {
		t.Fatal(before, after)
	}
	for range 20 {
		tt.Key(ui.Cmd, ui.KeyMinus)
	}
	if got := n(a.settings, "interfaceScale"); got < 0.399 || got > 0.401 {
		t.Fatal(got)
	}
	tt.Key(ui.Cmd, ui.Key0)
	if n(a.settings, "interfaceScale") != 1 {
		t.Fatal(a.settings["interfaceScale"])
	}
}

func TestClicksLandUnderZoom(t *testing.T) {
	a, _ := cookieApp(t)
	a.settings["interfaceScale"] = 1.5
	tt := ui.NewTester(a.View, 1200, 800)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if menu := tt.Menu(); !slices.Contains(menu, "Keyboard shortcuts") {
		t.Fatal(menu)
	}
}
