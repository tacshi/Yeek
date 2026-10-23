package desktop

import (
	"cmp"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
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
	a.checkingUpdates = user
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
			a.checkingUpdates = false
			switch {
			case errors.Is(err, mygo.ErrUpdatesDisabled):
				if user {
					a.alertDialog("Updates Unavailable", "This build of Yeek can't update itself. Development builds, and copies installed by a package manager, are updated outside the app.")
				}
			case err != nil:
				log.Printf("Failed to check for updates: %v", err)
				if user {
					a.showToast(updateToastID, "Failed to check for updates: "+err.Error(), "danger", 0)
				}
			case up == nil:
				if user {
					a.alertDialog("No Update Available", "You are currently on the latest version "+appVersion())
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
	whatsNew := toastAction{"What's New", func() { _ = mygo.Shell.OpenExternal(releaseURL(up.Version)) }}
	a.showToastActions(updateToastID, "Yeek "+up.Version+" is available", "Download and install the update?", "info", 0,
		toastAction{"Download and Install", func() { a.installUpdate(up) }}, whatsNew)
}

func (a *App) installUpdate(up *mygo.Update) {
	updates.Lock()
	if updates.busy {
		updates.Unlock()
		return
	}
	updates.busy = true
	updates.Unlock()
	a.showToast(updateToastID, "Downloading Yeek "+up.Version+"...", "info", 0)
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
				a.showToast(updateToastID, "Failed to install the update: "+err.Error(), "danger", 0)
				return
			}
			log.Printf("Installed %s", up.Version)
			a.showUpdateInstalledToast(up.Version)
		})
	}()
}

func (a *App) showUpdateInstalledToast(version string) {
	a.showToastActions(updateToastID, "Yeek "+version+" was installed", "Start using the new version now?", "info", 0,
		toastAction{"Relaunch Yeek", relaunchApp})
}

var relaunching atomic.Bool

func Relaunching() bool { return relaunching.Load() }

func relaunchApp() {
	relaunching.Store(true)
	for _, w := range mygo.Windows() {
		w.Close()
	}
}
