package desktop

import (
	"cmp"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"yeek/locales"
)

const updateCheckInterval = 12 * time.Hour

const updateToastID = "update-info"

func releaseURL(version string) string {
	return "https://github.com/tacshi/Yeek/releases/tag/v" + version
}

var updates struct {
	sync.Mutex
	lastCheck time.Time
	busy      bool
}

func appVersion() string { return cmp.Or(mygo.App.Version(), "0.1.0") }

func (a *App) maybeCheckForUpdates() {
	if !b(a.settings, "autoupdate") || !mygo.Updater.Enabled() {
		return
	}
	updates.Lock()
	due := !updates.busy && time.Since(updates.lastCheck) >= updateCheckInterval
	updates.Unlock()
	if !due {
		return
	}
	time.AfterFunc(3*time.Second, func() {
		if a.ctx.Err() == nil {
			a.Window.Update(func() { a.checkForUpdates(false) })
		}
	})
}

func (a *App) checkForUpdates(user bool) {
	autoDownload := b(a.settings, "autoupdate") && b(a.settings, "autoDownloadUpdates")
	updates.Lock()
	if updates.busy {
		updates.Unlock()
		return
	}
	updates.busy, updates.lastCheck = true, time.Now()
	updates.Unlock()
	go func() {
		started := time.Now()
		up, err := mygo.Updater.Check(a.ctx)
		installed := false
		if err == nil && up != nil && !user && autoDownload {
			log.Printf("Downloading update %s in background", up.Version)
			if err = up.Install(a.ctx, nil); err == nil {
				installed = true
			}
		}
		if user {
			time.Sleep(time.Until(started.Add(500 * time.Millisecond)))
		}
		updates.Lock()
		updates.busy = false
		updates.Unlock()
		if a.ctx.Err() != nil {
			return
		}
		a.Window.Update(func() {
			switch {
			case errors.Is(err, mygo.ErrUpdatesDisabled):
				if user {
					a.alertDialog(locales.UpdatesUnavailable(), locales.ThisBuildOfYeekCantUpdate())
				}
			case err != nil:
				log.Printf("Failed to check for updates: %v", err)
				if user {
					a.showToast(updateToastID, locales.FailedToCheckForUpdatesError(err.Error()), "danger", 0)
				}
			case up == nil:
				if user {
					a.alertDialog(locales.NoUpdateAvailable(), locales.YouAreCurrentlyOnTheLatest(appVersion()))
				}
			case installed:
				a.showUpdateInstalledToast(up.Version)
			default:
				a.showUpdateAvailableToast(up)
			}
		})
	}()
}

func (a *App) showUpdateAvailableToast(up *mygo.Update) {
	whatsNew := toastAction{locales.WhatsNew(), func() { _ = mygo.Shell.OpenExternal(releaseURL(up.Version)) }}
	a.showToastActions(updateToastID, locales.YeekVersionIsAvailable(up.Version), locales.DownloadAndInstallTheUpdate(), "info", 0,
		toastAction{locales.DownloadAndInstall(), func() { a.installUpdate(up) }}, whatsNew)
}

func (a *App) installUpdate(up *mygo.Update) {
	updates.Lock()
	if updates.busy {
		updates.Unlock()
		return
	}
	updates.busy = true
	updates.Unlock()
	a.showToast(updateToastID, locales.DownloadingYeekVersionEllipsis(up.Version), "info", 0)
	go func() {
		err := up.Install(a.ctx, nil)
		updates.Lock()
		updates.busy = false
		updates.Unlock()
		if a.ctx.Err() != nil {
			return
		}
		a.Window.Update(func() {
			if err != nil {
				log.Printf("Failed to install: %v", err)
				a.showToast(updateToastID, locales.FailedToInstallTheUpdateError(err.Error()), "danger", 0)
				return
			}
			log.Printf("Installed %s", up.Version)
			a.showUpdateInstalledToast(up.Version)
		})
	}()
}

func (a *App) showUpdateInstalledToast(version string) {
	a.showToastActions(updateToastID, locales.YeekVersionWasInstalled(version), locales.StartUsingTheNewVersionNow(), "info", 0,
		toastAction{locales.RelaunchYeek(), relaunchApp})
}

var relaunching atomic.Bool

func Relaunching() bool { return relaunching.Load() }

func relaunchApp() {
	relaunching.Store(true)
	for _, w := range mygo.Windows() {
		w.Close()
	}
}
