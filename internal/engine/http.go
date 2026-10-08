package engine

import (
	"cmp"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Azure/go-ntlmssp"
)

type SendOptions struct{ EnvironmentID, CookieJarID string }
type resolvedRequest struct {
	AuthOwnerID string
	Model       Object
	Settings    Object
	Workspace   Object
	Variables   map[string]string
}

func (e *Engine) resolve(ctx context.Context, request Object, environment string) (resolvedRequest, error) {
	ctx = context.WithValue(ctx, templateRequestKey{}, str(request, "id"))
	workspace, err := e.Store.Get(ctx, str(request, "workspaceId"))
	if err != nil {
		return resolvedRequest{}, err
	}
	chain := []Object{request}
	seen := map[string]bool{}
	for id := str(request, "folderId"); id != ""; {
		if seen[id] {
			return resolvedRequest{}, errors.New("folder inheritance contains a cycle")
		}
		seen[id] = true
		m, err := e.Store.Get(ctx, id)
		if err != nil {
			return resolvedRequest{}, err
		}
		chain = append(chain, m)
		id = str(m, "folderId")
	}
	chain = append(chain, workspace)
	settings := Object{}
	for _, key := range []string{"settingSendCookies", "settingStoreCookies", "settingFollowRedirects", "settingValidateCertificates", "settingRequestTimeout", "settingRequestMessageSize", "settingHttpVersion"} {
		settings[key] = workspace[key]
	}
	headers := e.DefaultHeaders()
	authType := workspace["authenticationType"]
	auth := workspace["authentication"]
	authOwnerID := str(workspace, "id")
	for i := len(chain) - 1; i >= 0; i-- {
		m := chain[i]
		if str(m, "model") != "workspace" {
			for k := range settings {
				if value := obj(m, k); boolean(value, "enabled") {
					settings[k] = value["value"]
				}
			}
		}
		if m["authenticationType"] != nil {
			authType = m["authenticationType"]
			auth = m["authentication"]
			authOwnerID = str(m, "id")
		}
		key := "headers"
		if str(m, "model") == "grpc_request" {
			key = "metadata"
		}
		levelHeaders := objects(array(m, key))
		overrides := map[string]bool{}
		for _, h := range levelHeaders {
			overrides[strings.ToLower(str(h, "name"))] = true
		}
		headers = slices.DeleteFunc(headers, func(value any) bool {
			h, _ := value.(map[string]any)
			return overrides[strings.ToLower(str(h, "name"))]
		})
		for _, h := range levelHeaders {
			headers = append(headers, h)
		}
	}
	vars, err := e.Variables(ctx, str(request, "workspaceId"), str(request, "folderId"), environment)
	if err != nil {
		return resolvedRequest{}, err
	}
	rendered := clone(request)
	rendered["authenticationType"] = authType
	rendered["authentication"] = auth
	rendered["headers"] = headers
	var render func(any) (any, error)
	render = func(v any) (any, error) {
		switch x := v.(type) {
		case string:
			return renderWith(x, vars, map[string]bool{}, 0, e.functions(ctx, str(request, "workspaceId"), environment))
		case map[string]any:
			out := Object{}
			for k, v := range x {
				r, err := render(v)
				if err != nil {
					return nil, err
				}
				out[k] = r
			}
			return out, nil
		case []any:
			out := []any{}
			for _, v := range x {
				if m, ok := v.(map[string]any); ok && !enabled(m) {
					out = append(out, clone(m))
					continue
				}
				r, err := render(v)
				if err != nil {
					return nil, err
				}
				out = append(out, r)
			}
			return out, nil
		default:
			return v, nil
		}
	}
	keys := []string{"url", "headers", "metadata", "urlParameters"}
	if str(rendered, "authenticationType") != "" && str(rendered, "authenticationType") != "none" {
		keys = append(keys, "authentication")
	}
	if str(rendered, "model") != "http_request" {
		keys = append(keys, "message")
	}
	body := obj(rendered, "body")
	switch str(rendered, "bodyType") {
	case "":
		delete(rendered, "body")
	case "graphql":
		rendered["body"] = Object{"query": body["query"], "variables": body["variables"], "operationName": body["operationName"]}
		keys = append(keys, "body")
	case "multipart/form-data", "application/x-www-form-urlencoded":
		rendered["body"] = Object{"form": body["form"]}
		keys = append(keys, "body")
	case "binary":
		rendered["body"] = Object{"filePath": body["filePath"]}
		keys = append(keys, "body")
	default:
		rendered["body"] = Object{"text": body["text"], "sendJsonComments": body["sendJsonComments"]}
		keys = append(keys, "body")
	}
	for _, key := range keys {
		if key == "authentication" && str(rendered, "authenticationType") == "oauth2" {
			value, err := e.RenderOAuth(ctx, obj(rendered, "authentication"), str(request, "workspaceId"), str(request, "folderId"), environment, str(request, "id"))
			if err != nil {
				return resolvedRequest{}, err
			}
			rendered[key] = value
			continue
		}
		value, err := render(rendered[key])
		if err != nil {
			return resolvedRequest{}, err
		}
		rendered[key] = value
	}

	return resolvedRequest{Model: rendered, Settings: settings, Workspace: workspace, Variables: vars, AuthOwnerID: authOwnerID}, nil
}
func buildURL(m Object) (*url.URL, error) {
	raw := strings.TrimSpace(str(m, "url"))
	if raw == "" {
		return nil, errors.New("enter a request URL")
	}
	if !strings.Contains(raw, "://") {
		if strings.HasPrefix(raw, "//") {
			raw = "http:" + raw
		} else {
			raw = "http://" + raw
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Hostname() == "" {
		return nil, errors.New("URL is missing a hostname")
	}
	query := u.Query()
	for _, p := range objects(array(m, "urlParameters")) {
		if !enabled(p) || str(p, "name") == "" {
			continue
		}
		name, value := str(p, "name"), str(p, "value")
		if strings.HasPrefix(name, ":") {
			escaped, changed := replacePathParameter(u.EscapedPath(), name, url.PathEscape(value))
			if !changed {
				continue
			}
			decoded, err := url.PathUnescape(escaped)
			if err != nil {
				return nil, err
			}
			u.Path, u.RawPath = decoded, escaped
		} else {
			query.Add(name, value)
		}
	}
	if str(m, "bodyType") == "graphql" && strings.EqualFold(str(m, "method"), "GET") {
		body := obj(m, "body")
		query.Set("query", str(body, "query"))
		for _, key := range []string{"variables", "operationName"} {
			query.Del(key)
			value := str(body, key)
			if key == "variables" {
				value = StripJSONComments(value)
			}
			if strings.TrimSpace(value) != "" {
				query.Set(key, value)
			}
		}
	}
	u.RawQuery = query.Encode()
	return u, nil
}
func replacePathParameter(path, name, value string) (string, bool) {
	parts := strings.Split(path, "/")
	changed := false
	for i, part := range parts {
		if part == name || strings.HasPrefix(part, name+":") {
			parts[i] = value + strings.TrimPrefix(part, name)
			changed = true
		}
	}
	return strings.Join(parts, "/"), changed
}
func (e *Engine) authenticate(req *http.Request, m Object, oauthOptions ...OAuthOptions) error {
	auth := obj(m, "authentication")
	switch str(m, "authenticationType") {
	case "", "none":
	case "basic":
		req.SetBasicAuth(str(auth, "username"), str(auth, "password"))
	case "bearer":
		prefix := "Bearer"
		if value, ok := auth["prefix"].(string); ok {
			prefix = value
		}
		req.Header.Set("Authorization", strings.TrimSpace(prefix+" "+str(auth, "token")))
	case "apikey":
		key := str(auth, "key")
		if key == "" {
			key = str(auth, "name")
		}
		if cmp.Or(str(auth, "location"), str(auth, "in")) == "query" {
			q := req.URL.Query()
			q.Set(key, str(auth, "value"))
			req.URL.RawQuery = q.Encode()
		} else {
			if strings.EqualFold(key, "Cookie") {
				req.Header.Add(key, str(auth, "value"))
			} else {
				req.Header.Set(key, str(auth, "value"))
			}
		}
	case "oauth2":
		token := str(auth, "accessToken")
		if token == "" {
			token = str(auth, "token")
		}
		if token == "" || str(auth, "tokenSource") == "automatic" {
			options := OAuthOptions{WorkspaceID: str(m, "workspaceId"), ContextID: str(m, "id")}
			if len(oauthOptions) > 0 {
				options = oauthOptions[0]
			}
			stored, err := e.AcquireOAuthToken(req.Context(), auth, options)
			if err != nil {
				return err
			}
			token = stored.Value()
		}
		prefix := "Bearer"
		if value, exists := auth["headerPrefix"]; exists {
			prefix, _ = value.(string)
		}
		req.Header.Set(cmp.Or(str(auth, "headerName"), "Authorization"), strings.TrimSpace(prefix+" "+token))
	default:
		if found, err := e.pluginAuth(req.Context(), str(m, "authenticationType"), auth, req, str(m, "workspaceId")); found {
			return err
		}
		return extendedAuth(req, str(m, "authenticationType"), auth)
	}
	return nil
}
func headerModels(h http.Header) []any {
	result := []any{}
	for name, values := range h {
		for _, value := range values {
			result = append(result, Object{"name": name, "value": value})
		}
	}
	return result
}
func (e *Engine) SendHTTP(ctx context.Context, id string, opts SendOptions) (response Object, sendErr error) {
	ctx, finish, err := e.begin(ctx, id)
	if err != nil {
		return nil, err
	}
	defer finish()
	ctx = context.WithValue(ctx, selectedCookieJarKey{}, opts.CookieJarID)
	request, err := e.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if str(request, "model") != "http_request" {
		return nil, errors.New("select an HTTP request")
	}
	response, err = e.Save(ctx, Object{"model": "http_response", "workspaceId": str(request, "workspaceId"), "requestId": id, "url": str(request, "url")})
	if err != nil {
		return nil, err
	}
	started := time.Now()
	responseID := str(response, "id")
	var eventsMu sync.Mutex
	event := func(data Object) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		_, _ = e.Save(context.WithoutCancel(e.ctx), Object{"model": "http_response_event", "workspaceId": str(request, "workspaceId"), "responseId": responseID, "event": data})
	}
	defer func() {
		response["state"] = "closed"
		response["elapsed"] = float64(time.Since(started).Microseconds()) / 1000
		if sendErr != nil {
			response["error"] = sendErr.Error()
		}
		saved, err := e.Save(context.WithoutCancel(e.ctx), response)
		if err == nil {
			response = saved
		} else {
			sendErr = errors.Join(sendErr, err)
		}
	}()
	resolved, err := e.resolve(ctx, request, opts.EnvironmentID)
	if err != nil {
		return response, err
	}
	if timeout := number(resolved.Settings, "settingRequestTimeout"); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
	}
	target, err := buildURL(resolved.Model)
	if err != nil {
		return response, err
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return response, errors.New("HTTP requests require an http:// or https:// URL")
	}
	if err = ValidateHTTPMethod(str(resolved.Model, "method")); err != nil {
		return response, err
	}
	prepared, err := e.prepareRequestBody(ctx, responseID, resolved.Model)
	if err != nil {
		return response, err
	}
	defer func() { _ = prepared.body.Close() }()
	req, err := http.NewRequestWithContext(ctx, str(resolved.Model, "method"), target.String(), prepared.body)
	if err != nil {
		return response, err
	}
	req.GetBody, req.ContentLength = prepared.reopen, prepared.size
	contentType := prepared.contentType
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	seenHeaders := map[string]bool{}
	for _, h := range objects(array(resolved.Model, "headers")) {
		name := str(h, "name")
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if !seenHeaders[key] {
			req.Header.Del(name)
			seenHeaders[key] = true
		}
		if !enabled(h) {
			if key == "user-agent" {
				req.Header.Set(name, "")
			}
			continue
		}
		if key == "host" {
			req.Host = str(h, "value")
		} else if key == "content-type" && str(resolved.Model, "bodyType") == "multipart/form-data" && openAPIMediaType(str(h, "value")) == "multipart/form-data" {
			req.Header.Set(name, contentType)
		} else {
			req.Header.Add(name, str(h, "value"))
		}
	}

	if err := e.beforeSend(ctx, resolved.Model, req); err != nil {
		return response, err
	}
	if err := e.authenticate(req, resolved.Model, resolved.oauthOptions(opts.EnvironmentID)); err != nil {
		return response, err
	}
	if req.Body != prepared.body {
		if err = e.snapshotModifiedRequest(ctx, responseID, req); err != nil {
			return response, err
		}
	}
	defer func() {
		if req.Body != nil {
			_ = req.Body.Close()
		}
	}()
	if cookies := req.Header.Values("Cookie"); len(cookies) > 1 {
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	response["requestHeaders"] = headerModels(req.Header)
	response["requestContentLength"] = req.ContentLength
	response["url"] = req.URL.String()
	transport, err := e.transport(ctx, resolved)
	if err != nil {
		return response, err
	}
	defer transport.CloseIdleConnections()
	var roundTripper http.RoundTripper = cookieTraceTransport{base: transport, observe: func(sent *http.Request, received *http.Response) {
		response["requestHeaders"] = headerModels(sent.Header)
		event(Object{"type": "info", "message": sent.Method + " " + sent.URL.String()})
		for _, h := range objects(headerModels(sent.Header)) {
			event(Object{"type": "header_up", "name": h["name"], "value": h["value"]})
		}
		var headers http.Header
		if received != nil {
			headers = received.Header
			for _, h := range objects(headerModels(headers)) {
				event(Object{"type": "header_down", "name": h["name"], "value": h["value"]})
			}
		}
		exchange := cookieHeaderDetails(sent.Header, headers, time.Now())
		exchange["url"] = sent.URL.String()
		response["cookieHistory"] = append(array(response, "cookieHistory"), exchange)
	}}

	switch str(resolved.Model, "authenticationType") {
	case "digest":
		roundTripper = digestTransport{base: roundTripper, auth: obj(resolved.Model, "authentication")}
	case "windows", "ntlm":
		roundTripper = ntlmssp.Negotiator{RoundTripper: roundTripper}
	}
	jarID, recordingJar, err := e.prepareCookieJar(ctx, str(request, "workspaceId"), opts.CookieJarID, boolean(resolved.Settings, "settingSendCookies"), boolean(resolved.Settings, "settingStoreCookies"))
	if err != nil {
		return response, err
	}
	client := &http.Client{Transport: roundTripper, Jar: recordingJar}
	defer func() {
		sendErr = errors.Join(sendErr, e.persistCookies(context.WithoutCancel(ctx), jarID, recordingJar))
	}()
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		event(Object{"type": "info", "message": "Redirect → " + next.URL.String()})
		if !boolean(resolved.Settings, "settingFollowRedirects") {
			return http.ErrUseLastResponse
		}
		if len(via) >= 20 {
			return errors.New("stopped after 20 redirects")
		}
		return nil
	}
	var dnsStarted time.Time
	trace := &httptrace.ClientTrace{DNSStart: func(info httptrace.DNSStartInfo) { dnsStarted = time.Now() }, DNSDone: func(info httptrace.DNSDoneInfo) {
		elapsed := time.Since(dnsStarted)
		response["elapsedDns"] = float64(elapsed.Microseconds()) / 1000
		addresses := []any{}
		for _, ip := range info.Addrs {
			addresses = append(addresses, ip.String())
		}
		event(Object{"type": "dns_resolved", "hostname": target.Hostname(), "addresses": addresses, "duration": elapsed.Milliseconds(), "overridden": false})
	}, GotConn: func(info httptrace.GotConnInfo) {
		response["remoteAddr"] = info.Conn.RemoteAddr().String()
		event(Object{"type": "info", "message": "Connected to " + info.Conn.RemoteAddr().String()})
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	res, err := client.Do(req)
	if err != nil {
		return response, err
	}
	defer func() { _ = res.Body.Close() }()
	maps.Copy(response, Object{"status": res.StatusCode, "statusReason": http.StatusText(res.StatusCode), "headers": headerModels(res.Header), "elapsedHeaders": float64(time.Since(started).Microseconds()) / 1000, "version": res.Proto, "state": "connected", "url": res.Request.URL.String()})
	if _, err = e.Save(ctx, response); err != nil {
		return response, err
	}
	if err = e.persistCookies(ctx, jarID, recordingJar); err != nil {
		return response, err
	}
	reader := io.Reader(res.Body)
	switch strings.ToLower(res.Header.Get("Content-Encoding")) {
	case "gzip":
		gz, err := gzip.NewReader(res.Body)
		if err != nil {
			return response, err
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	case "deflate":
		z, err := zlib.NewReader(res.Body)
		if err != nil {
			return response, err
		}
		defer func() { _ = z.Close() }()
		reader = z
	}
	file, err := e.bodies.OpenFile(responseID, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return response, err
	}
	defer func() { sendErr = errors.Join(sendErr, file.Close()) }()
	contentType, _, _ = mime.ParseMediaType(res.Header.Get("Content-Type"))
	var size int64
	buf := make([]byte, 32*1024)
	lastUpdate := time.Now()
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			written, writeErr := file.Write(buf[:n])
			size += int64(written)
			response["contentLength"] = size
			if writeErr != nil {
				return response, writeErr
			}
			if contentType == "text/event-stream" && time.Since(lastUpdate) > 100*time.Millisecond {
				response["contentLength"] = size
				response["elapsed"] = float64(time.Since(started).Microseconds()) / 1000
				if _, err = e.Save(ctx, response); err != nil {
					return response, err
				}
				lastUpdate = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return response, readErr
		}
	}
	if len(e.pluginList()) > 0 {
		data, err := e.Body(responseID)
		if err != nil {
			return response, err
		}
		if err = e.afterReceive(ctx, resolved.Model, res, data); err != nil {
			return response, err
		}
	}
	response["contentLength"] = size
	if res.ContentLength >= 0 {
		response["contentLengthCompressed"] = res.ContentLength
	}
	event(Object{"type": "chunk_received", "bytes": size})
	return response, nil
}
