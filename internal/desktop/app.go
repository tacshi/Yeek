package desktop

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type KV struct {
	ID, Name, Value, File string
	Filename, ContentType string
	FileMode              bool
	Extra                 engine.Object
	Enabled               bool
}
type Draft struct {
	FilePath                                                string
	OperationName, DescriptionMode                          string
	OperationExplicit                                       bool
	FilterProvider                                          string
	FilterPending, FilterOpen, InheritedClosed              bool
	TreeOpen                                                map[string]bool
	PrettySource, PrettyBody                                string
	ResponseFilter, FilteredBody, FilterError, ResponseMode string
	FilterSource                                            string
	// ConnEvent is the gRPC or WebSocket event shown (-1 for none), of
	// ConnEventFor's connection; HexDump and ShowLarge are how it shows.
	ConnEvent                                                                                                         int
	ConnEventFor                                                                                                      string
	EventSplit                                                                                                        float32
	HexDump                                                                                                           map[string]bool
	ShowLarge                                                                                                         bool
	Services                                                                                                          []engine.GRPCService
	MessageType                                                                                                       string
	LastEdit                                                                                                          time.Time
	SavedVersion                                                                                                      string
	ID, Kind, Name, URL, Method, BodyType, Body, Query, Variables, AuthType, Description, Message, Service, RPCMethod string
	Headers, Parameters, Form                                                                                         []KV
	Auth                                                                                                              map[string]string
	Tab, ResponseTab, PartIndex, EventIndex, TimelineIndex                                                            int
	TimelineText, TimelineRaw                                                                                         bool
	Dirty                                                                                                             bool
	Model                                                                                                             engine.Object
	outline                                                                                                           *jsonOutline
	outlineSource, crumbKey                                                                                           string

	// AuthDisabled is Yaak's authentication.disabled: nil or false (enabled),
	// true (disabled), or a template ("Enabled when...").
	AuthDisabled any
	authPreview  authConditionPreview
}

type App struct {
	oauthBrowserCount          int
	oauthEditors               map[string]*oauthEditorState
	methodError                string
	partEditor                 *multipartPartEditor
	exports                    *exportDialog
	imports                    *importDialog
	certificates               *certificateEditor
	proxyEditor                *proxyEditor
	workspaceNetworkEditors    map[string]*workspaceNetworkEditor
	inView                     bool
	afterInput                 []func()
	cookieSelections           map[string]string
	cookies                    *cookieManager
	modelVersion               uint64
	templateCache              templateVariableCache
	templateForm               *templateForm
	editors                    map[string]*documentEditor
	workerDone                 chan struct{}
	backgroundWorkers          sync.WaitGroup
	images                     map[string]ui.ImageSource
	graphqlStates              map[string]*graphQLState
	docsTrail                  map[string][]string
	docsSearch                 string
	docsSplit                  float32
	scopeDraft, workspaceDraft *Draft

	gitState                                  *engine.GitStatus
	gitInfo                                   *engine.GitBranchInfo
	gitNoRepo                                 bool
	gitRefreshed                              time.Time
	gitCommitMessage, syncing, divergedChoice string
	notEmptySyncDir                           string
	switchTarget, moveTarget                  string
	rememberWindow                            bool
	moving                                    []string
	// OpenWorkspace opens a workspace in a new window.
	OpenWorkspace                              func(id string)
	gitRemotes                                 []engine.GitRemoteInfo
	diverged                                   *engine.GitResult
	asking                                     *askState
	stopSync                                   func()
	connections                                map[string]engine.Object
	closing                                    bool
	Engine                                     *engine.Engine
	Window                                     *mygo.Window
	ctx                                        context.Context
	cancel                                     context.CancelFunc
	work                                       chan func() (func(), error)
	closeOnce                                  sync.Once
	models                                     map[string]engine.Object
	workspace, environment, cookieJar, active  string
	tabs                                       []string
	drafts                                     map[string]*Draft
	responses                                  map[string]engine.Object
	bodies                                     map[string]string
	running                                    map[string]bool
	expanded                                   map[string]bool
	sidebarWidth, requestWidth                 float32
	vertical, hideSidebar                      bool
	search                                     string
	dialog, dialogTitle, dialogValue, dialogID string
	dialogOpen                                 bool
	modalTab                                   int
	envDraft                                   []KV
	errorMessage                               string
	status                                     string
	settings                                   engine.Object
	palette                                    paletteState
	switcher                                   switcherState
	focusURL, focusSidebar, focusFilter        bool
	sidebarFocused                             bool
	tree                                       treeState
	grpcSchemas                                map[string]*grpcSchemaState
	pinnedConnections                          map[string]string
	activeFolder                               string
	revealedLarge                              map[string]bool
	previewDraft                               *Draft
	deleteIDs                                  []string
	detailsOpen, dismissed, revealed, bulkEdit map[string]bool
	bulkText, placeholderNames                 map[string]string
	showEnvValues, kvMask                      bool
	newEnv                                     newEnvironmentForm
	environmentColor                           string
	toasts                                     []toastItem
	toastSerial                                int
	folderEnvID, hotkeyFilter, recordingHotkey string
	folderEnvRows                              []KV
	valuePrompts                               []*valuePrompt
	testMode                                   bool
}

