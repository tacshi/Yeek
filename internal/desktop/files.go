package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo"
	"yeek/internal/engine"
)

func (a *App) importFile() { a.openImport(nil, engine.ImportDestination{WorkspaceID: a.workspace}) }
func (a *App) importCurl() {
	if a.deferUntilInputs(a.importCurl) {
		return
	}
	a.openImport([]engine.ImportInput{{Kind: "text", Content: a.dialogValue}}, engine.ImportDestination{WorkspaceID: a.workspace})
	a.previewImport()
}
func (a *App) copyCurl() {
	a.saveActive()
	id, env := a.active, a.environment
	a.background(func() (func(), error) {
		text, err := a.Engine.Curl(a.ctx, id, env)
		if err == nil {
			mygo.Clipboard.WriteText(text)
		}
		return nil, err
	})
}
func (a *App) newGraphQL(folder string) {
	workspace := a.workspace
	a.background(func() (func(), error) {
		m, err := a.Engine.Save(a.ctx, engine.Object{"model": "http_request", "workspaceId": workspace, "folderId": nilIfEmpty(folder), "name": "GraphQL Request", "method": "POST", "bodyType": "graphql", "body": engine.Object{"query": "query {\n  \n}", "variables": "{}"}})
		return func() { a.applyModel(m); a.openRequest(s(m, "id")); a.drafts[s(m, "id")].Tab = 0 }, err
	})
}
func (a *App) saveResponse(response engine.Object) {
	id := s(response, "id")
	a.background(func() (func(), error) {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.Window, Title: "Save Response", DefaultPath: "response.json"})
		if err != nil || path == "" {
			return nil, err
		}
		return nil, a.Engine.SaveBody(id, path)
	})
}
func (a *App) chooseBodyFile(d *Draft) {
	id := d.ID
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Request Body File"})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() {
			if draft := a.drafts[id]; draft != nil {
				draft.FilePath = paths[0]
				draft.Dirty = true
			}
		}, nil
	})
}
func (a *App) chooseSyncDir() {
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Workspace Directory", Directory: true})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		path, err := filepath.Abs(paths[0])
		return func() { a.dialogValue = path; a.gitDirectory = path; a.refreshGit() }, err
	})
}

func (a *App) pickFormFile(rows *[]KV, id string, dirty *bool) {
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Choose Multipart File"})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() {
			for i := range *rows {
				if (*rows)[i].ID == id {
					(*rows)[i].File = paths[0]
					(*rows)[i].FileMode = true
					*dirty = true
				}
			}
		}, nil
	})
}

func (a *App) ImportPath(path string) {
	if a.deferUntilInputs(func() { a.ImportPath(path) }) {
		return
	}
	if a.dialogOpen && a.dialog == "imports" && a.imports != nil && !a.imports.applying {
		if a.imports.cancel != nil {
			a.imports.cancel()
		}
		a.imports.generation++
		a.imports.busy = false
		a.imports.plan = nil
		a.imports.inputs = append(a.imports.inputs, engine.ImportInput{Origin: path})
	} else {
		a.openImport([]engine.ImportInput{{Origin: path}}, engine.ImportDestination{})
	}
	a.previewImport()
}
func (a *App) ReviewImportURL(raw string) {
	a.openImport(nil, engine.ImportDestination{WorkspaceID: a.workspace})
	if a.deferUntilInputs(func() { a.imports.entry = raw }) {
		return
	}
	a.imports.entry = raw
}
func (a *App) Focus() bool {
	if a.Window == nil || a.ctx.Err() != nil {
		return false
	}
	a.Window.Show()
	a.Window.Focus()
	return true
}
func (a *App) Import() { a.importFile() }
func (a *App) Export() { a.exportFile() }
func (a *App) NewRequest() {
	if a.workspace == "" {
		a.prompt("workspace", "New Workspace", "", "")
	} else {
		a.addRequest("http_request", "")
	}
}
func (a *App) Settings() { a.prompt("settings", "Settings", "", "") }
