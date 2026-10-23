package desktop

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	ui "github.com/egoist/mygo/yeekui"

	"yeek/locales"
)

func formatDate(t time.Time, layout, skeleton string) string {
	locale := i18n.Locale()
	if locale == "" || strings.HasPrefix(locale, "en") {
		return t.Format(layout)
	}
	return i18n.FormatDate(locale, t, i18n.DateOptions{Skeleton: skeleton})
}

func localeName(tag string) string {
	if tag == "" {
		return locales.FollowSystem()
	}
	return i18n.Autonym(tag)
}

func (a *App) languageSetting(c *ui.Context, p colors) {
	tags := append([]string{""}, mygo.App.Languages()...)
	names := make([]string, len(tags))
	for i, tag := range tags {
		names[i] = localeName(tag)
	}
	choice := names[max(0, slices.Index(tags, s(a.settings, "language")))]
	settingRow(c, p, locales.Language(), locales.TheLanguageYeekShowsItsMenus(), nil, func() {
		if ui.Select(c, &choice, names).Label(locales.SettingSetting(locales.Language())).Width(220).Changed() {
			if i := slices.Index(names, choice); i >= 0 {
				a.setLanguage(tags[i])
			}
		}
	})
}

func (a *App) setLanguage(tag string) {
	a.saveSetting("language", nilIfEmpty(tag))
	setAppLocale(tag)
}

var setAppLocale = mygo.App.SetLocale

var applyLanguage sync.Once

func (a *App) applySavedLanguage() {
	applyLanguage.Do(func() {
		if tag := s(a.settings, "language"); tag != "" {
			setAppLocale(tag)
		}
	})
}