func New(e *engine.Engine) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{Engine: e, ctx: ctx, cancel: cancel, work: make(chan func() (func(), error), 128), workerDone: make(chan struct{}), models: map[string]engine.Object{}, connections: map[string]engine.Object{}, drafts: map[string]*Draft{}, responses: map[string]engine.Object{}, bodies: map[string]string{}, running: map[string]bool{}, expanded: map[string]bool{}, revealedLarge: map[string]bool{}, sidebarWidth: 260, requestWidth: 500, status: "", settings: engine.Object{}}
	models, err := e.Snapshot(ctx, "")
	if err != nil {
		cancel()
		return nil, err
	}
	a.replace(models)
	for _, m := range models {
		if s(m, "model") == "workspace" {
			a.workspace = s(m, "id")
			break
		}
	}
	a.restoreSession()
	if a.workspace != "" {
		models, err = e.Snapshot(ctx, a.workspace)
		if err != nil {
			cancel()
			return nil, err
		}
		a.replace(models)
	}
	if m := a.models[a.active]; m != nil {
		a.drafts[a.active] = newDraft(m)
	} else {
		a.active = ""
	}
	return a, nil
}
func (a *App) OpenWindow() {
	style := mygo.TitleBarHidden
	if b(a.settings, "useNativeTitlebar") {
		style = mygo.TitleBarDefault
	}
	a.Window = mygo.NewWindow(mygo.WindowOptions{Title: "Yeek", Width: 1360, Height: 860, MinWidth: 840, MinHeight: 520, StateKey: "main", TitleBarStyle: style, TitleBarHeight: headerHeight, TrafficLightPosition: &mygo.Point{X: 14, Y: (headerHeight - 14) / 2}, BackgroundColor: "light-dark(#ffffff, #1b1a2b)", Content: ui.View(a.View)})
	a.Window.OnClose(func(event *mygo.CloseEvent) {
		if a.closing {
			return
		}
		event.PreventDefault()
		a.persistSession()
		for _, draft := range a.drafts {
			a.save(draft)
		}
		a.run(func() (func(), error) { return func() { a.closing = true; a.Window.Close() }, nil })
	})
	a.Window.OnFileDrop(func(event *mygo.FileDropEvent) {
		for _, path := range event.Paths {
			a.ImportPath(path)
		}
	})
	a.Window.OnClosed(func() { a.Close() })
	go a.worker()
	if a.active != "" {
		a.openRequest(a.active)
	}
	changes, unsubscribe := a.Engine.Subscribe()
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-a.ctx.Done():
				return
			case batch := <-changes:
				content := map[string]string{}
				for _, change := range batch {
					m := o(change, "model")
					if s(m, "model") == "http_response" {
						if raw, _, err := a.Engine.BodyPreview(s(m, "id"), 2<<20); err == nil {
							content[s(m, "id")] = string(raw)
						}
					}
				}
				if batch == nil {
					all, err := a.Engine.Store.List(a.ctx, "", "")
					if err != nil {
						continue
					}
					a.Window.Update(func() { a.replace(all) })
					continue
				}
				a.Window.Update(func() {
					for _, change := range batch {
						m := o(change, "model")
						if s(o(change, "change"), "type") == "delete" {
							if d := a.drafts[s(m, "id")]; d != nil {
								d.Dirty = false
							}
							delete(a.models, s(m, "id"))
							a.modelVersion++
							if a.active == s(m, "id") {
								a.closeTab(a.active)
							}
						} else {
							a.applyModel(m)
						}
					}
					maps.Copy(a.bodies, content)
				})
			}
		}
	}()
}
func (a *App) Close() { a.closeOnce.Do(func() { a.cancel() }) }
func (a *App) worker() {
	defer close(a.workerDone)
	for {
		select {
		case <-a.ctx.Done():
			return
		case job := <-a.work:
			done, err := job()
			if a.Window != nil {
				a.Window.Update(func() {
					if err != nil {
						a.errorMessage = err.Error()
					}
					if done != nil && err == nil {
						done()
					}
				})
			}
		}
	}
}
func (a *App) run(job func() (func(), error)) {
	if a.testMode {
		done, err := job()
		if err != nil {
			a.errorMessage = err.Error()
		}
		if done != nil && err == nil {
			done()
		}
		return
	}
	select {
	case a.work <- job:
	case <-a.ctx.Done():
	}
}
func (a *App) replace(models []engine.Object) {
	a.modelVersion++
	a.models = map[string]engine.Object{}
	for _, m := range models {
		a.applyModel(m)
	}
}
func (a *App) applyModel(m engine.Object) {
	a.modelVersion++
	if s(m, "model") == "oauth_token" {
		for _, state := range a.oauthEditors {
			if !state.busy {
				state.source = ""
			}
		}
		return
	}
	if s(m, "model") == "graphql_introspection" {
		a.applyGraphQLSchema(m)
	}
	if s(m, "model") == "websocket_connection" || s(m, "model") == "grpc_connection" {
		a.connections[s(m, "requestId")] = m
		a.running[s(m, "requestId")] = s(m, "state") != "closed"
	}
	a.models[s(m, "id")] = m
	if s(m, "model") == "settings" {
		a.settings = m
	}
	if s(m, "model") == "http_response" {
		id := s(m, "requestId")
		old := a.responses[id]
		if old == nil || s(m, "createdAt") >= s(old, "createdAt") {
			a.responses[id] = m
		}
	}
}
func (a *App) list(kind string) []engine.Object {
	out := []engine.Object{}
	for _, m := range a.models {
		if s(m, "model") == kind && (s(m, "workspaceId") == "" || s(m, "workspaceId") == a.workspace) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(x, y engine.Object) int {
		if n(x, "sortPriority") < n(y, "sortPriority") {
			return -1
		}
		if n(x, "sortPriority") > n(y, "sortPriority") {
			return 1
		}
		return strings.Compare(s(x, "createdAt"), s(y, "createdAt"))
	})
	return out
}

