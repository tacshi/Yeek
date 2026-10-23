package desktop

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/yeekui"
)

func sourceLocale() string { return "" }

func TestLanguageSettingShowsTheChosenLanguage(t *testing.T) {
	var shown []string
	setAppLocale = func(tag string) { shown = append(shown, tag) }
	locale := ""
	i18n.SetLocaleFunc(func() string { return locale })
	t.Cleanup(func() {
		setAppLocale = mygo.App.SetLocale
		i18n.SetLocaleFunc(sourceLocale)
	})
	a, e := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	a.modalTab = settingsInterface
	tt := ui.NewTester(a.View, 1360, 860)
	if !tt.HasText("System") {
		t.Fatal("the language follows the system by default", tt.Texts())
	}
	if err := tt.Click("Language setting"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	settings, err := e.Store.Get(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if s(settings, "language") != "zh-Hans" || len(shown) != 1 || shown[0] != "zh-Hans" {
		t.Fatal(settings["language"], shown)
	}
	locale = "zh-Hans"
	tt.Frame()
	for _, want := range []string{"通用", "界面", "语言", "简体中文", "打开工作区的方式"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Language setting"); err == nil {
		t.Fatal("the select's label is in English still")
	}
}
