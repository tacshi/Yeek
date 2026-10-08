package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	api "yeek/plugin"
)

type PluginInfo struct {
	ID, Path, Name, Version, Description, Error string
	Enabled                                     bool
}
type loadedPlugin struct {
	info       PluginInfo
	definition api.Definition
	mu         sync.Mutex
}

func pluginSymbols() interp.Exports {
	return interp.Exports{"yeek/plugin/plugin": {
		"Context": reflect.ValueOf((*api.Context)(nil)), "Definition": reflect.ValueOf((*api.Definition)(nil)), "Field": reflect.ValueOf((*api.Field)(nil)), "Template": reflect.ValueOf((*api.Template)(nil)), "Request": reflect.ValueOf((*api.Request)(nil)), "Response": reflect.ValueOf((*api.Response)(nil)), "Authentication": reflect.ValueOf((*api.Authentication)(nil)), "Action": reflect.ValueOf((*api.Action)(nil)), "Theme": reflect.ValueOf((*api.Theme)(nil)),
	}}
}
func loadGoPlugin(ctx context.Context, path string) (definition api.Definition, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("plugin initialization failed: %v", failure)
		}
	}()
	source, err := os.ReadFile(path) // #nosec G304 -- plugin source is explicitly selected in the local plugin manager.
	if err != nil {
		return definition, err
	}
	syntax, err := parser.ParseFile(token.NewFileSet(), path, source, parser.PackageClauseOnly)
	if err != nil {
		return definition, err
	}
	vm := interp.New(interp.Options{})
	if err = vm.Use(stdlib.Symbols); err != nil {
		return definition, err
	}
	if err = vm.Use(pluginSymbols()); err != nil {
		return definition, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err = vm.EvalWithContext(ctx, string(source)); err != nil {
		return definition, err
	}
	value, err := vm.Eval(syntax.Name.Name + ".Plugin")
	if err != nil {
		return definition, errors.New("export a Plugin variable of type plugin.Definition")
	}
	definition, ok := value.Interface().(api.Definition)
	if !ok {
		return definition, errors.New("plugin must have type plugin.Definition")
	}
	if definition.Name == "" {
		return definition, errors.New("plugin needs a name")
	}
	return definition, nil
}
func (e *Engine) LoadPlugins(ctx context.Context) error {
	models, err := e.Store.List(ctx, "plugin", "")
	if err != nil {
		return err
	}
	loaded := []*loadedPlugin{}
	for _, m := range models {
		p := &loadedPlugin{info: PluginInfo{ID: str(m, "id"), Path: str(m, "directory"), Enabled: boolean(m, "enabled"), Name: filepath.Base(str(m, "directory"))}}
		if p.info.Enabled {
			definition, err := loadGoPlugin(ctx, p.info.Path)
			if err != nil {
				p.info.Error = err.Error()
			} else {
				p.definition = definition
				p.info.Name = definition.Name
				p.info.Version = definition.Version
				p.info.Description = definition.Description
			}
		}
		loaded = append(loaded, p)
	}
	e.pluginMu.Lock()
	e.plugins = loaded
	e.pluginMu.Unlock()
	return nil
}
func (e *Engine) pluginList() []*loadedPlugin {
	e.pluginMu.RLock()
	defer e.pluginMu.RUnlock()
	return slices.Clone(e.plugins)
}
func (e *Engine) Plugins() []PluginInfo {
	out := []PluginInfo{}
	for _, p := range e.pluginList() {
		out = append(out, p.info)
	}
	return out
}
func (e *Engine) InstallPlugin(ctx context.Context, path string) (PluginInfo, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return PluginInfo{}, err
	}
	if filepath.Ext(path) != ".go" {
		return PluginInfo{}, errors.New("choose a Go source file")
	}
	definition, err := loadGoPlugin(ctx, path)
	if err != nil {
		return PluginInfo{}, err
	}
	m := Object{"model": "plugin", "directory": path, "enabled": true, "source": "filesystem"}
	existing, err := e.Store.Find(ctx, "plugin", "directory", path)
	if err != nil {
		return PluginInfo{}, err
	}
	if len(existing) > 0 {
		m["id"] = existing[0]["id"]
	}
	saved, err := e.Save(ctx, m)
	if err != nil {
		return PluginInfo{}, err
	}
	p := &loadedPlugin{info: PluginInfo{ID: str(saved, "id"), Path: path, Name: definition.Name, Version: definition.Version, Description: definition.Description, Enabled: true}, definition: definition}
	e.pluginMu.Lock()
	e.plugins = slices.DeleteFunc(e.plugins, func(v *loadedPlugin) bool { return v.info.Path == path })
	e.plugins = append(e.plugins, p)
	e.pluginMu.Unlock()
	return p.info, nil
}
func (e *Engine) PluginEnabled(ctx context.Context, id string, enabled bool) error {
	m, err := e.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	m["enabled"] = enabled
	if _, err = e.Save(ctx, m); err != nil {
		return err
	}
	return e.LoadPlugins(ctx)
}
func (e *Engine) RemovePlugin(ctx context.Context, id string) error {
	if err := e.Delete(ctx, id); err != nil {
		return err
	}
	return e.LoadPlugins(ctx)
}
func (e *Engine) pluginContext(ctx context.Context, name, workspace, request string) api.Context {
	return api.Context{Context: ctx, WorkspaceID: workspace, RequestID: request, GetValue: func(key string) (string, error) {
		var value string
		err := e.Store.db.QueryRowContext(ctx, "SELECT value FROM plugin_values WHERE plugin=? AND key=?", name, key).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return value, err
	}, SetValue: func(key, value string) error {
		_, err := e.Store.db.ExecContext(ctx, "INSERT INTO plugin_values(plugin,key,value) VALUES(?,?,?) ON CONFLICT(plugin,key) DO UPDATE SET value=excluded.value", name, key, value)
		return err
	}}
}
func pluginCall(p *loadedPlugin, fn func() error) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("plugin %s failed: %v", p.info.Name, failure)
		}
	}()
	return fn()
}
func (e *Engine) pluginTemplate(ctx context.Context, name string, args map[string]string, workspace string) (string, error) {
	for _, p := range e.pluginList() {
		if !p.info.Enabled || p.info.Error != "" {
			continue
		}
		for _, f := range p.definition.Templates {
			if f.Name == name {
				if f.Run == nil {
					return "", errors.New("template function has no implementation")
				}
				var result string
				err := pluginCall(p, func() error {
					var err error
					result, err = f.Run(e.pluginContext(ctx, p.info.Name, workspace, ""), args)
					return err
				})
				return result, err
			}
		}
	}
	return "", fmt.Errorf("unknown template function %q", name)
}
func (e *Engine) PluginAuthentication() []api.Authentication {
	result := []api.Authentication{}
	for _, p := range e.pluginList() {
		if p.info.Enabled && p.info.Error == "" {
			result = append(result, p.definition.Authentication...)
		}
	}
	return result
}
func (e *Engine) pluginAuth(ctx context.Context, name string, args Object, req *http.Request, workspace string) (bool, error) {
	for _, p := range e.pluginList() {
		if !p.info.Enabled || p.info.Error != "" {
			continue
		}
		for _, auth := range p.definition.Authentication {
			if auth.Name != name {
				continue
			}
			if auth.Apply == nil {
				return true, errors.New("authentication plugin has no implementation")
			}
			values := map[string]string{}
			for k, v := range args {
				values[k] = fmt.Sprint(v)
			}
			request, err := pluginRequest(req)
			if err != nil {
				return true, err
			}
			err = pluginCall(p, func() error { return auth.Apply(e.pluginContext(ctx, p.info.Name, workspace, ""), values, &request) })
			if err != nil {
				return true, err
			}
			return true, applyPluginRequest(req, request)
		}
	}
	return false, nil
}
func pluginRequest(req *http.Request) (api.Request, error) {
	body, err := requestBytes(req)
	return api.Request{Method: req.Method, URL: req.URL.String(), Headers: maps.Clone(req.Header), Body: body}, err
}
func applyPluginRequest(req *http.Request, r api.Request) error {
	u, err := url.Parse(r.URL)
	if err != nil {
		return err
	}
	req.Method, req.URL, req.Header = r.Method, u, http.Header(r.Headers)
	if req.Body != nil {
		_ = req.Body.Close()
	}
	req.Body = io.NopCloser(bytes.NewReader(r.Body))
	req.ContentLength = int64(len(r.Body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(r.Body)), nil }
	return nil
}
func (e *Engine) beforeSend(ctx context.Context, m Object, req *http.Request) error {
	for _, p := range e.pluginList() {
		if !p.info.Enabled || p.info.Error != "" || p.definition.BeforeSend == nil {
			continue
		}
		request, err := pluginRequest(req)
		if err != nil {
			return err
		}
		err = pluginCall(p, func() error {
			return p.definition.BeforeSend(e.pluginContext(ctx, p.info.Name, str(m, "workspaceId"), str(m, "id")), &request)
		})
		if err != nil {
			return err
		}
		if err = applyPluginRequest(req, request); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) afterReceive(ctx context.Context, m Object, response *http.Response, body []byte) error {
	for _, p := range e.pluginList() {
		if !p.info.Enabled || p.info.Error != "" || p.definition.AfterReceive == nil {
			continue
		}
		value := api.Response{Status: response.StatusCode, Headers: maps.Clone(response.Header), Body: body}
		if err := pluginCall(p, func() error {
			return p.definition.AfterReceive(e.pluginContext(ctx, p.info.Name, str(m, "workspaceId"), str(m, "id")), &value)
		}); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) PluginActions() []api.Action {
	result := []api.Action{}
	for _, p := range e.pluginList() {
		if p.info.Enabled && p.info.Error == "" {
			result = append(result, p.definition.Actions...)
		}
	}
	return result
}
func (e *Engine) RunPluginAction(ctx context.Context, name, workspace, request string) error {
	for _, p := range e.pluginList() {
		for _, action := range p.definition.Actions {
			if p.info.Enabled && p.info.Error == "" && action.Name == name && action.Run != nil {
				return pluginCall(p, func() error { return action.Run(e.pluginContext(ctx, p.info.Name, workspace, request)) })
			}
		}
	}
	return errors.New("plugin action is unavailable")
}

func (e *Engine) pluginImport(ctx context.Context, content string) ([]Object, error) {
	for _, p := range e.pluginList() {
		if !p.info.Enabled || p.info.Error != "" || p.definition.Import == nil {
			continue
		}
		var result []Object
		err := pluginCall(p, func() error {
			var err error
			result, err = p.definition.Import(e.pluginContext(ctx, p.info.Name, "", ""), content)
			return err
		})
		if err != nil {
			return nil, err
		}
		if len(result) > 0 {
			return result, nil
		}
	}
	return nil, errors.New("no importer recognized this collection")
}
func (e *Engine) PluginFilterNames() []string {
	names := []string{}
	for _, p := range e.pluginList() {
		if p.info.Enabled && p.info.Error == "" && p.definition.Filter != nil {
			names = append(names, p.info.Name)
		}
	}
	return names
}
func (e *Engine) Filter(ctx context.Context, content, expression, provider, workspace string) (string, error) {
	if provider == "" {
		return FilterResponse(content, expression)
	}
	for _, p := range e.pluginList() {
		if p.info.Name == provider && p.info.Enabled && p.info.Error == "" && p.definition.Filter != nil {
			var result string
			err := pluginCall(p, func() error {
				var err error
				result, err = p.definition.Filter(e.pluginContext(ctx, p.info.Name, workspace, ""), content, expression)
				return err
			})
			return result, err
		}
	}
	return "", errors.New("response filter is unavailable")
}
func (e *Engine) PluginThemes() []api.Theme {
	result := []api.Theme{}
	for _, p := range e.pluginList() {
		if p.info.Enabled && p.info.Error == "" {
			result = append(result, p.definition.Themes...)
		}
	}
	return result
}