// ShowWorkspace opens a workspace in the window.
func (a *App) ShowWorkspace(id string) { a.switchWorkspace(id) }

func (a *App) switchWorkspace(id string) {
	if a.deferUntilInputs(func() { a.switchWorkspace(id) }) {
		return
	}
	a.saveActive()
	a.run(func() (func(), error) {
		models, err := a.Engine.Snapshot(a.ctx, id)
		return func() {
			a.workspace = id
			a.active = ""
			a.tabs = nil
			a.environment = ""
			a.cookieJar = ""
			a.replace(models)
			a.activeCookieJar()
		}, err
	})
}
func (a *App) openRequest(id string) {
	if a.deferUntilInputs(func() { a.openRequest(id) }) {
		return
	}
	a.saveActive()
	m := a.models[id]
	if m == nil {
		return
	}
	a.activeFolder = ""
	// Most recent first, as Yaak's recent requests are.
	a.tabs = append([]string{id}, slices.DeleteFunc(a.tabs, func(tab string) bool { return tab == id })...)
	a.active = id
	if a.drafts[id] == nil {
		a.drafts[id] = newDraft(m)
	}
	if connection := a.connections[id]; connection != nil {
		connectionID, kind := s(connection, "id"), "websocket_event"
		if s(connection, "model") == "grpc_connection" {
			kind = "grpc_event"
		}
		a.run(func() (func(), error) {
			events, err := a.Engine.Store.Find(a.ctx, kind, "connectionId", connectionID)
			return func() {
				for _, event := range events {
					a.applyModel(event)
				}
			}, err
		})
	}
	if response := a.responses[id]; response != nil {
		rid := s(response, "id")
		a.run(func() (func(), error) {
			body, _, err := a.Engine.BodyPreview(rid, 2<<20)
			if err != nil {
				return nil, err
			}
			events, err := a.Engine.Store.Find(a.ctx, "http_response_event", "responseId", rid)
			return func() {
				a.bodies[rid] = string(body)
				for _, event := range events {
					a.applyModel(event)
				}
			}, err
		})
	}
}
func (a *App) closeTab(id string) {
	if a.deferUntilInputs(func() { a.closeTab(id) }) {
		return
	}
	if d := a.drafts[id]; d != nil {
		a.save(d)
	}
	i := slices.Index(a.tabs, id)
	if i < 0 {
		return
	}
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active == id {
		a.active = ""
		if len(a.tabs) > 0 {
			a.active = a.tabs[min(i, len(a.tabs)-1)]
		}
	}
}
func (a *App) saveActive() {
	if a.deferUntilInputs(a.saveActive) {
		return
	}
	if d := a.drafts[a.active]; d != nil {
		a.save(d)
	}
}
func (a *App) save(d *Draft) {
	if a.deferUntilInputs(func() { a.save(d) }) {
		return
	}
	if !d.Dirty {
		return
	}
	model := d.object()
	d.Dirty = false
	a.run(func() (func(), error) { _, err := a.Engine.Save(a.ctx, model); return nil, err })
}
func (a *App) addRequest(kind, folder string) {
	a.createRequest(engine.Object{"model": kind, "folderId": nilIfEmpty(folder)})
}

