package desktop

import (
	"testing"

	"github.com/egoist/mygo/yeekui"
)

func TestAuthEnabledControl(t *testing.T) {
	a, d, tt := composerApp(t, "", nil)
	d.AuthType, d.Auth, d.Tab = "basic", map[string]string{"username": "user", "password": "pass"}, 3
	tt.Frame()
	// typeUsername types into the field under the "Username" label.
	typeUsername := func(text string) {
		t.Helper()
		label, ok := tt.Find("Username")
		if !ok {
			t.Fatal("no Username field", tt.Texts())
		}
		tt.ClickAt(label.X+10, label.Y+label.H+6+16)
		tt.Type(text)
		tt.Frame()
	}
	save := func() map[string]any {
		t.Helper()
		tt.Key(ui.Cmd, ui.KeyS)
		saved, err := a.Engine.Store.Get(t.Context(), d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return o(saved, "authentication")
	}
	if err := tt.Click("Disabled"); err != nil {
		t.Fatal(err)
	}
	if auth := save(); auth["disabled"] != true || auth["username"] != "user" {
		t.Fatalf("disabled not saved: %v", auth)
	}
	if typeUsername("x"); d.Auth["username"] != "user" {
		t.Fatal("disabled auth form was editable", d.Auth["username"])
	}
	if err := tt.Click("Enabled when..."); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("disabled") {
		t.Fatal("empty condition should read as disabled", tt.Texts())
	}
	if err := tt.Click("Dynamic Disabled"); err != nil {
		t.Fatal(err)
	}
	tt.Type("yes")
	tt.Frame()
	if !tt.HasText("enabled") {
		t.Fatal("non-empty condition should read as enabled", tt.Texts())
	}
	if auth := save(); auth["disabled"] != "yes" {
		t.Fatalf("condition not saved: %v", auth)
	}
	// A reopened request keeps the condition out of the auth fields.
	saved, _ := a.Engine.Store.Get(t.Context(), d.ID)
	if fresh := newDraft(saved); fresh.AuthDisabled != "yes" || fresh.Auth["disabled"] != "" || fresh.Auth["username"] != "user" {
		t.Fatalf("reloaded draft: %v %v", fresh.AuthDisabled, fresh.Auth)
	}
	if err := tt.Click("Enabled"); err != nil {
		t.Fatal(err)
	}
	if auth := save(); auth["disabled"] != false {
		t.Fatalf("enabled not saved: %v", auth)
	}
	if typeUsername("x"); d.Auth["username"] == "user" {
		t.Fatal("enabled auth form is not editable")
	}
}
