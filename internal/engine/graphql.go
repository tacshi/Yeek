package engine

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vektah/gqlparser/v2/parser"
)

const introspectionQuery = `query YeekIntrospection {
 __schema { queryType { name } mutationType { name } subscriptionType { name }
 types { kind name description fields(includeDeprecated:true) { name description isDeprecated deprecationReason args { name description defaultValue type { ...TypeRef } } type { ...TypeRef } }
 inputFields { name description defaultValue type { ...TypeRef } }
 enumValues(includeDeprecated:true) { name description isDeprecated deprecationReason }
 interfaces { kind name } possibleTypes { kind name }
 }
 directives { name description locations args {name description defaultValue type {...TypeRef}} }
 }
}
fragment TypeRef on __Type {kind name ofType {kind name ofType {kind name ofType {kind name ofType {kind name ofType {kind name ofType {kind name}}}}}}}`

func FormatGraphQL(source string) (string, error) {
	doc, err := parser.ParseQuery(&ast.Source{Input: source})
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	formatter.NewFormatter(&out).FormatQueryDocument(doc)
	return out.String(), nil
}
func (e *Engine) GraphQLSchema(ctx context.Context, id, environment string) (Object, error) {
	original, err := e.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	raw, err := e.IntrospectGraphQL(ctx, original, SendOptions{EnvironmentID: environment})
	if err != nil {
		return nil, err
	}
	if _, err = e.StoreGraphQLSchema(ctx, id, string(raw), "", ""); err != nil {
		return nil, err
	}
	var result Object
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return obj(obj(result, "data"), "__schema"), nil
}

// IntrospectGraphQL uses the request draft and inherited transport settings,
// without replacing the saved query or adding an entry to response history.
func (e *Engine) IntrospectGraphQL(ctx context.Context, original Object, options SendOptions) (content []byte, fetchErr error) {
	ctx = context.WithValue(ctx, selectedCookieJarKey{}, options.CookieJarID)
	request := clone(original)
	request["method"] = "POST"
	request["bodyType"] = "graphql"
	request["body"] = Object{"query": introspectionQuery, "variables": "{}"}
	resolved, err := e.resolve(ctx, request, options.EnvironmentID)
	if err != nil {
		return nil, err
	}
	target, err := buildURL(resolved.Model)
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Second
	if milliseconds := number(resolved.Settings, "settingRequestTimeout"); milliseconds > 0 && milliseconds < 30000 {
		timeout = time.Duration(milliseconds * float64(time.Millisecond))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, ct, err := requestBody(resolved.Model)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	seenHeaders := map[string]bool{}
	for _, h := range objects(array(resolved.Model, "headers")) {
		name := strings.TrimSpace(str(h, "name"))
		if name == "" {
			continue
		}
		if !seenHeaders[strings.ToLower(name)] {
			req.Header.Del(name)
			seenHeaders[strings.ToLower(name)] = true
		}
		if !enabled(h) && strings.EqualFold(name, "User-Agent") {
			req.Header.Set(name, "")
		}
		if enabled(h) && str(h, "name") != "" {
			if strings.EqualFold(str(h, "name"), "Host") {
				req.Host = str(h, "value")
			} else {
				req.Header.Add(str(h, "name"), str(h, "value"))
			}
		}
	}
	if err = e.beforeSend(ctx, resolved.Model, req); err != nil {
		return nil, err
	}
	if err = e.authenticate(req, resolved.Model); err != nil {
		return nil, err
	}
	transport, err := e.transport(ctx, resolved)
	if err != nil {
		return nil, err
	}
	defer transport.CloseIdleConnections()

	var rt http.RoundTripper = transport
	if str(resolved.Model, "authenticationType") == "digest" {
		rt = digestTransport{base: rt, auth: obj(resolved.Model, "authentication")}
	}
	if kind := str(resolved.Model, "authenticationType"); kind == "ntlm" || kind == "windows" {
		rt = ntlmssp.Negotiator{RoundTripper: rt}
	}
	cookieID, recordingJar, err := e.prepareCookieJar(ctx, str(original, "workspaceId"), options.CookieJarID, boolean(resolved.Settings, "settingSendCookies"), boolean(resolved.Settings, "settingStoreCookies"))
	if err != nil {
		return nil, err
	}
	client := http.Client{Transport: rt, Jar: recordingJar}
	defer func() {
		fetchErr = errors.Join(fetchErr, e.persistCookies(context.WithoutCancel(ctx), cookieID, recordingJar))
	}()
	if !boolean(resolved.Settings, "settingFollowRedirects") {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	reader := io.Reader(res.Body)
	switch strings.ToLower(res.Header.Get("Content-Encoding")) {
	case "gzip":
		gz, err := gzip.NewReader(res.Body)
		if err != nil {
			return nil, err
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	case "deflate":
		z, err := zlib.NewReader(res.Body)
		if err != nil {
			return nil, err
		}
		defer func() { _ = z.Close() }()
		reader = z
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("introspection returned %s", res.Status)
	}
	if len(raw) > 16<<20 {
		return nil, errors.New("GraphQL schema exceeds 16 MiB")
	}
	if err = e.afterReceive(ctx, resolved.Model, res, raw); err != nil {
		return nil, err
	}
	var result Object
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	schema := obj(obj(result, "data"), "__schema")
	if len(schema) == 0 {
		if problems := objects(array(result, "errors")); len(problems) > 0 {
			return nil, errors.New(str(problems[0], "message"))
		}
		return nil, errors.New("server did not return a GraphQL schema")
	}
	if _, err = LoadGraphQLSchema(raw); err != nil {
		return nil, err
	}
	if err = e.persistCookies(ctx, cookieID, recordingJar); err != nil {
		return nil, err
	}
	return raw, nil
}

func (e *Engine) StoreGraphQLSchema(ctx context.Context, id, content, key, file string) (Object, error) {
	if content != "" {
		if _, err := LoadGraphQLSchema([]byte(content)); err != nil {
			return nil, err
		}
	}
	request, err := e.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if str(request, "model") != "http_request" {
		return nil, errors.New("GraphQL schemas belong to HTTP requests")
	}
	existing, err := e.Store.Find(ctx, "graphql_introspection", "requestId", id)
	if err != nil {
		return nil, err
	}
	model := Object{"model": "graphql_introspection", "workspaceId": str(request, "workspaceId"), "requestId": id, "content": content, "schemaKey": key, "schemaFile": file}
	if len(existing) > 0 {
		model["id"] = existing[0]["id"]
	}
	return e.Save(ctx, model)
}

func (e *Engine) LoadGraphQLSchemaFile(ctx context.Context, id, path, key string) (Object, error) {
	file, err := os.Open(path) // #nosec G304 -- the user selects the schema file in a native file picker.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	return e.StoreGraphQLSchema(ctx, id, string(content), key, path)
}

func GraphQLTypeName(value Object) string {
	switch str(value, "kind") {
	case "NON_NULL":
		return GraphQLTypeName(obj(value, "ofType")) + "!"
	case "LIST":
		return "[" + GraphQLTypeName(obj(value, "ofType")) + "]"
	default:
		return strings.TrimSpace(str(value, "name"))
	}
}
