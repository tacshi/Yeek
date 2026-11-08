package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	moeengine "github.com/MoeForge/moejs/engine"
	"github.com/MoeForge/moejs/eventloop"
	"github.com/MoeForge/moejs/webapi"
	api "yeek/plugin"

	"yeek/locales"
)

const pluginMemoryLimit = 256 << 20

var (
	pluginLoadTimeout = 10 * time.Second
	pluginCallTimeout = 60 * time.Second
)

var pluginExtensions = []string{".js", ".mjs", ".ts", ".mts"}

type jsPlugin struct {
	engine    *Engine
	name      string
	module    *moejs.Module
	runtime   *moejs.Runtime
	loop      *eventloop.Loop
	web       *webapi.API
	root      moejs.Value
	bytes     moejs.Value
	workspace string
	stale     bool
	closed    bool
}

type pluginManifest struct {
	Name           string                         `json:"name"`
	Version        string                         `json:"version"`
	Description    string                         `json:"description"`
	Templates      []pluginManifestTemplate       `json:"templates"`
	Authentication []pluginManifestAuthentication `json:"authentication"`
	Actions        []pluginManifestAction         `json:"actions"`
	Themes         []pluginManifestTheme          `json:"themes"`
}

type pluginManifestField struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Default string   `json:"default"`
	Secret  bool     `json:"secret"`
	Options []string `json:"options"`
}

type pluginManifestTemplate struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Fields      []pluginManifestField `json:"fields"`
}

type pluginManifestAuthentication struct {
	Name   string                `json:"name"`
	Label  string                `json:"label"`
	Fields []pluginManifestField `json:"fields"`
}

type pluginManifestAction struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type pluginManifestTheme struct {
	Name   string            `json:"name"`
	Dark   bool              `json:"dark"`
	Colors map[string]string `json:"colors"`
}

func (e *Engine) loadPlugin(ctx context.Context, path string) (definition api.Definition, plugin *jsPlugin, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = errors.New(locales.PluginInitializationFailedFailure(fmt.Sprint(failure)))
		}
	}()
	extension := filepath.Ext(path)
	if extension == ".go" {
		return definition, nil, errors.New(locales.GoPluginsAreNoLongerSupportedRewrite())
	}
	if !slices.Contains(pluginExtensions, extension) {
		return definition, nil, errors.New(locales.ChooseAJavaScriptOrTypeScriptFile())
	}
	source, err := os.ReadFile(path) // #nosec G304 -- plugin source is explicitly selected in the local plugin manager.
	if err != nil {
		return definition, nil, err
	}
	compile := moejs.Compile
	if extension == ".ts" || extension == ".mts" {
		compile = moejs.CompileTS
	}
	module, err := compile(filepath.Base(path), string(source))
	if err != nil {
		return definition, nil, err
	}
	if len(module.Requests()) > 0 {
		return definition, nil, errors.New(locales.PluginsAreASingleFileBundle())
	}
	plugin = &jsPlugin{engine: e, module: module, name: filepath.Base(path)}
	if err = plugin.start(ctx); err != nil {
		return definition, nil, err
	}
	definition, err = plugin.definition()
	if err != nil {
		plugin.close()
		return definition, nil, err
	}
	plugin.name = definition.Name
	return definition, plugin, nil
}

func (p *jsPlugin) start(ctx context.Context) error {
	runtime := moejs.NewRuntime(moejs.Options{MemoryLimit: pluginMemoryLimit})
	loop, err := eventloop.New(runtime, eventloop.Options{})
	if err != nil {
		return err
	}
	web, err := webapi.Install(loop, webapi.Options{Client: &http.Client{Transport: pluginNetwork{p}}})
	if err != nil {
		return err
	}
	if err = runtime.SetGlobal("console", p.console(runtime)); err != nil {
		web.Close()
		return err
	}
	script, err := moejs.CompileScript("yeek:bytes", "(buffer) => new Uint8Array(buffer)")
	if err != nil {
		web.Close()
		return err
	}
	bytes, err := runtime.RunScript(script)
	if err != nil {
		web.Close()
		return err
	}
	p.runtime, p.loop, p.web, p.bytes, p.stale = runtime, loop, web, bytes, false
	err = p.run(ctx, pluginLoadTimeout, func(runtime *moejs.Runtime) error {
		if err := runtime.Load(p.module); !errors.Is(err, moejs.ErrModulePending) {
			return err
		}
		return nil
	})
	if err != nil {
		p.close()
		return err
	}
	root, ok := runtime.Export("default")
	if !ok || !root.IsObject() {
		p.close()
		return errors.New(locales.ExportThePluginAsTheDefault())
	}
	p.root = root
	return nil
}