// createRequest is Yaak's createRequestAndNavigate: a new request goes
// below the active one, in its folder, else at the top.
func (a *App) createRequest(m engine.Object) {
	m["workspaceId"] = a.workspace
	if active := a.models[a.active]; active != nil && a.activeFolder == "" {
		m["sortPriority"] = n(active, "sortPriority")
		if m["folderId"] == nil {
			m["folderId"] = active["folderId"]
		}
	} else {
		m["sortPriority"] = float64(-time.Now().UnixMilli())
	}
	a.run(func() (func(), error) {
		m, err := a.Engine.Save(a.ctx, m)
		return func() {
			if m != nil {
				a.applyModel(m)
				a.openRequest(s(m, "id"))
			}
		}, err
	})
}
func (a *App) send() {
	if a.deferUntilInputs(a.send) {
		return
	}
	d := a.drafts[a.active]
	if d == nil {
		return
	}
	// Yaak's request.send: an open WebSocket, or a gRPC call streaming
	// from the client, sends the message; another running call is
	// cancelled.
	if a.running[d.ID] {
		kind := a.grpcMethodType(d)
		switch {
		case d.Kind == "websocket_request" && a.connected(d):
			a.sendMessage(d)
		case d.Kind == "grpc_request" && (kind == "client_streaming" || kind == "streaming"):
			a.sendMessage(d)
		default:
			a.Engine.Cancel(d.ID)
		}
		return
	}
	a.save(d)
	id, env, jar, kind, files := d.ID, a.environment, a.cookieJar, d.Kind, a.protoFiles(d.ID)
	a.running[id] = true
	a.run(func() (func(), error) {
		go func() {
			var err error
			var response engine.Object
			ctx := a.authorizationContext(a.ctx)
			switch kind {
			case "http_request":
				response, err = a.Engine.SendHTTP(ctx, id, engine.SendOptions{EnvironmentID: env, CookieJarID: jar})
			case "websocket_request":
				_, err = a.Engine.ConnectWebSocket(ctx, id, engine.SendOptions{EnvironmentID: env, CookieJarID: jar})
			case "grpc_request":
				_, err = a.Engine.StartGRPC(ctx, id, env, files)
			}
			a.Window.Update(func() {
				if response != nil {
					a.applyModel(response)
				}
				if kind == "http_request" || err != nil {
					a.running[id] = false
				}
				if err != nil && (response == nil || a.active != id) {
					a.errorMessage = err.Error()
				}
			})
		}()
		return nil, nil
	})
}

