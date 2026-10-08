package engine

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

type OAuthAuthorization struct {
	URL, RedirectURI, State, Nonce, GrantType, TokenName, ContextID string
}
type OAuthBrowser func(context.Context, OAuthAuthorization) (string, error)
type oauthBrowserKey struct{}
type oauthExternalKey struct{}

func WithOAuthBrowser(ctx context.Context, browser OAuthBrowser) context.Context {
	return context.WithValue(ctx, oauthBrowserKey{}, browser)
}
func WithOAuthExternalBrowser(ctx context.Context, open func(string) error) context.Context {
	return context.WithValue(ctx, oauthExternalKey{}, open)
}

type OAuthOptions struct {
	WorkspaceID, ContextID, EnvironmentID string
	Settings                              Object
	Browser                               OAuthBrowser
	OpenExternal                          func(string) error
	Force, RefreshOnly                    bool
}
type OAuthToken struct {
	Response   Object    `json:"response"`
	ObtainedAt time.Time `json:"obtainedAt"`
	ExpiresAt  time.Time `json:"expiresAt,omitzero"`
	TokenName  string    `json:"tokenName"`
}

func (t OAuthToken) Value() string { return str(t.Response, cmp.Or(t.TokenName, "access_token")) }
func (t OAuthToken) Expired(at time.Time) bool {
	return !t.ExpiresAt.IsZero() && !at.Before(t.ExpiresAt)
}

type oauthFlight struct {
	done       chan struct{}
	token      OAuthToken
	err        error
	generation uint64
}
type oauthPair struct{ Name, Value string }

var oauthPairFields = []string{"authorizationParams", "tokenHeaders", "tokenBodyParams", "refreshHeaders", "refreshBodyParams"}

func oauthBool(auth Object, key string, fallback bool) bool {
	value, exists := auth[key]
	if !exists || value == nil || value == "" {
		return fallback
	}
	if flag, ok := value.(bool); ok {
		return flag
	}
	flag, err := strconv.ParseBool(importText(value))
	if err != nil {
		return fallback
	}
	return flag
}
func normalizeOAuth(auth Object) Object {
	result := clone(auth)
	result["grantType"] = cmp.Or(str(auth, "grantType"), "client_credentials")
	defaultToken := "access_token"
	if str(auth, "responseType") == "id_token" {
		defaultToken = "id_token"
	}
	result["tokenName"] = cmp.Or(str(auth, "tokenName"), defaultToken)
	result["credentials"] = cmp.Or(str(auth, "credentials"), "body")
	if str(result, "credentials") == "header" {
		result["credentials"] = "basic"
	}
	result["usePkce"] = oauthBool(auth, "usePkce", true)
	result["pkceChallengeMethod"] = cmp.Or(str(auth, "pkceChallengeMethod"), "S256")
	result["responseType"] = cmp.Or(str(auth, "responseType"), "token")
	result["clientCredentialsMethod"] = cmp.Or(str(auth, "clientCredentialsMethod"), "client_secret")
	result["clientAssertionAlgorithm"] = cmp.Or(str(auth, "clientAssertionAlgorithm"), "HS256")
	result["clientAssertionSecretBase64"] = oauthBool(auth, "clientAssertionSecretBase64", false)
	return result
}
func (r resolvedRequest) oauthOptions(environment string) OAuthOptions {
	return OAuthOptions{WorkspaceID: str(r.Workspace, "id"), ContextID: r.AuthOwnerID, EnvironmentID: environment, Settings: r.Settings}
}

