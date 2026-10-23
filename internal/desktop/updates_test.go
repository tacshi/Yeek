package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/yeekui"
)

func TestSettingsGeneralUpdates(t *testing.T) {
	a, _ := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	tt := ui.NewTester(a.View, 1360, 860)
	for _, want := range []string{"Updates", "Update Channel", "Update Behavior", "Automatically download updates", "Send anonymous usage statistics", "App Info"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q", want)
		}
	}
	if !b(a.settings, "autoupdate") || !b(a.settings, "autoDownloadUpdates") {
		t.Fatal("updates are automatic by default, as in Yaak", a.settings)
	}
	if err := tt.Click("Enable Automatically download updates"); err != nil {
		t.Fatal(err)
	}
	if b(a.settings, "autoDownloadUpdates") {
		t.Fatal("the checkbox did not turn automatic downloads off")
	}
	a.saveSetting("autoupdate", false)
	tt.Frame()
	_ = tt.Click("Enable Automatically download updates")
	if b(a.settings, "autoDownloadUpdates") {
		t.Fatal("a disabled checkbox changed the setting")
	}
}

func TestToastActions(t *testing.T) {
	a, _ := cookieApp(t)
	ran := ""
	a.showToastActions(updateToastID, "Yeek 9.9.9 is available", "Download and install the update?", "info", 0,
		toastAction{"Download and Install", func() { ran = "install" }}, toastAction{"What's New", func() { ran = "notes" }})
	tt := ui.NewTester(a.View, 1360, 860)
	for _, want := range []string{"Yeek 9.9.9 is available", "Download and install the update?", "Download and Install", "What's New"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q", want)
		}
	}
	a.toasts[0].shown = a.toasts[0].shown.Add(-time.Minute)
	tt.Frame()
	if err := tt.Click("Download and Install"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ran != "install" || len(a.toasts) != 0 {
		t.Fatal(ran, a.toasts)
	}
}

func TestAlertDialog(t *testing.T) {
	a, _ := cookieApp(t)
	a.alertDialog("No Update Available", "You are currently on the latest version "+appVersion())
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("No Update Available") || !tt.HasText("You are currently on the latest version 0.1.0") || tt.HasText("Cancel") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Confirm Okay"); err != nil {
		t.Fatal(err)
	}
	if a.dialogOpen {
		t.Fatal("Okay did not close the alert")
	}
}