// sendFolder sends every HTTP request in a folder one at a time, in sidebar order.
func (a *App) sendFolder(folder string) { a.sendRequests(a.folderRequests(folder)) }

// sendRequests sends HTTP requests one after another, as Send All does.
func (a *App) sendRequests(ids []string) {
	if a.deferUntilInputs(func() { a.sendRequests(ids) }) {
		return
	}
	ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return a.running[id] })
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		if d := a.drafts[id]; d != nil && d.Dirty {
			a.save(d)
		}
		a.running[id] = true
	}
	env, jar := a.environment, a.cookieJar
	a.background(func() (func(), error) {
		ctx := a.authorizationContext(a.ctx)
		responses := make([]engine.Object, 0, len(ids))
		sent, failed := 0, 0
		var last error
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			sent++
			response, err := a.Engine.SendHTTP(ctx, id, engine.SendOptions{EnvironmentID: env, CookieJarID: jar})
			if err != nil {
				failed++
				last = err
			}
			if response != nil {
				responses = append(responses, response)
			}
			if !a.testMode {
				a.Window.Update(func() {
					if response != nil {
						a.applyModel(response)
					}
					a.running[id] = false
				})
			}
		}
		return func() {
			for _, response := range responses {
				a.applyModel(response)
			}
			for _, id := range ids {
				a.running[id] = false
			}
			if err := sendAllError(failed, sent, last); err != nil {
				a.errorMessage = err.Error()
			}
		}, nil
	})
}
func sendAllError(failed, total int, last error) error {
	if failed == 0 {
		return nil
	}
	if total == 1 {
		return last
	}
	return fmt.Errorf("%d of %d requests failed: %w", failed, total, last)
}
func (a *App) prompt(kind, title, value, id string) {
	a.errorMessage = ""
	if a.dialog != kind {
		a.modalTab = 0
	}
	a.dialog, a.dialogTitle, a.dialogValue, a.dialogID = kind, title, value, id
	if kind == "workspace_settings" {
		a.workspaceDraft = nil
	}
	a.dialogOpen = true
}
func (a *App) saveModel(m engine.Object) {
	copy := deepCopy(m)
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, copy)
		return func() {
			if saved != nil {
				a.applyModel(saved)
			}
		}, err
	})
}
func (a *App) duplicate(id string) {
	if a.deferUntilInputs(func() { a.duplicate(id) }) {
		return
	}
	a.saveActive()
	a.run(func() (func(), error) {
		created, err := a.Engine.Duplicate(a.ctx, id)
		if err != nil {
			return nil, err
		}
		m, err := a.Engine.Store.Get(a.ctx, created)
		var copies []engine.Object
		if err == nil && s(m, "model") == "folder" {
			// The folder's contents were copied with it.
			copies, err = a.Engine.Store.List(a.ctx, "", s(m, "workspaceId"))
		}
		return func() {
			for _, copy := range copies {
				if a.models[s(copy, "id")] == nil {
					a.applyModel(copy)
				}
			}
			a.applyModel(m)
			if strings.HasSuffix(s(m, "model"), "_request") {
				a.openRequest(created)
			} else if s(m, "model") == "folder" {
				a.openFolder(created)
			}
		}, err
	})
}
func (a *App) deleteModel(id string) {
	a.run(func() (func(), error) {
		return func() { delete(a.models, id); a.modelVersion++; a.closeTab(id) }, a.Engine.Delete(a.ctx, id)
	})
}
func newDraft(m engine.Object) *Draft {
	body, auth := o(m, "body"), o(m, "authentication")
	d := &Draft{FilePath: s(body, "filePath"), OperationName: s(body, "operationName"), OperationExplicit: body["operationName"] != nil, Tab: 1, TimelineIndex: -1, ConnEvent: -1, MessageType: "text", ID: s(m, "id"), Kind: s(m, "model"), Name: s(m, "name"), URL: s(m, "url"), Method: s(m, "method"), BodyType: s(m, "bodyType"), Body: s(body, "text"), Query: s(body, "query"), Variables: s(body, "variables"), Description: s(m, "description"), Message: s(m, "message"), Service: s(m, "service"), RPCMethod: s(m, "method"), AuthType: s(m, "authenticationType"), Auth: map[string]string{}, Headers: kvRows(m, "headers"), Parameters: kvRows(m, "urlParameters"), Form: kvRows(body, "form"), Model: deepCopy(m)}
	d.Auth = oauthAuthStrings(auth)
	if disabled, ok := auth["disabled"]; ok {
		d.AuthDisabled = disabled
		delete(d.Auth, "disabled")
	}
	if d.Auth["location"] == "" && d.Auth["in"] != "" {
		d.Auth["location"] = d.Auth["in"]
		delete(d.Auth, "in")
	}
	if d.Kind != "http_request" {
		d.Tab = 0
	}
	if d.Kind == "grpc_request" {
		d.Headers = kvRows(m, "metadata")
	}
	return d
}
func (d *Draft) object() engine.Object {
	m := deepCopy(d.Model)
	maps.Copy(m, engine.Object{"id": d.ID, "name": d.Name, "url": d.URL, "method": d.Method, "description": d.Description, "bodyType": nilIfEmpty(d.BodyType), "body": d.bodyObject(), "headers": rowObjects(d.Headers), "urlParameters": rowObjects(slices.DeleteFunc(slices.Clone(d.Parameters), func(row KV) bool { return strings.HasPrefix(row.ID, placeholderRowIDTag) && row.Value == "" })), "authenticationType": nilIfEmpty(d.AuthType), "message": d.Message})
	if !d.OperationExplicit && d.OperationName == "" {
		delete(o(m, "body"), "operationName")
	}
	m["authentication"] = draftAuthObject(d)
	if d.Kind == "grpc_request" {
		m["metadata"] = rowObjects(d.Headers)
		m["service"] = nilIfEmpty(d.Service)
		m["method"] = nilIfEmpty(d.RPCMethod)
	}
	return m
}
func kvRows(m engine.Object, key string) []KV {
	rows := []KV{}
	for _, v := range oslice(m, key) {
		enabled := true
		if on, ok := v["enabled"].(bool); ok {
			enabled = on
		}
		rows = append(rows, KV{ID: s(v, "id"), Name: s(v, "name"), Value: s(v, "value"), File: s(v, "file"), Filename: s(v, "filename"), ContentType: s(v, "contentType"), FileMode: key == "form" && engine.IsFileFormField(v), Extra: deepCopy(v), Enabled: enabled})
	}
	return rows
}
func rowObjects(rows []KV) []any {
	out := []any{}
	for _, r := range rows {
		if r.Name == "" && r.Value == "" && r.File == "" && !r.FileMode && r.ContentType == "" && r.Filename == "" {
			continue
		}
		value := deepCopy(r.Extra)
		maps.Copy(value, engine.Object{"id": r.ID, "name": r.Name, "enabled": r.Enabled})
		delete(value, "isFile")
		if r.FileMode || r.File != "" {
			value["type"], value["file"] = "file", r.File
			delete(value, "value")
		} else {
			value["value"] = r.Value
			delete(value, "file")
			if s(value, "type") == "file" {
				delete(value, "type")
			}
		}
		for key, field := range map[string]string{"filename": r.Filename, "contentType": r.ContentType} {
			if field != "" {
				value[key] = field
			} else {
				delete(value, key)
			}
		}
		out = append(out, value)
	}
	return out
}
func s(m engine.Object, k string) string { v, _ := m[k].(string); return v }
func n(m engine.Object, k string) float64 {
	switch v := m[k].(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	}
	return 0
}
func b(m engine.Object, k string) bool { v, _ := m[k].(bool); return v }
func o(m engine.Object, k string) engine.Object {
	v, _ := m[k].(map[string]any)
	if v == nil {
		return engine.Object{}
	}
	return v
}
func oslice(m engine.Object, k string) []engine.Object {
	out := []engine.Object{}
	if v, ok := m[k].([]any); ok {
		for _, v := range v {
			if m, ok := v.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}
func deepCopy(m engine.Object) engine.Object {
	data, _ := json.Marshal(m)
	var copy engine.Object
	_ = json.Unmarshal(data, &copy)
	return copy
}
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// requestName is Yaak's resolvedModelName: the name, else for a request
// what its URL or kind says.
func requestName(m engine.Object) string {
	if _, request := m["url"]; !request || s(m, "name") != "" {
		return s(m, "name")
	}
	// Template tags show what is inside them.
	url := s(m, "url")
	r := []rune(url)
	var b strings.Builder
	at := 0
	for _, tag := range templateTags(url) {
		b.WriteString(string(r[at:tag.Start]))
		b.WriteString(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(string(r[tag.Start:tag.End]), "${["), "]}")))
		at = tag.End
	}
	b.WriteString(string(r[at:]))
	url = b.String()
	if strings.TrimSpace(url) == "" {
		switch {
		case s(m, "model") == "http_request" && s(m, "bodyType") == "graphql":
			return "GraphQL Request"
		case s(m, "model") == "http_request":
			return "HTTP Request"
		case s(m, "model") == "websocket_request":
			return "WebSocket Request"
		}
		return "gRPC Request"
	}
	// gRPC requests are named by their method.
	if s(m, "model") == "grpc_request" && s(m, "service") != "" && s(m, "method") != "" {
		service := s(m, "service")
		return service[strings.LastIndex(service, ".")+1:] + "/" + s(m, "method")
	}
	for _, scheme := range []string{"http://", "https://", "ws://", "wss://"} {
		if strings.HasPrefix(url, scheme) {
			return strings.TrimPrefix(url, scheme)
		}
	}
	return url
}