// PrepareOAuth resolves a saved scope's network settings and the editable auth values.
func (e *Engine) PrepareOAuth(ctx context.Context, ownerID, environment string, auth Object) (Object, OAuthOptions, error) {
	owner, err := e.Store.Get(ctx, ownerID)
	if err != nil {
		return nil, OAuthOptions{}, err
	}
	workspace, folder := str(owner, "workspaceId"), str(owner, "folderId")
	if str(owner, "model") == "workspace" {
		workspace = ownerID
	}
	if str(owner, "model") == "folder" {
		folder = ownerID
	}
	root, err := e.Store.Get(ctx, workspace)
	if err != nil {
		return nil, OAuthOptions{}, err
	}
	settings := maps.Clone(root)
	chain := []Object{owner}
	seen := map[string]bool{}
	for id := str(owner, "folderId"); id != ""; {
		if seen[id] {
			return nil, OAuthOptions{}, errors.New("folder settings contain a cycle")
		}
		seen[id] = true
		parent, err := e.Store.Get(ctx, id)
		if err != nil {
			return nil, OAuthOptions{}, err
		}
		chain = append(chain, parent)
		id = str(parent, "folderId")
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if str(chain[i], "model") == "workspace" {
			continue
		}
		for key := range root {
			if strings.HasPrefix(key, "setting") {
				value := obj(chain[i], key)
				if boolean(value, "enabled") {
					settings[key] = value["value"]
				}
			}
		}
	}
	rendered, err := e.RenderOAuth(ctx, auth, workspace, folder, environment, ownerID)
	return rendered, OAuthOptions{WorkspaceID: workspace, ContextID: ownerID, EnvironmentID: environment, Settings: settings}, err
}
func parseOAuthPairs(value any) ([]oauthPair, error) {
	if value == nil || value == "" {
		return nil, nil
	}
	var rows []Object
	if text, ok := value.(string); ok {
		if err := json.Unmarshal([]byte(text), &rows); err != nil {
			return nil, errors.New("custom parameters must be a JSON array of name/value rows")
		}
	} else {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &rows); err != nil {
			return nil, errors.New("custom parameters must contain name/value rows")
		}
	}
	result := []oauthPair{}
	for _, row := range rows {
		if !enabled(row) {
			continue
		}
		name := strings.TrimSpace(str(row, "name"))
		if name != "" {
			result = append(result, oauthPair{name, importText(row["value"])})
		}
	}
	return result, nil
}
func mergeOAuthPairs(base, override []oauthPair, insensitive bool) []oauthPair {
	key := func(name string) string {
		if insensitive {
			return strings.ToLower(name)
		}
		return name
	}
	replaced := map[string]bool{}
	for _, pair := range override {
		replaced[key(pair.Name)] = true
	}
	result := slices.DeleteFunc(slices.Clone(base), func(pair oauthPair) bool { return replaced[key(pair.Name)] })
	return append(result, override...)
}