func (p *jsPlugin) close() {
	if p.web != nil {
		p.web.Close()
		p.loop.Terminate()
		p.runtime.ClearInterrupt()
	}
	p.runtime, p.loop, p.web = nil, nil, nil
}

func (p *jsPlugin) console(runtime *moejs.Runtime) map[string]any {
	write := moejs.NativeFunc(func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, pluginDisplay(runtime, arg))
		}
		log.Printf("plugin %s: %s", p.name, strings.Join(parts, " "))
		return moejs.Undefined(), nil
	})
	return map[string]any{"debug": write, "error": write, "info": write, "log": write, "warn": write}
}

type pluginLimitError struct {
	message string
	cause   error
}

func (e pluginLimitError) Error() string { return e.message }
func (e pluginLimitError) Unwrap() error { return e.cause }

func (p *jsPlugin) run(parent context.Context, timeout time.Duration, work func(*moejs.Runtime) error) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	loop, done := p.loop, make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		loop.Interrupt(context.Cause(ctx))
	})
	p.web.SetContext(ctx)
	err := loop.Run(work)
	if !stop() {
		<-done
		p.stale = true
	}
	var interrupted *moejs.InterruptedError
	if errors.As(err, &interrupted) {
		p.stale = true
	}
	switch {
	case err == nil:
	case errors.Is(err, moejs.ErrMemoryLimit):
		return pluginLimitError{locales.PluginNameUsedMoreThanMegabytes(p.name, strconv.Itoa(pluginMemoryLimit>>20)), err}
	case parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return pluginLimitError{locales.PluginNameStoppedAfterSeconds(p.name, strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64)), context.DeadlineExceeded}
	}
	return err
}

func (p *jsPlugin) member(path []string) (function, this moejs.Value, err error) {
	function = p.root
	for _, key := range path {
		this = function
		if function, err = p.runtime.Get(this, key); err != nil {
			return function, this, err
		}
	}
	return function, this, nil
}

func (p *jsPlugin) callable(path ...string) bool {
	function, _, err := p.member(path)
	return err == nil && moeengine.IsCallable(function)
}

func (p *jsPlugin) shutdown() {
	p.closed = true
	p.close()
}

func (p *jsPlugin) call(c api.Context, path []string, arguments func(*moejs.Runtime) ([]moejs.Value, error), read func(*moejs.Runtime, moejs.Value) error) error {
	if p.closed {
		return errors.New(locales.PluginNameWasReloadedTryAgain(p.name))
	}
	if p.stale || p.runtime == nil {
		p.close()
		if err := p.start(c.Context); err != nil {
			return err
		}
	}
	p.workspace = c.WorkspaceID
	defer func() {
		if p.runtime != nil {
			p.runtime.ReleaseCallData()
		}
	}()
	function, this, err := p.member(path)
	if err != nil {
		return err
	}
	context, err := p.context(c)
	if err != nil {
		return err
	}
	args, err := arguments(p.runtime)
	if err != nil {
		return err
	}
	var result moejs.Value
	err = p.run(c.Context, pluginCallTimeout, func(runtime *moejs.Runtime) error {
		var err error
		result, err = runtime.CallFunction(function, this, append([]moejs.Value{context}, args...)...)
		return err
	})
	if err != nil {
		return err
	}
	if state, value, ok := moejs.PromiseResult(result); ok {
		switch state {
		case moejs.PromisePending:
			return errors.New(locales.PluginNeverFinished())
		case moejs.PromiseRejected:
			return errors.New(pluginDisplay(p.runtime, pluginRejection(p.runtime, value)))
		}
		result = value
	}
	if read == nil {
		return nil
	}
	return read(p.runtime, result)
}

func pluginRejection(runtime *moejs.Runtime, reason moejs.Value) moejs.Value {
	if reason.IsObject() {
		if message, err := runtime.Get(reason, "message"); err == nil && message.IsString() {
			return message
		}
	}
	return reason
}

func pluginDisplay(runtime *moejs.Runtime, value moejs.Value) string {
	if value.IsObject() {
		if text, err := runtime.AppendJSON(nil, value); err == nil {
			return string(text)
		}
	}
	return pluginText(runtime, value)
}

