package engine

import (
	"cmp"
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
	"strconv"
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
	// SettingSources names the model each setting came from, for the timeline.
	SettingSources map[string]Object
	Workspace      Object
	Variables      map[string]string
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
	sources := map[string]Object{}
	for _, key := range []string{"settingSendCookies", "settingStoreCookies", "settingFollowRedirects", "settingValidateCertificates", "settingRequestTimeout", "settingRequestMessageSize", "settingHttpVersion"} {
		settings[key] = workspace[key]
		sources[key] = settingSource(workspace)
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
					sources[k] = settingSource(m)
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
		// Like Yaak's render_http_request: authentication.disabled is true,
		// false, or a template ("Enabled when...") that disables auth when it
		// renders empty. Disabled auth keeps only {disabled: true}.
		authValues := obj(rendered, "authentication")
		disabled := false
		switch condition := authValues["disabled"].(type) {
		case bool:
			disabled = condition
		case string:
			value, _ := renderWith(condition, vars, map[string]bool{}, 0, e.functions(ctx, str(request, "workspaceId"), environment))
			disabled = value == ""
		}
		if disabled {
			rendered["authentication"] = Object{"disabled": true}
		} else {
			if _, ok := authValues["disabled"]; ok {
				authValues = maps.Clone(authValues)
				authValues["disabled"] = false
				rendered["authentication"] = authValues
			}
			keys = append(keys, "authentication")
		}
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

	return resolvedRequest{Model: rendered, Settings: settings, SettingSources: sources, Workspace: workspace, Variables: vars, AuthOwnerID: authOwnerID}, nil
}

// ensureProto is Yaak's default scheme for a URL typed without one: HTTPS
// for the .app, .dev and .page TLDs (which are HSTS-preloaded), else HTTP.
func ensureProto(raw string) string {
	raw = strings.TrimPrefix(raw, "//")
	if u, err := url.Parse("http://" + raw); err == nil {
		if host := u.Hostname(); strings.HasSuffix(host, ".app") || strings.HasSuffix(host, ".dev") || strings.HasSuffix(host, ".page") {
			return "https://" + raw
		}
	}
	return "http://" + raw
}

func buildURL(m Object) (*url.URL, error) {
	raw := strings.TrimSpace(str(m, "url"))
	if raw == "" {
		return nil, errors.New("enter a request URL")
	}
	if !strings.Contains(raw, "://") {
		raw = ensureProto(raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Hostname() == "" {
		return nil, errors.New("URL is missing a hostname")
	}
	// Like Yaak, the URL's own query stays as typed and enabled parameters
	// are appended after it in table order.
	var params [][2]string
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
			params = append(params, [2]string{name, value})
		}
	}
	u.RawQuery = appendQuery(escapeRawQuery(u.RawQuery), params)
	if str(m, "bodyType") == "graphql" && strings.EqualFold(str(m, "method"), "GET") {
		body := obj(m, "body")
		params = [][2]string{{"query", str(body, "query")}}
		if variables := StripJSONComments(str(body, "variables")); strings.TrimSpace(variables) != "" {
			params = append(params, [2]string{"variables", variables})
		}
		if operation := str(body, "operationName"); strings.TrimSpace(operation) != "" {
			params = append(params, [2]string{"operationName", operation})
		}
		u.RawQuery = appendQuery(stripQuery(u.RawQuery, "query", "variables", "operationName"), params)
	}
	u.ForceQuery = false
	return u, nil
}

// queryComponent percent-encodes everything but unreserved characters, so
// spaces become %20 as in Yaak rather than +.
func queryComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func appendQuery(raw string, params [][2]string) string {
	parts := make([]string, 0, len(params)+1)
	if strings.TrimSpace(raw) != "" {
		parts = append(parts, raw)
	}
	for _, p := range params {
		parts = append(parts, queryComponent(p[0])+"="+queryComponent(p[1]))
	}
	return strings.Join(parts, "&")
}

func stripQuery(raw string, names ...string) string {
	if raw == "" {
		return ""
	}
	pairs := strings.Split(raw, "&")
	return strings.Join(slices.DeleteFunc(pairs, func(pair string) bool {
		key, _, _ := strings.Cut(pair, "=")
		decoded, err := url.QueryUnescape(key)
		return err == nil && slices.Contains(names, decoded)
	}), "&")
}

// escapeRawQuery percent-encodes bytes that can't appear in a request line
// (the url crate's query set: controls, space, quotes, angle brackets,
// non-ASCII) while leaving existing escapes and the user's ordering alone.
func escapeRawQuery(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c <= ' ' || c >= 0x7f || c == '"' || c == '<' || c == '>' {
			b.WriteString("%" + strings.ToUpper(strconv.FormatUint(uint64(c)|0x100, 16)[1:]))
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
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

// authApplies reports whether a resolved model's authentication should be
// applied: it has a type and wasn't disabled.
func authApplies(m Object) bool {
	kind := str(m, "authenticationType")
	return kind != "" && kind != "none" && !boolean(obj(m, "authentication"), "disabled")
}

func (e *Engine) authenticate(req *http.Request, m Object, oauthOptions ...OAuthOptions) error {
	if !authApplies(m) {
		return nil
	}
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
			// Appended after the URL's own parameters, like Yaak's setQueryParameters.
			req.URL.RawQuery = appendQuery(req.URL.RawQuery, [][2]string{{key, str(auth, "value")}})
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
	for _, setting := range resolved.timelineSettings() {
		event(setting)
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
		event(sendURLEvent(sent))
		for _, h := range objects(headerModels(sent.Header)) {
			event(Object{"type": "header_up", "name": h["name"], "value": h["value"]})
		}
		if sent.ContentLength > 0 {
			event(Object{"type": "chunk_sent", "bytes": sent.ContentLength})
		}
		var headers http.Header
		if received != nil {
			event(Object{"type": "receive_url", "version": received.Proto, "status": received.Status})
			headers = received.Header
			for _, h := range objects(headerModels(headers)) {
				event(Object{"type": "header_down", "name": h["name"], "value": h["value"]})
			}
		}
		exchange := cookieHeaderDetails(sent.Header, headers, time.Now())
		exchange["url"] = sent.URL.String()
		response["cookieHistory"] = append(array(response, "cookieHistory"), exchange)
	}}

	switch kind := str(resolved.Model, "authenticationType"); {
	case !authApplies(resolved.Model):
	case kind == "digest":
		bodyType := str(resolved.Model, "bodyType")
		roundTripper = digestTransport{base: roundTripper, auth: obj(resolved.Model, "authentication"), signBody: bodyType != "binary" && bodyType != "multipart/form-data"}
	case kind == "windows" || kind == "ntlm":
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
		event(redirectEvent(next, via[len(via)-1]))
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
	reader, closeDecoder, err := decodeContent(res.Body, res.Header.Get("Content-Encoding"))
	if err != nil {
		return response, err
	}
	defer closeDecoder()
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

func settingSource(m Object) Object {
	return Object{"model": str(m, "model"), "id": str(m, "id"), "name": str(m, "name")}
}

// timelineSettings are Yaak's "setting" timeline events: each setting the
// request is sent with, and the model it came from.
func (r resolvedRequest) timelineSettings() []Object {
	timeout := "Infinity"
	if ms := number(r.Settings, "settingRequestTimeout"); ms > 0 {
		timeout = rustDuration(time.Duration(ms) * time.Millisecond)
	}
	version := str(r.Settings, "settingHttpVersion")
	if version == "" {
		version = "auto"
	}
	events := []Object{}
	for _, s := range []struct{ name, key, value string }{
		{"validate_certificates", "settingValidateCertificates", strconv.FormatBool(boolean(r.Settings, "settingValidateCertificates"))},
		{"redirects", "settingFollowRedirects", strconv.FormatBool(boolean(r.Settings, "settingFollowRedirects"))},
		{"timeout", "settingRequestTimeout", timeout},
		{"send_cookies", "settingSendCookies", strconv.FormatBool(boolean(r.Settings, "settingSendCookies"))},
		{"store_cookies", "settingStoreCookies", strconv.FormatBool(boolean(r.Settings, "settingStoreCookies"))},
		{"http_version", "settingHttpVersion", version},
	} {
		source := r.SettingSources[s.key]
		events = append(events, Object{"type": "setting", "name": s.name, "value": s.value, "source_model": str(source, "model"), "source_id": str(source, "id"), "source_name": str(source, "name")})
	}
	return events
}

// rustDuration writes a duration as Rust's Debug does, which Yaak shows: 500ms, 30s, 1.5s.
func rustDuration(d time.Duration) string {
	if d < time.Second {
		return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
	}
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + "s"
}

// sendURLEvent is Yaak's send_url event: the request line's parts.
func sendURLEvent(req *http.Request) Object {
	u := req.URL
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p, err := strconv.Atoi(u.Port()); err == nil {
		port = p
	}
	password, _ := u.User.Password()
	return Object{"type": "send_url", "method": req.Method, "scheme": u.Scheme, "username": u.User.Username(), "password": password, "host": u.Hostname(), "port": port, "path": u.EscapedPath(), "query": u.RawQuery, "fragment": u.Fragment}
}

// redirectEvent is Yaak's redirect event: where the redirect goes, and what
// following it drops from the request.
func redirectEvent(next, previous *http.Request) Object {
	status := 0
	if next.Response != nil {
		status = next.Response.StatusCode
	}
	behavior := "preserve"
	droppedBody := previous.ContentLength > 0 && next.ContentLength <= 0 && next.GetBody == nil
	if next.Method != previous.Method || droppedBody {
		behavior = "drop_body"
	}
	dropped := []any{}
	for name := range previous.Header {
		if next.Header.Get(name) == "" {
			dropped = append(dropped, name)
		}
	}
	slices.SortFunc(dropped, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
	return Object{"type": "redirect", "status": status, "url": next.URL.String(), "behavior": behavior, "dropped_body": droppedBody, "dropped_headers": dropped}
}