// RenderOAuth keeps substituted pair values inside their rows, even when they contain JSON quotes.
func (e *Engine) RenderOAuth(ctx context.Context, auth Object, workspace, folder, environment, request string) (Object, error) {
	ctx = context.WithValue(ctx, templateRequestKey{}, request)
	result := clone(auth)
	for key, value := range auth {
		if slices.Contains(oauthPairFields, key) {
			pairs, err := parseOAuthPairs(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			rows := []any{}
			for _, pair := range pairs {
				name, err := e.Render(ctx, pair.Name, workspace, folder, environment)
				if err != nil {
					return nil, err
				}
				value, err := e.Render(ctx, pair.Value, workspace, folder, environment)
				if err != nil {
					return nil, err
				}
				rows = append(rows, Object{"name": name, "value": value, "enabled": true})
			}
			result[key] = rows
			continue
		}
		if text, ok := value.(string); ok {
			rendered, err := e.Render(ctx, text, workspace, folder, environment)
			if err != nil {
				return nil, err
			}
			result[key] = rendered
		}
	}
	return normalizeOAuth(result), nil
}

func oauthTokenKey(auth Object, options OAuthOptions) string {
	config := normalizeOAuth(auth)
	for _, key := range []string{"accessToken", "refreshToken", "idToken", "token", "tokenSource", "headerName", "headerPrefix"} {
		delete(config, key)
	}
	for _, key := range oauthPairFields {
		pairs, _ := parseOAuthPairs(config[key])
		config[key] = pairs
	}
	return "ot_" + importHash([]any{options.WorkspaceID, options.ContextID, options.EnvironmentID, config})
}
func validateOAuthPairs(auth Object) error {
	for _, key := range oauthPairFields {
		if _, err := parseOAuthPairs(auth[key]); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

// FetchOAuthToken retains the uncached API for callers without a saved request context.
func (e *Engine) FetchOAuthToken(ctx context.Context, auth Object, openURL func(string) error) (Object, error) {
	token, err := e.fetchOAuthToken(ctx, normalizeOAuth(auth), OAuthOptions{OpenExternal: openURL})
	return token.Response, err
}

func (e *Engine) AcquireOAuthToken(ctx context.Context, auth Object, options OAuthOptions) (OAuthToken, error) {
	auth = normalizeOAuth(auth)
	if err := validateOAuthPairs(auth); err != nil {
		return OAuthToken{}, err
	}
	if options.Browser == nil {
		options.Browser, _ = ctx.Value(oauthBrowserKey{}).(OAuthBrowser)
	}
	if options.OpenExternal == nil {
		options.OpenExternal, _ = ctx.Value(oauthExternalKey{}).(func(string) error)
	}
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		return OAuthToken{}, errors.New("application is closing")
	}
	e.workers.Add(1)
	e.mu.Unlock()
	defer e.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	defer func() { stop(); cancel() }()
	key := oauthTokenKey(auth, options)
	e.oauthMu.Lock()
	if e.oauthFlights == nil {
		e.oauthFlights = map[string]*oauthFlight{}
	}
	if e.oauthGenerations == nil {
		e.oauthGenerations = map[string]uint64{}
	}
	if flight := e.oauthFlights[key]; flight != nil {
		e.oauthMu.Unlock()
		select {
		case <-ctx.Done():
			return OAuthToken{}, ctx.Err()
		case <-flight.done:
			return flight.token, flight.err
		}
	}
	flight := &oauthFlight{done: make(chan struct{}), generation: e.oauthGenerations[key]}
	e.oauthFlights[key] = flight
	e.oauthMu.Unlock()
	token, err := e.acquireOAuthToken(ctx, key, auth, options, flight.generation)
	e.oauthMu.Lock()
	flight.token, flight.err = token, err
	close(flight.done)
	delete(e.oauthFlights, key)
	e.oauthMu.Unlock()
	return token, err
}
func (e *Engine) acquireOAuthToken(ctx context.Context, key string, auth Object, options OAuthOptions, generation uint64) (OAuthToken, error) {
	cached, err := e.readOAuthToken(ctx, key)
	if err != nil {
		return OAuthToken{}, err
	}
	if cached != nil && !cached.Expired(time.Now()) && !options.Force && !options.RefreshOnly {
		return *cached, nil
	}
	refresh := ""
	if cached != nil {
		refresh = str(cached.Response, "refresh_token")
	}
	if refresh == "" {
		refresh = str(auth, "refreshToken")
	}
	var token OAuthToken
	if refresh != "" && (!options.Force || options.RefreshOnly) {
		request := maps.Clone(auth)
		request["grantType"], request["refreshToken"] = "refresh_token", refresh
		token, err = e.fetchOAuthToken(ctx, request, options)
		if err == nil && str(token.Response, "refresh_token") == "" {
			token.Response["refresh_token"] = refresh
		}
		if err != nil {
			endpointError, invalid := errors.AsType[*OAuthEndpointError](err)
			invalidRefresh := invalid && (endpointError.Status >= 400 && endpointError.Status < 500 || endpointError.Code == "invalid_grant" || endpointError.Code == "invalid_token")
			if !invalidRefresh {
				return OAuthToken{}, err
			}
			if removeErr := e.removeOAuthToken(ctx, key); removeErr != nil {
				return OAuthToken{}, removeErr
			}
			if options.RefreshOnly {
				return OAuthToken{}, err
			}
		}
	} else if options.RefreshOnly {
		return OAuthToken{}, errors.New("the current token has no refresh token")
	}
	if token.Value() == "" {
		token, err = e.fetchOAuthToken(ctx, auth, options)
		if err != nil {
			return OAuthToken{}, err
		}
	}
	if err = e.saveOAuthToken(ctx, key, options, token, generation); err != nil {
		return OAuthToken{}, err
	}
	return token, nil
}
