package desktop

import (
	"cmp"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// Yaak's updater (crates-tauri/yaak-app-client/src/updates.rs) on
// mygo.Updater: while updates are automatic, a window that is focused checks
// at most every 12 hours, and Check for Updates in the gear menu and the
// General settings checks at once. Yeek publishes one release feed, so there
// is no beta channel.

// updateCheckInterval is Yaak's MAX_UPDATE_CHECK_HOURS_STABLE.
const updateCheckInterval = 12 * time.Hour

// updateToastID is Yaak's UPDATE_TOAST_ID: an update toast replaces the last.
const updateToastID = "update-info"

// releaseURL is the GitHub release of a version, Yaak's "What's New" page.
func releaseURL(version string) string {
	return "https://github.com/tacshi/Yeek/releases/tag/v" + version
}

// updates is shared by every window of the app.
var updates struct {
	sync.Mutex
	lastCheck time.Time
	busy      bool // checking or installing
}

// appVersion is the packaged app's version, or mygo.json's in development.
func appVersion() string { return cmp.Or(mygo.App.Version(), "0.1.0") }

// maybeCheckForUpdates is Yaak's maybe_check, run when a window is focused.
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
	// Yaak waits a bit, so it's not so jarring.
	time.AfterFunc(3*time.Second, func() {
		if a.ctx.Err() == nil {
			a.Window.Update(func() { a.checkForUpdates(false) })
		}
	})
}

// checkForUpdates is Yaak's check_now. A check the user asked for says when
// Yeek is up to date or can't update; a background one only speaks up when
// there is an update, and installs it first when downloads are automatic.
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
			// Yaak's minPromiseMillis: the spinner shows for at least 500 ms.
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

// showUpdateAvailableToast is Yaak's toast for an update it can install.
func (a *App) showUpdateAvailableToast(up *mygo.Update) {
	whatsNew := toastAction{"What's New", func() { _ = mygo.Shell.OpenExternal(releaseURL(up.Version)) }}
	a.showToastActions(updateToastID, "Yeek "+up.Version+" is available", "Download and install the update?", "info", 0,
		toastAction{"Download and Install", func() { a.installUpdate(up) }}, whatsNew)
}

// installUpdate downloads and installs an update; the running app is not
// affected until it relaunches.
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

// showUpdateInstalledToast is Yaak's toast once an update is installed.
func (a *App) showUpdateInstalledToast(version string) {
	a.showToastActions(updateToastID, "Yeek "+version+" was installed", "Start using the new version now?", "info", 0,
		toastAction{"Relaunch Yeek", func() { mygo.App.Relaunch() }})
}
