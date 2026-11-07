package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"yeek/internal/cli"
	"yeek/locales"
)

type commandStatus struct {
	supported, installed, other, translocated bool
	link, taken                               string
}

type commandUI struct {
	status   commandStatus
	checked  time.Time
	checking bool
	busy     bool
	removing bool
}

var customDataDir bool

func UseCustomDataDir() { customDataDir = true }

func commandName() string { return cli.CommandName(mygo.App.Name()) }

func executable() string {
	p, err := os.Executable()
	if err != nil {
		return commandName()
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p
}

func (a *App) cliInvocation(status commandStatus) (string, []string) {
	command := executable()
	if status.installed {
		command = commandName()
	}
	args := []string{"mcp"}
	if customDataDir {
		args = []string{"--data-dir", a.Engine.DataDir, "mcp"}
	}
	return command, args
}

func (a *App) checkCommand() {
	st := &a.command
	if st.checking || st.busy || time.Since(st.checked) < 3*time.Second {
		return
	}
	st.checking = true
	name, exe := commandName(), executable()
	a.background(func() (func(), error) {
		status := commandState(name, exe)
		return func() { st.status, st.checked, st.checking = status, time.Now(), false }, nil
	})
}

func (a *App) changeCommand(remove bool) {
	st := &a.command
	st.busy, st.removing = true, remove
	name, exe := commandName(), executable()
	a.background(func() (func(), error) {
		var err error
		if remove {
			err = removeCommand(name, exe)
		} else {
			err = installCommand(name, exe)
		}
		status := commandState(name, exe)
		return func() {
			st.busy, st.status, st.checked = false, status, time.Now()
			if err != nil {
				a.showToast("", commandFailed(name, remove, err), "danger", 10*time.Second)
			}
		}, nil
	})
}

func commandFailed(name string, remove bool, err error) string {
	if remove {
		return locales.CouldNotRemoveCommand(name, err.Error())
	}
	return locales.CouldNotInstallCommand(name, err.Error())
}

func shellWord(s string) string {
	if runtime.GOOS == "windows" {
		if s != "" && !strings.ContainsAny(s, " '\"$`&|<>()[]{}^;,=!%@#~+\t") {
			return s
		}
		return `"` + s + `"`
	}
	return shellQuote(s)
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " '\"\\$`!*?;&|<>()[]{}#~\t\n") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