func pluginText(runtime *moejs.Runtime, value moejs.Value) string {
	if value.IsNullish() {
		return ""
	}
	out, err := runtime.ToGo(value)
	if err != nil {
		return value.String()
	}
	if text, ok := out.(string); ok {
		return text
	}
	return fmt.Sprint(out)
}

func (p *jsPlugin) context(c api.Context) (moejs.Value, error) {
	runtime := p.runtime
	return runtime.FromGo(map[string]any{
		"workspaceId": c.WorkspaceID,
		"requestId":   c.RequestID,
		"getValue": moejs.NativeFunc(func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			value, err := c.GetValue(pluginText(runtime, moejs.Arg(args, 0)))
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.String(value), nil
		}),
		"setValue": moejs.NativeFunc(func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			return moejs.Undefined(), c.SetValue(pluginText(runtime, moejs.Arg(args, 0)), pluginText(runtime, moejs.Arg(args, 1)))
		}),
	})
}

func (p *jsPlugin) uint8Array(runtime *moejs.Runtime, body []byte) (moejs.Value, error) {
	buffer, err := runtime.FromGo(append([]byte{}, body...))
	if err != nil {
		return moejs.Undefined(), err
	}
	return runtime.CallFunction(p.bytes, moejs.Undefined(), buffer)
}

func (p *jsPlugin) requestValue(runtime *moejs.Runtime, request *api.Request) (moejs.Value, error) {
	body, err := p.uint8Array(runtime, request.Body)
	if err != nil {
		return moejs.Undefined(), err
	}
	return runtime.FromGo(map[string]any{"method": request.Method, "url": request.URL, "headers": request.Headers, "body": body})
}

func readPluginRequest(runtime *moejs.Runtime, value moejs.Value, request *api.Request) error {
	field := func(name string) (any, error) {
		v, err := runtime.Get(value, name)
		if err != nil {
			return nil, err
		}
		return runtime.ToGo(v)
	}
	method, err := field("method")
	if err != nil {
		return err
	}
	address, err := field("url")
	if err != nil {
		return err
	}
	headers, err := field("headers")
	if err != nil {
		return err
	}
	body, err := field("body")
	if err != nil {
		return err
	}
	request.Method, request.URL = fmt.Sprint(method), fmt.Sprint(address)
	request.Headers = pluginHeaders(headers)
	switch b := body.(type) {
	case []byte:
		request.Body = b
	case string:
		request.Body = []byte(b)
	case nil:
		request.Body = nil
	default:
		return errors.New(locales.PluginBodyMustBeTextOrBytes())
	}
	return nil
}

func pluginHeaders(value any) map[string][]string {
	switch headers := value.(type) {
	case map[string][]string:
		return headers
	case map[string]any:
		out := make(map[string][]string, len(headers))
		for name, entry := range headers {
			switch v := entry.(type) {
			case []string:
				out[name] = v
			case []any:
				for _, item := range v {
					out[name] = append(out[name], fmt.Sprint(item))
				}
			default:
				out[name] = []string{fmt.Sprint(v)}
			}
		}
		return out
	}
	return map[string][]string{}
}

func (p *jsPlugin) definition() (api.Definition, error) {
	data, err := p.runtime.AppendJSON(nil, p.root)
	if err != nil {
		return api.Definition{}, err
	}
	var manifest pluginManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return api.Definition{}, err
	}
	if manifest.Name == "" {
		return api.Definition{}, errors.New(locales.PluginNeedsAName())
	}
	definition := api.Definition{Name: manifest.Name, Version: manifest.Version, Description: manifest.Description}
	for i, template := range manifest.Templates {
		item := api.Template{Name: template.Name, Description: template.Description, Fields: pluginFields(template.Fields)}
		if path := []string{"templates", strconv.Itoa(i), "run"}; p.callable(path...) {
			item.Run = func(c api.Context, args map[string]string) (string, error) { return p.text(c, path, args) }
		}
		definition.Templates = append(definition.Templates, item)
	}
	for i, authentication := range manifest.Authentication {
		item := api.Authentication{Name: authentication.Name, Label: authentication.Label, Fields: pluginFields(authentication.Fields)}
		if path := []string{"authentication", strconv.Itoa(i), "apply"}; p.callable(path...) {
			item.Apply = func(c api.Context, values map[string]string, request *api.Request) error {
				return p.changeRequest(c, path, request, values)
			}
		}
		definition.Authentication = append(definition.Authentication, item)
	}
	for i, action := range manifest.Actions {
		item := api.Action{Name: action.Name, Label: action.Label}
		if path := []string{"actions", strconv.Itoa(i), "run"}; p.callable(path...) {
			item.Run = func(c api.Context) error {
				return p.call(c, path, func(*moejs.Runtime) ([]moejs.Value, error) { return nil, nil }, nil)
			}
		}
		definition.Actions = append(definition.Actions, item)
	}
	for _, theme := range manifest.Themes {
		definition.Themes = append(definition.Themes, api.Theme{Name: theme.Name, Dark: theme.Dark, Colors: theme.Colors})
	}
	if p.callable("import") {
		definition.Import = p.importCollection
	}
	if p.callable("filter") {
		definition.Filter = func(c api.Context, content, expression string) (string, error) {
			return p.text(c, []string{"filter"}, content, expression)
		}
	}
	if p.callable("beforeSend") {
		definition.BeforeSend = func(c api.Context, request *api.Request) error {
			return p.changeRequest(c, []string{"beforeSend"}, request)
		}
	}
	if p.callable("afterReceive") {
		definition.AfterReceive = p.afterReceive
	}
	return definition, nil
}