// sizeText is Yaak's formatSize: decimal units, to one place.
func sizeText(n float64) string {
	num, unit := n, "B"
	switch {
	case n > 1000*1000*1000:
		num, unit = n/1000/1000/1000, "GB"
	case n > 1000*1000:
		num, unit = n/1000/1000, "MB"
	case n > 1000:
		num, unit = n/1000, "KB"
	}
	return strconv.FormatFloat(math.Round(num*10)/10, 'f', -1, 64) + " " + unit
}

func stringifyDraft(d *Draft) string {
	value, _ := json.Marshal(d.object(), json.Deterministic(true))
	return string(value)
}

// background starts I/O after queued model writes, keeping saves and window closing responsive.
func (a *App) background(job func() (func(), error)) {
	if a.testMode {
		a.run(job)
		return
	}
	a.run(func() (func(), error) {
		a.backgroundWorkers.Go(func() {
			done, err := job()
			if a.ctx.Err() != nil {
				return
			}
			a.Window.Update(func() {
				if err != nil {
					a.errorMessage = err.Error()
				} else if done != nil {
					done()
				}
			})
		})
		return nil, nil
	})
}
func (a *App) Wait() {
	if a.workerDone != nil {
		<-a.workerDone
	}
	a.backgroundWorkers.Wait()
}
