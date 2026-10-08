package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type websocketSession struct {
	conn   *websocket.Conn
	model  Object
	mu     sync.Mutex
	ctx    context.Context
	finish func()
}

func (e *Engine) ConnectWebSocket(ctx context.Context, id string, opts SendOptions) (result Object, resultErr error) {
	ctx = context.WithValue(ctx, selectedCookieJarKey{}, opts.CookieJarID)
	ctx, finish, err := e.begin(ctx, id)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			finish()
		}
	}()
	request, err := e.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	resolved, err := e.resolve(ctx, request, opts.EnvironmentID)
	if err != nil {
		return nil, err
	}
	target, err := buildURL(resolved.Model)
	if err != nil {
		return nil, err
	}
	switch target.Scheme {
	case "http":
		target.Scheme = "ws"
	case "https":
		target.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, errors.New("WebSocket URL must begin with ws:// or wss://")
	}
	model, err := e.Save(ctx, Object{"model": "websocket_connection", "workspaceId": str(request, "workspaceId"), "requestId": id, "url": target.String()})
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			closed := clone(model)
			closed["state"] = "closed"
			if resultErr != nil {
				closed["error"] = resultErr.Error()
			}
			_, _ = e.Save(context.WithoutCancel(e.ctx), closed)
		}
	}()
	transport, err := e.transport(ctx, resolved)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	for _, h := range objects(array(resolved.Model, "headers")) {
		if enabled(h) && str(h, "name") != "" {
			headers.Add(str(h, "name"), str(h, "value"))
		}
	}
	authReq := (&http.Request{URL: target, Header: headers}).WithContext(ctx)
	if err = e.authenticate(authReq, resolved.Model, resolved.oauthOptions(opts.EnvironmentID)); err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	jarID, recording, err := e.prepareCookieJar(ctx, str(request, "workspaceId"), opts.CookieJarID, boolean(resolved.Settings, "settingSendCookies"), boolean(resolved.Settings, "settingStoreCookies"))
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport, Jar: recording}
	defer func() {
		resultErr = errors.Join(resultErr, e.persistCookies(context.WithoutCancel(ctx), jarID, recording))
	}()
	if !boolean(resolved.Settings, "settingFollowRedirects") {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	conn, res, err := websocket.Dial(dialCtx, target.String(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: headers, CompressionMode: websocket.CompressionContextTakeover})
	if saveErr := e.persistCookies(ctx, jarID, recording); saveErr != nil {
		if conn != nil {
			_ = conn.CloseNow()
		}
		return nil, saveErr
	}
	if err != nil {
		model["state"] = "closed"
		model["error"] = err.Error()
		if res != nil {
			model["status"] = res.StatusCode
			model["headers"] = headerModels(res.Header)
		}
		_, saveErr := e.Save(context.WithoutCancel(e.ctx), model)
		transport.CloseIdleConnections()
		return model, errors.Join(err, saveErr)
	}
	maxSize := int64(number(resolved.Settings, "settingRequestMessageSize"))
	if maxSize <= 0 {
		maxSize = 64 * 1024 * 1024
	}
	conn.SetReadLimit(maxSize)
	model["state"] = "connected"
	model["status"] = res.StatusCode
	model["headers"] = headerModels(res.Header)
	model, err = e.Save(ctx, model)
	if err != nil {
		_ = conn.CloseNow()
		return nil, err
	}
	session := &websocketSession{conn: conn, model: model, ctx: ctx, finish: finish}
	e.mu.Lock()
	if e.websockets == nil {
		e.websockets = map[string]*websocketSession{}
	}
	e.websockets[str(model, "id")] = session
	e.mu.Unlock()
	success = true
	started := time.Now()
	e.websocketEvent(ctx, model, "open", true, []byte("Connected"))
	go func() {
		defer finish()
		defer transport.CloseIdleConnections()
		defer func() { _ = conn.CloseNow() }()
		stop := context.AfterFunc(ctx, func() { _ = conn.CloseNow() })
		defer stop()
		var readErr error
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				readErr = err
				break
			}
			kind := "text"
			if typ == websocket.MessageBinary {
				kind = "binary"
			}
			e.websocketEvent(ctx, model, kind, true, data)
		}
		session.mu.Lock()
		closed := clone(session.model)
		session.mu.Unlock()
		closed["state"] = "closed"
		closed["elapsed"] = float64(time.Since(started).Milliseconds())
		code := websocket.CloseStatus(readErr)
		if readErr != nil && !errors.Is(readErr, context.Canceled) && code != websocket.StatusNormalClosure && code != websocket.StatusGoingAway {
			closed["error"] = readErr.Error()
			e.websocketEvent(e.ctx, model, "error", true, []byte(readErr.Error()))
		}
		e.websocketEvent(e.ctx, model, "close", true, []byte(fmt.Sprintf("Connection closed (%d)", code)))
		_, _ = e.Save(context.WithoutCancel(e.ctx), closed)
		e.mu.Lock()
		delete(e.websockets, str(model, "id"))
		e.mu.Unlock()
	}()
	return model, nil
}
func (e *Engine) websocketEvent(ctx context.Context, m Object, kind string, server bool, data []byte) {
	values := make([]any, len(data))
	for i, b := range data {
		values[i] = int(b)
	}
	_, _ = e.Save(ctx, Object{"model": "websocket_event", "workspaceId": m["workspaceId"], "requestId": m["requestId"], "connectionId": m["id"], "isServer": server, "message": values, "messageType": kind})
}
func (e *Engine) SendWebSocket(ctx context.Context, connectionID, message string, binary bool) error {
	e.mu.Lock()
	session := e.websockets[connectionID]
	e.mu.Unlock()
	if session == nil {
		return errors.New("connect the WebSocket first")
	}
	data := []byte(message)
	typ := websocket.MessageText
	if binary {
		var err error
		data, err = base64.StdEncoding.DecodeString(strings.TrimSpace(message))
		if err != nil {
			return fmt.Errorf("binary message must be base64: %w", err)
		}
		typ = websocket.MessageBinary
	}
	if err := session.conn.Write(ctx, typ, data); err != nil {
		return err
	}
	kind := "text"
	if binary {
		kind = "binary"
	}
	e.websocketEvent(ctx, session.model, kind, false, data)
	return nil
}
func (e *Engine) CloseWebSocket(id string) error {
	e.mu.Lock()
	session := e.websockets[id]
	e.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.conn.Close(websocket.StatusNormalClosure, "Disconnected by user")
}
func (e *Engine) PingWebSocket(ctx context.Context, id string) error {
	e.mu.Lock()
	session := e.websockets[id]
	e.mu.Unlock()
	if session == nil {
		return errors.New("WebSocket is not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := session.conn.Ping(ctx); err != nil {
		return err
	}
	e.websocketEvent(ctx, session.model, "pong", true, []byte("Pong"))
	return nil
}
