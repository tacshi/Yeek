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

const updateStateToastID = "update-state"

const (
	updateIdle        = ""
	updateOffered     = "offered"
	updateDownloading = "downloading"
	updateInstalled   = "installed"
)

var updates struct {
	sync.Mutex
	lastCheck time.Time
	checking  bool
	state     string
	version   string
	offer     *mygo.Update
}

func updateBusy() bool {
	return updates.checking || updates.state == updateDownloading || updates.state == updateInstalled
}

func beginUpdateCheck() (bool, string, string) {
	updates.Lock()
	defer updates.Unlock()
	if updates.checking {
		return false, "checking", ""
	}
	if updates.state == updateDownloading || updates.state == updateInstalled {
		return false, updates.state, updates.version
	}
	updates.checking, updates.lastCheck = true, time.Now()
	return true, "", ""
}

func endUpdateCheck() {
	updates.Lock()
	updates.checking = false
	updates.Unlock()
}

func offerUpdate(up *mygo.Update, download bool) (reshow *mygo.Update, start bool) {
	updates.Lock()
	defer updates.Unlock()
	updates.checking = false
	if updates.state == updateOffered && updates.version == up.Version && updates.offer != nil {
		return updates.offer, false
	}
	if download {
		updates.state, updates.version, updates.offer = updateDownloading, up.Version, up
		return nil, true
	}
	updates.state, updates.version, updates.offer = updateOffered, up.Version, up
	return nil, false
}

func beginUpdateInstall(up *mygo.Update) (bool, string, string) {
	updates.Lock()
	defer updates.Unlock()
	if updates.state == updateDownloading || updates.state == updateInstalled {
		return false, updates.state, updates.version
	}
	updates.state, updates.version, updates.offer = updateDownloading, up.Version, up
	return true, "", ""
}

func endUpdateInstall(up *mygo.Update, err error, fallback string) {
	updates.Lock()
	defer updates.Unlock()
	if err == nil {
		updates.state, updates.offer = updateInstalled, nil
		return
	}
	updates.state = fallback
	if fallback == updateIdle {
		updates.version, updates.offer = "", nil
	} else {
		updates.offer = up
	}
}

func (a *App) showUpdateBusy(state, version string) {
	switch state {
	case "checking":
		a.showToast(updateStateToastID, locales.AlreadyCheckingForUpdates(), "info", 0)
	case updateDownloading:
		a.showToast(updateStateToastID, locales.YeekVersionIsDownloading(version), "info", 0)
	case updateInstalled:
		a.showUpdateInstalledToast(version)
	}
}

func appVersion() string { return cmp.Or(mygo.App.Version(), "0.1.0") }

func AppVersion() string { return appVersion() }

func (a *App) maybeCheckForUpdates() {
	if !b(a.settings, "autoupdate") || !mygo.Updater.Enabled() {
		return
	}
	updates.Lock()
	due := !updateBusy() && time.Since(updates.lastCheck) >= updateCheckInterval
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
	ok, state, version := beginUpdateCheck()
	if !ok {
		if user {
			a.showUpdateBusy(state, version)
		}
		return
	}
	go func() {
		started := time.Now()
		up, err := mygo.Updater.Check(a.ctx)
		if user {
			time.Sleep(time.Until(started.Add(500 * time.Millisecond)))
		}
		if err != nil || up == nil {
			endUpdateCheck()
		}
		var reshow *mygo.Update
		installed := false
		if err == nil && up != nil {
			var download bool
			reshow, download = offerUpdate(up, !user && autoDownload)
			if download {
				log.Printf("Downloading update %s in background", up.Version)
				err = up.Install(a.ctx, nil)
				endUpdateInstall(up, err, updateIdle)
				installed = err == nil
				if err != nil {
					log.Printf("Failed to install: %v", err)
					return
				}
			}
		}
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
			case reshow != nil:
				a.showUpdateAvailableToast(reshow)
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
	ok, state, version := beginUpdateInstall(up)
	if !ok {
		a.showUpdateBusy(state, version)
		return
	}
	a.showToast(updateToastID, locales.DownloadingYeekVersionEllipsis(up.Version), "info", 0)
	go func() {
		err := up.Install(a.ctx, nil)
		endUpdateInstall(up, err, updateOffered)
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
	a.showToastActions(updateToastID, locales.YeekVersionWasInstalled(version), locales.StartUsingTheNewVersionNow(), "primary", 0,
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