func pluginFields(fields []pluginManifestField) []api.Field {
	out := make([]api.Field, 0, len(fields))
	for _, field := range fields {
		out = append(out, api.Field{Name: field.Name, Label: field.Label, Default: field.Default, Secret: field.Secret, Options: field.Options})
	}
	return out
}

func (p *jsPlugin) text(c api.Context, path []string, inputs ...any) (string, error) {
	var text string
	err := p.call(c, path, func(runtime *moejs.Runtime) ([]moejs.Value, error) {
		return pluginValues(runtime, inputs)
	}, func(runtime *moejs.Runtime, result moejs.Value) error {
		text = pluginText(runtime, result)
		return nil
	})
	return text, err
}

func pluginValues(runtime *moejs.Runtime, inputs []any) ([]moejs.Value, error) {
	values := make([]moejs.Value, 0, len(inputs))
	for _, input := range inputs {
		value, err := runtime.FromGo(input)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (p *jsPlugin) changeRequest(c api.Context, path []string, request *api.Request, inputs ...any) error {
	var value moejs.Value
	return p.call(c, path, func(runtime *moejs.Runtime) ([]moejs.Value, error) {
		values, err := pluginValues(runtime, inputs)
		if err != nil {
			return nil, err
		}
		if value, err = p.requestValue(runtime, request); err != nil {
			return nil, err
		}
		return append(values, value), nil
	}, func(runtime *moejs.Runtime, _ moejs.Value) error {
		return readPluginRequest(runtime, value, request)
	})
}

func (p *jsPlugin) afterReceive(c api.Context, response *api.Response) error {
	return p.call(c, []string{"afterReceive"}, func(runtime *moejs.Runtime) ([]moejs.Value, error) {
		body, err := p.uint8Array(runtime, response.Body)
		if err != nil {
			return nil, err
		}
		value, err := runtime.FromGo(map[string]any{"status": response.Status, "headers": response.Headers, "body": body})
		if err != nil {
			return nil, err
		}
		return []moejs.Value{value}, nil
	}, nil)
}

func (p *jsPlugin) importCollection(c api.Context, content string) ([]map[string]any, error) {
	var models []map[string]any
	err := p.call(c, []string{"import"}, func(runtime *moejs.Runtime) ([]moejs.Value, error) {
		return pluginValues(runtime, []any{content})
	}, func(runtime *moejs.Runtime, result moejs.Value) error {
		if result.IsNullish() {
			return nil
		}
		data, err := runtime.AppendJSON(nil, result)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &models)
	})
	return models, err
}

type pluginNetwork struct{ plugin *jsPlugin }

type pluginResponseBody struct {
	io.ReadCloser
	transport *networkTransport
}

func (b pluginResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.transport.CloseIdleConnections()
	return err
}

func (n pluginNetwork) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, workspace := request.Context(), n.plugin.workspace
	w := Object{"settingValidateCertificates": true, "settingHttpVersion": "auto"}
	if workspace != "" {
		var err error
		if w, err = n.plugin.engine.Store.Get(ctx, workspace); err != nil {
			return nil, err
		}
	}
	target := url.URL{Scheme: request.URL.Scheme, Host: request.URL.Host, Path: request.URL.Path}
	transport, err := n.plugin.engine.transport(ctx, resolvedRequest{Model: Object{"url": target.String()}, Workspace: w, Settings: w})
	if err != nil {
		return nil, err
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	response.Body = pluginResponseBody{response.Body, transport}
	return response, nil
}
