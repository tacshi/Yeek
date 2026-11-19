package desktop

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
)

func resetUpdates(t *testing.T) {
	t.Helper()
	reset := func() {
		updates.Lock()
		updates.checking, updates.state, updates.version, updates.offer, updates.lastCheck = false, updateIdle, "", nil, time.Time{}
		updates.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestUpdateStateMachine(t *testing.T) {
	resetUpdates(t)
	if ok, _, _ := beginUpdateCheck(); !ok {
		t.Fatal("first check refused")
	}
	if ok, state, _ := beginUpdateCheck(); ok || state != "checking" {
		t.Fatal("second check ran while the first was checking", state)
	}
	up := &mygo.Update{Version: "9.9.9"}
	if reshow, start := offerUpdate(up, false); reshow != nil || start {
		t.Fatal("first offer", reshow, start)
	}
	if ok, _, _ := beginUpdateCheck(); !ok {
		t.Fatal("a check while an update is offered should run")
	}
	if reshow, _ := offerUpdate(&mygo.Update{Version: "9.9.9"}, false); reshow != up {
		t.Fatal("the same version should show its prompt again")
	}
	newer := &mygo.Update{Version: "9.9.10"}
	beginUpdateCheck()
	if reshow, _ := offerUpdate(newer, false); reshow != nil || updates.version != "9.9.10" {
		t.Fatal("a newer version replaces the prompt")
	}
	if ok, _, _ := beginUpdateInstall(newer); !ok {
		t.Fatal("install refused")
	}
	if ok, state, version := beginUpdateCheck(); ok || state != updateDownloading || version != "9.9.10" {
		t.Fatal("a check during a download should report it", ok, state, version)
	}
	if ok, state, _ := beginUpdateInstall(newer); ok || state != updateDownloading {
		t.Fatal("a second install ran", state)
	}
	endUpdateInstall(newer, errors.New("boom"), updateOffered)
	if updates.state != updateOffered || updates.offer != newer {
		t.Fatal("a failed install should keep the offer", updates.state)
	}
	beginUpdateInstall(newer)
	endUpdateInstall(newer, nil, updateOffered)
	if ok, state, version := beginUpdateCheck(); ok || state != updateInstalled || version != "9.9.10" {
		t.Fatal("an installed update should stay installed until relaunch", ok, state, version)
	}
	if !updateBusy() {
		t.Fatal("background checks should stop after an install")
	}
}

func TestUpdateBackgroundDownload(t *testing.T) {
	resetUpdates(t)
	beginUpdateCheck()
	up := &mygo.Update{Version: "9.9.9"}
	if _, start := offerUpdate(up, true); !start || updates.state != updateDownloading {
		t.Fatal("background download did not start", updates.state)
	}
	endUpdateInstall(up, errors.New("boom"), updateIdle)
	if updates.state != updateIdle || updates.offer != nil || updateBusy() {
		t.Fatal("a failed background download should leave nothing in hand", updates.state)
	}
}

func TestUpdateBusyToasts(t *testing.T) {
	a, _ := cookieApp(t)
	tt := ui.NewTester(a.View, 1360, 860)
	a.showUpdateBusy("checking", "")
	tt.Frame()
	if !tt.HasText("Already checking for updates.") {
		t.Fatal(tt.Texts())
	}
	a.showUpdateBusy(updateDownloading, "9.9.9")
	tt.Frame()
	if !tt.HasText("Yeek 9.9.9 is downloading.") {
		t.Fatal(tt.Texts())
	}
	a.showUpdateBusy(updateInstalled, "9.9.9")
	tt.Frame()
	if !tt.HasText("Yeek 9.9.9 was installed") || !tt.HasText("Relaunch Yeek") {
		t.Fatal(tt.Texts())
	}
}
