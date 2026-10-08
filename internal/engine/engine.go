package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Engine struct {
	oauthMu          sync.Mutex
	prompts          promptCache
	oauthFlights     map[string]*oauthFlight
	oauthGenerations map[string]uint64
	oauthCallbacks   map[string]func(string) bool
	workers          sync.WaitGroup
	closing          bool
	pluginMu         sync.RWMutex
	plugins          []*loadedPlugin
	bodies           *os.Root
	keyMu            sync.Mutex
	masterKey        []byte
	secrets          secretStore
	grpcSessions     map[string]*grpcSession
	websockets       map[string]*websocketSession
	Store            *Store
	DataDir          string
	mu               sync.Mutex
	listeners        map[chan []Object]struct{}
	pending          map[string]context.CancelFunc
	ctx              context.Context
	cancel           context.CancelFunc
}

func Open(dir string) (*Engine, error) {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{DataDir: dir, listeners: map[chan []Object]struct{}{}, pending: map[string]context.CancelFunc{}, ctx: ctx, cancel: cancel}
	for _, sub := range []string{"bodies", "logs", "plugins"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			cancel()
			return nil, err
		}
	}
	var err error
	e.bodies, err = os.OpenRoot(filepath.Join(dir, "bodies"))
	if err != nil {
		cancel()
		return nil, err
	}
	e.Store, err = OpenStore(dir, e.publish)
	if err != nil {
		cancel()
		return nil, err
	}
	if err = e.LoadPlugins(ctx); err != nil {
		_ = e.Close()
		return nil, err
	}
	return e, nil
}
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		return nil
	}
	e.closing = true
	e.cancel()
	for _, cancel := range e.pending {
		cancel()
	}
	e.mu.Unlock()
	e.workers.Wait()
	return errors.Join(e.Store.Close(), e.bodies.Close())
}

func (e *Engine) Subscribe() (<-chan []Object, func()) {
	ch := make(chan []Object, 128)
	e.mu.Lock()
	e.listeners[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() { e.mu.Lock(); delete(e.listeners, ch); e.mu.Unlock() }
}
func (e *Engine) publish(changes []Object) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.listeners {
		select {
		case ch <- changes:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- nil
		}
	}
}
func (e *Engine) Save(ctx context.Context, m Object) (Object, error) {
	var saved Object
	err := e.Store.Write(ctx, Object{"type": "background"}, func(t *modelTx) error {
		if str(m, "id") != "" && str(m, "createdAt") != "" {
			if _, err := t.get(ctx, str(m, "id")); err != nil {
				return errors.New("this item was deleted; reload the workspace")
			}
		}
		var err error
		saved, err = t.upsert(ctx, m)
		return err
	})
	if err == nil && str(saved, "model") == "workspace" {
		err = e.Store.EnsureWorkspace(ctx, str(saved, "id"))
	}
	return saved, err
}
func (e *Engine) Create(ctx context.Context, kind, workspace string) (Object, error) {
	m := Object{"model": kind}
	if workspace != "" {
		m["workspaceId"] = workspace
	}
	return e.Save(ctx, m)
}
func (e *Engine) Delete(ctx context.Context, id string) error {
	return e.Store.Delete(ctx, id, Object{"type": "background"})
}
func (e *Engine) Duplicate(ctx context.Context, id string) (string, error) {
	return e.Store.Duplicate(ctx, id, Object{"type": "background"})
}
func (e *Engine) Cancel(id string) {
	e.mu.Lock()
	cancel := e.pending[id]
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (e *Engine) begin(ctx context.Context, id string) (context.Context, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing {
		return nil, nil, errors.New("application is closing")
	}
	if _, ok := e.pending[id]; ok {
		return nil, nil, errors.New("request is already running")
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	e.pending[id] = cancel
	e.workers.Add(1)
	return ctx, func() {
		stop()
		cancel()
		e.mu.Lock()
		delete(e.pending, id)
		e.mu.Unlock()
		e.workers.Done()
	}, nil
}
func (e *Engine) Body(id string) ([]byte, error) {
	if !validBodyID(id) {
		return nil, errors.New("invalid response ID")
	}
	return e.bodies.ReadFile(id)
}
func validBodyID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".."
}
func (e *Engine) Snapshot(ctx context.Context, workspace string) ([]Object, error) {
	return e.Store.WorkspaceModels(ctx, workspace)
}
func (e *Engine) DefaultHeaders() []any {
	return []any{Object{"name": "User-Agent", "value": "Yeek", "enabled": true}, Object{"name": "Accept", "value": "*/*", "enabled": true}}
}

func (e *Engine) BodyPreview(id string, limit int64) ([]byte, bool, error) {
	if !validBodyID(id) {
		return nil, false, errors.New("invalid response ID")
	}
	file, err := e.bodies.Open(id)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return data, truncated, err
}
func (e *Engine) SaveBody(id, path string) error {
	if !validBodyID(id) {
		return errors.New("invalid response ID")
	}
	source, err := e.bodies.Open(id)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 -- the native Save dialog supplies this destination.
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	return errors.Join(copyErr, target.Close())
}
