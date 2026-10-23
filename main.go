package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"yeek/internal/desktop"
	"yeek/internal/engine"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() (runErr error) {
	dataDir := flag.String("data-dir", "", "Directory for workspace data")
	flag.Parse()
	name := appName(mygo.App.Name())
	setProcessName(name)
	mygo.App.SetName(name)
	dir := *dataDir
	var err error
	if dir == "" {
		dir, err = mygo.App.Path(mygo.PathUserData)
		if err != nil {
			return err
		}
		if !mygo.App.RequestSingleInstanceLock() {
			return nil
		}
	} else {
		dir, err = filepath.Abs(dir)
		if err != nil {
			return err
		}
		mygo.App.SetPath(mygo.PathUserData, dir)
		mygo.App.SetPath(mygo.PathLogs, filepath.Join(dir, "logs"))
	}
	if err := desktop.RegisterPreviewScheme(); err != nil {
		return err
	}
	e, err := engine.Open(dir)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, e.Close()) }()
	var windows []*desktop.App
	var windowsMu sync.Mutex
	ready := false
	pendingURLs := []string{}
	pending := append([]string{}, flag.Args()...)
	var open func() *desktop.App
	open = func() *desktop.App {
		a, err := desktop.New(e)
		if err != nil {
			log.Print(err)
			return nil
		}
		windowsMu.Lock()
		windows = append(windows, a)
		windowsMu.Unlock()
		a.OpenWorkspace = func(id string) {
			if w := open(); w != nil {
				w.ShowWorkspace(id)
			}
		}
		a.OpenWindow()
		return a
	}
	active := func(window *mygo.Window) *desktop.App {
		if !ready {
			return nil
		}
		for _, app := range windows {
			if app.Window == window {
				return app
			}
		}
		for i := len(windows) - 1; i >= 0; i-- {
			if windows[i].Focus() {
				return windows[i]
			}
		}
		return open()
	}
	importPath := func(path string) {
		if len(windows) == 0 {
			pending = append(pending, path)
			return
		}
		if a := active(nil); a != nil {
			a.ImportPath(path)
		}
	}
	mygo.App.OnOpenFile(importPath)
	openURL := func(raw string) {
		if !ready {
			pendingURLs = append(pendingURLs, raw)
			return
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "yeek" {
			return
		}
		if e.HandleOAuthCallback(raw) {
			if a := active(nil); a != nil {
				a.Focus()
			}
			return
		}
		if u.Host == "import-data" {
			if a := active(nil); a != nil {
				query := u.Query()
				a.ImportDeepLink(query.Get("url"), query.Get("path"), query.Get("name"))
			}
			return
		}
		if u.Host == "import" {
			if a := active(nil); a != nil {
				a.ReviewImportURL(u.Query().Get("url"))
			}
		}
	}
	mygo.App.OnOpenURL(openURL)
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		a := active(nil)
		if a == nil {
			return
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if !filepath.IsAbs(arg) {
				arg = filepath.Join(workingDir, arg)
			}
			a.ImportPath(arg)
		}
	})
	mcpCtx, stopMCP := context.WithCancel(context.Background())
	defer stopMCP()
	mcpServer := &desktop.MCPServer{Engine: e, Windows: func() []*desktop.App {
		windowsMu.Lock()
		defer windowsMu.Unlock()
		return slices.Clone(windows)
	}}
	mygo.App.WhenReady(func() {
		ready = true
		mcpServer.Run(mcpCtx)
		action := func(fn func(*desktop.App)) func(*mygo.MenuItem, *mygo.Window) {
			return func(_ *mygo.MenuItem, w *mygo.Window) {
				if a := active(w); a != nil {
					fn(a)
					if a.Window != nil {
						a.Window.Invalidate()
					}
				}
			}
		}
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "File", Submenu: []*mygo.MenuItem{
				{Label: "New Request", Accelerator: "CmdOrCtrl+N", Click: action(func(a *desktop.App) { a.NewRequest() })},
				{Label: "New Window", Accelerator: "CmdOrCtrl+Shift+N", Click: func(*mygo.MenuItem, *mygo.Window) { open() }},
				mygo.Separator(),
				{Label: "Import Collection…", Accelerator: "CmdOrCtrl+O", Click: action(func(a *desktop.App) { a.Import() })},
				{Label: "Export Workspace…", Accelerator: "CmdOrCtrl+Shift+E", Click: action(func(a *desktop.App) { a.Export() })},
				{Label: "Settings…", Accelerator: "CmdOrCtrl+,", Click: action(func(a *desktop.App) { a.Settings() })},
				mygo.Separator(), {Role: mygo.RoleClose},
			}},
			{Role: mygo.RoleEditMenu}, {Role: mygo.RoleViewMenu}, {Role: mygo.RoleWindowMenu},
		}))
		a := open()
		if a != nil {
			for _, path := range pending {
				a.ImportPath(path)
			}
		}
		pending = nil
		for _, raw := range pendingURLs {
			openURL(raw)
		}
		pendingURLs = nil
	})
	mygo.App.OnWindowAllClosed(func() {
		if desktop.Relaunching() {
			mygo.App.Relaunch()
		} else {
			mygo.App.Quit()
		}
	})
	mygo.App.OnQuit(func() {
		stopMCP()
		for _, a := range windows {
			a.Close()
		}
	})
	err = mygo.App.Run()
	for _, a := range windows {
		a.Wait()
	}
	return err
}

func appName(packaged string) string {
	if strings.HasPrefix(packaged, "Yeek") {
		return packaged
	}
	return "Yeek"
}
