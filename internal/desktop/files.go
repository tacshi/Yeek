package desktop

import (
	"os"
	"path/filepath"
	"time"
	"uuid"

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
		if err != nil {
			return nil, err
		}
		mygo.Clipboard.WriteText(text)
		return a.copiedToast, nil
	})
}

// copyGrpcurl is Yaak's Copy as gRPCurl.
func (a *App) copyGrpcurl(id string) {
	a.saveActive()
	env := a.environment
	var files []string
	if d := a.drafts[id]; d != nil {
		files = a.protoFiles(d.ID)
	}
	a.background(func() (func(), error) {
		text, err := a.Engine.Grpcurl(a.ctx, id, env, files)
		if err != nil {
			return nil, err
		}
		mygo.Clipboard.WriteText(text)
		return a.copiedToast, nil
	})
}

func (a *App) copiedToast() {
	a.showToast("", "Command copied to clipboard", "success", 5*time.Second)
}

// newGraphQL is Yaak's GraphQL item of the create menu.
func (a *App) newGraphQL(folder string) {
	a.createRequest(engine.Object{"model": "http_request", "folderId": nilIfEmpty(folder), "method": "POST", "bodyType": "graphql",
		"headers": []any{engine.Object{"name": "Content-Type", "value": "application/json", "enabled": true, "id": uuid.NewV4().String()}}})
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

// chooseSyncDir picks the directory the workspace syncs with.
func (a *App) chooseSyncDir(workspace string) {
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Workspace Directory", Directory: true})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		path, err := filepath.Abs(paths[0])
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(path)
		return func() {
			if len(entries) > 0 {
				a.notEmptySyncDir = path
				return
			}
			a.notEmptySyncDir = ""
			a.setSyncDir(workspace, path)
		}, err
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
