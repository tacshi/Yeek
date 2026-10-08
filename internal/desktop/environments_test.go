package desktop

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

func envShot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if dir := os.Getenv("YEEK_ENVS"); dir != "" {
		tt.Frame()
		f, _ := os.Create(dir + "/" + name + ".png") // #nosec G304 G703 -- the test runner explicitly chooses the snapshot directory.
		_ = png.Encode(f, tt.Image())
		_ = f.Close()
	}
}

func TestEnvironmentEditorFeatures(t *testing.T) {
	a, e := cookieApp(t)
	base, _ := a.environmentsBreakdown()
	if base == nil {
		t.Fatal("no base environment")
	}
	staging, _ := e.Save(t.Context(), engine.Object{"model": "environment", "workspaceId": a.workspace, "parentModel": "environment", "name": "Staging", "public": true, "color": "var(--success)", "variables": []any{engine.Object{"name": "token", "value": "plain-secret", "enabled": true}}})
	a.applyModel(staging)
	a.openEnvironmentsAt(s(staging, "id"))
	tt := ui.NewTester(a.View, 1360, 860)
	for _, text := range []string{s(base, "name"), "Staging", "Sharable", "Show Values", "This sharable environment contains plain-text secrets"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %v", text, tt.Texts())
		}
	}
	dots := "••••••••••••" // the twelve characters of "plain-secret", masked
	if !tt.HasText(dots) {
		t.Fatal("value not masked before Show Values")
	}
	envShot(t, tt, "editor")
	if err := tt.Click("Show Values"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText(dots) || !a.showEnvValues {
		t.Fatal("Show Values did not reveal values")
	}

	// Sharable toggles to Private and saves.
	if err := tt.Click("Sharable"); err != nil {
		t.Fatal(err)
	}
	saved, _ := e.Store.Get(t.Context(), s(staging, "id"))
	if b(saved, "public") {
		t.Fatal("environment still sharable")
	}

	// The context menu offers Yaak's actions.
	if err := tt.RightClick("Staging"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	for _, item := range []string{"Rename", "Duplicate", "Change Color", "Make Sharable", "Delete"} {
		if !contains(menu, item) {
			t.Fatalf("menu missing %q: %v", item, menu)
		}
	}
	if err := tt.ChooseMenuItem("Duplicate"); err != nil {
		t.Fatal(err)
	}
	if _, subs := a.environmentsBreakdown(); len(subs) != 2 {
		t.Fatalf("duplicate: %d sub-environments", len(subs))
	}

	// A new sub-environment from the base environment's button.
	a.openEnvironmentsAt(s(base, "id"))
	tt.Frame()
	if err := tt.Click("Add Sub-Environment"); err != nil {
		t.Fatal(err)
	}
	if a.dialog != "new_environment" {
		t.Fatalf("dialog = %s", a.dialog)
	}
	tt.Type("Production")
	if err := tt.Click("danger"); err != nil {
		t.Fatal(err)
	}
	envShot(t, tt, "new")
	if err := tt.Click("Create Environment"); err != nil {
		t.Fatal(err)
	}
	created := a.models[a.dialogID]
	if a.dialog != "environments" || s(created, "name") != "Production" || s(created, "color") != "var(--danger)" || s(created, "parentModel") != "environment" {
		t.Fatalf("created %v in dialog %s", created, a.dialog)
	}

	// The active environment's color tints the header and marks its button.
	a.dialogOpen = false
	a.environment = s(staging, "id")
	tt.Frame()
	envShot(t, tt, "header")
	if !tt.HasText("Staging") {
		t.Fatal("environment button missing")
	}
}

func TestEnvironmentColors(t *testing.T) {
	p := colors{red: ui.Hex("#ff0000"), green: ui.Hex("#00ff00")}
	if c, ok := envColor("var(--danger)", p); !ok || c != p.red {
		t.Fatal("theme color")
	}
	if c, ok := envColor("#123456", p); !ok || hexString(c) != "#123456" {
		t.Fatal("hex color")
	}
	if _, ok := envColor("", p); ok {
		t.Fatal("empty color")
	}
	if _, ok := envColor("var(--bogus)", p); ok {
		t.Fatal("unknown theme color")
	}
}
