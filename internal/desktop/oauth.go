package desktop

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type oauthEditorState struct {
	source, error, operation, pairTab                string
	generation                                       uint64
	busy, loading, advanced, showToken, showResponse bool
	token                                            *engine.OAuthToken
	cancel                                           context.CancelFunc
	pairSource                                       map[string]string
	pairs                                            map[string][]KV
}

func (a *App) oauthState(d *Draft) *oauthEditorState {
	if a.oauthEditors == nil {
		a.oauthEditors = map[string]*oauthEditorState{}
	}
	state := a.oauthEditors[d.ID]
	if state == nil {
		state = &oauthEditorState{pairTab: "Authorization Params", pairs: map[string][]KV{}, pairSource: map[string]string{}}
		a.oauthEditors[d.ID] = state
	}
	return state
}
func oauthDraftSource(d *Draft, environment string) string {
	data, _ := json.Marshal([]any{environment, d.Auth}, json.Deterministic(true))
	return string(data)
}
func draftAuthObject(d *Draft) engine.Object {
	result := engine.Object{}
	if d.AuthDisabled != nil {
		result["disabled"] = d.AuthDisabled
	}
	for key, value := range d.Auth {
		result[key] = value
		if d.AuthType == "oauth2" && (key == "usePkce" || key == "useExternalBrowser" || key == "clientAssertionSecretBase64") {
			if flag, err := strconv.ParseBool(value); err == nil {
				result[key] = flag
			}
		}
	}
	return result
}
func (a *App) syncOAuthState(d *Draft, state *oauthEditorState) {
	source := oauthDraftSource(d, a.environment)
	if state.source == source {
		return
	}
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	state.generation++
	state.source, state.error, state.token = source, "", nil
	state.busy, state.loading = false, true
	generation, auth, environment, id := state.generation, draftAuthObject(d), a.environment, d.ID
	a.background(func() (func(), error) {
		rendered, options, err := a.Engine.PrepareOAuth(a.ctx, id, environment, auth)
		var token *engine.OAuthToken
		if err == nil {
			token, err = a.Engine.StoredOAuthToken(a.ctx, rendered, options)
		}
		return func() {
			if state.generation != generation {
				return
			}
			state.loading = false
			if err != nil {
				state.error = err.Error()
			} else {
				state.token = token
			}
		}, nil
	})
}
func (a *App) runOAuth(d *Draft, action string) {
	if a.deferUntilInputs(func() { a.runOAuth(d, action) }) {
		return
	}
	state := a.oauthState(d)
	if state.busy {
		return
	}
	if d == a.drafts[d.ID] {
		a.save(d)
	}
	state.generation++
	state.source = oauthDraftSource(d, a.environment)
	state.busy, state.loading, state.error, state.operation = true, false, "", action
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	generation, auth, environment, id := state.generation, draftAuthObject(d), a.environment, d.ID
	a.background(func() (func(), error) {
		defer cancel()
		rendered, options, err := a.Engine.PrepareOAuth(ctx, id, environment, auth)
		var token *engine.OAuthToken
		if err == nil {
			options.Browser, options.OpenExternal = a.oauthBrowser, mygo.Shell.OpenExternal
			switch action {
			case "Delete":
				err = a.Engine.DeleteOAuthToken(ctx, rendered, options)
			default:
				options.Force, options.RefreshOnly = action == "Fetch", action == "Refresh"
				value, fetchErr := a.Engine.AcquireOAuthToken(ctx, rendered, options)
				err = fetchErr
				if err == nil {
					token = &value
				}
			}
		}
		return func() {
			if state.generation != generation {
				return
			}
			state.busy, state.cancel = false, nil
			if err != nil {
				state.error = err.Error()
				return
			}
			state.token = token
			if token != nil {
				d.Auth["tokenSource"] = "automatic"
				d.Dirty = true
				state.source = oauthDraftSource(d, a.environment)
				if d == a.drafts[d.ID] {
					a.save(d)
				}
			}
		}, nil
	})
}
func (a *App) oauthText(c *ui.Context, p colors, d *Draft, key, label string, secret, multiline bool) {
	value := d.Auth[key]
	ui.Column(c).Key(key).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.muted)
		if multiline {
			if ui.TextArea(c, &value).Label("OAuth " + label).Height(100).Font("monospace").FontSize(12).Changed() {
				d.Auth[key] = value
				d.Dirty = true
			}
			return
		}
		entry := a.templateInput(c, p, &value, d.ID+":oauth:"+key, "OAuth "+label, "", false).FillWidth().Height(32).Border(1, p.border).Radius(4)
		if secret {
			entry.Password()
		}
		if entry.Changed() {
			d.Auth[key] = value
			d.Dirty = true
		}
	})
}
func (a *App) oauthChoice(c *ui.Context, p colors, d *Draft, key, label string, values []string, labels []string) {
	selected := labels[0]
	for i, value := range values {
		if d.Auth[key] == value {
			selected = labels[i]
		}
	}
	ui.Column(c).Key(key).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(p.muted)
		if ui.Select(c, &selected, labels).Label("OAuth " + label).FillWidth().Changed() {
			for i, label := range labels {
				if label == selected {
					value := values[i]
					a.deferUntilInputs(func() {
						d.Auth[key] = value
						if key == "grantType" && value != "client_credentials" {
							d.Auth["clientCredentialsMethod"] = "client_secret"
						}
						if key == "responseType" && value == "id_token" {
							d.Auth["tokenName"] = "id_token"
						} else if key == "responseType" && value == "token" {
							d.Auth["tokenName"] = "access_token"
						}
						d.Dirty = true
					})
					break
				}
			}
		}
	})
}
func (a *App) oauthCheckbox(c *ui.Context, d *Draft, key, label string, defaultValue bool) {
	value := defaultValue
	if raw, exists := d.Auth[key]; exists && raw != "" {
		value = raw == "true"
	}
	if ui.Checkbox(c, &value, label).Changed() {
		a.deferUntilInputs(func() { d.Auth[key] = strconv.FormatBool(value); d.Dirty = true })
	}
}
func (a *App) oauthEditor(c *ui.Context, p colors, d *Draft) {
	state := a.oauthState(d)
	defaults := map[string]string{"grantType": "authorization_code", "credentials": "body", "tokenName": "access_token", "responseType": "token", "clientCredentialsMethod": "client_secret", "clientAssertionAlgorithm": "HS256", "pkceChallengeMethod": "S256"}
	for key, value := range defaults {
		if d.Auth[key] == "" {
			d.Auth[key] = value
		}
	}
	if _, exists := d.Auth["headerName"]; !exists {
		d.Auth["headerName"] = "Authorization"
	}
	if _, exists := d.Auth["headerPrefix"]; !exists {
		d.Auth["headerPrefix"] = "Bearer"
	}
	a.oauthChoice(c, p, d, "grantType", "Grant Type", []string{"authorization_code", "client_credentials", "implicit", "password", "refresh_token"}, []string{"Authorization Code", "Client Credentials", "Implicit", "Password", "Refresh Token"})
	grant := d.Auth["grantType"]
	a.oauthText(c, p, d, "clientId", "Client ID", false, false)
	if grant == "client_credentials" {
		a.oauthChoice(c, p, d, "clientCredentialsMethod", "Client Authentication", []string{"client_secret", "client_assertion"}, []string{"Client Secret", "JWT Client Assertion"})
	}
	if grant != "implicit" {
		if d.Auth["clientCredentialsMethod"] == "client_assertion" && grant == "client_credentials" {
			algorithms := []string{"HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA", "none"}
			a.oauthChoice(c, p, d, "clientAssertionAlgorithm", "Assertion Algorithm", algorithms, algorithms)
			a.oauthText(c, p, d, "clientAssertionSecret", "Assertion Key", true, true)
			a.oauthCheckbox(c, d, "clientAssertionSecretBase64", "Assertion key is base64 encoded", false)
		} else {
			a.oauthText(c, p, d, "clientSecret", "Client Secret", true, false)
		}
		a.oauthText(c, p, d, "accessTokenUrl", "Token URL", false, false)
	}
	if grant == "authorization_code" || grant == "implicit" {
		a.oauthText(c, p, d, "authorizationUrl", "Authorization URL", false, false)
		a.oauthText(c, p, d, "redirectUri", "Redirect URI", false, false)
		a.oauthChoice(c, p, d, "useExternalBrowser", "Sign-in Browser", []string{"false", "true"}, []string{"MyGo Sign-in Window", "System Browser"})
		if d.Auth["useExternalBrowser"] == "true" && d.Auth["redirectUri"] == "" {
			a.oauthText(c, p, d, "callbackPort", "Callback Port", false, false)
		}
	}
	if grant == "authorization_code" {
		a.oauthCheckbox(c, d, "usePkce", "Use PKCE", true)
		if d.Auth["usePkce"] != "false" {
			a.oauthChoice(c, p, d, "pkceChallengeMethod", "PKCE Method", []string{"S256", "plain"}, []string{"S256", "plain"})
		}
	}
	if grant == "implicit" {
		a.oauthChoice(c, p, d, "responseType", "Response Type", []string{"token", "id_token", "id_token token"}, []string{"Access Token", "ID Token", "ID and Access Token"})
	}
	if grant == "password" {
		a.oauthText(c, p, d, "username", "Username", false, false)
		a.oauthText(c, p, d, "password", "Password", true, false)
	}
	if grant == "refresh_token" {
		a.oauthText(c, p, d, "refreshToken", "Refresh Token", true, false)
	}
	a.oauthText(c, p, d, "scope", "Scope", false, false)
	if ui.Button(c, "More OAuth Options").Clicked() {
		a.deferUntilInputs(func() { state.advanced = !state.advanced })
	}
	if state.advanced {
		a.oauthText(c, p, d, "audience", "Audience", false, false)
		a.oauthText(c, p, d, "state", "State", false, false)
		if grant == "authorization_code" && d.Auth["usePkce"] != "false" {
			a.oauthText(c, p, d, "pkceCodeVerifier", "PKCE Verifier", true, false)
		}
		a.oauthChoice(c, p, d, "tokenName", "Token to Send", []string{"access_token", "id_token"}, []string{"Access Token", "ID Token"})
		if grant != "implicit" {
			a.oauthChoice(c, p, d, "credentials", "Send Client Credentials", []string{"body", "basic", "none"}, []string{"Request Body", "Basic Authentication", "Client ID Only"})
		}
		a.oauthText(c, p, d, "headerName", "Header Name", false, false)
		a.oauthText(c, p, d, "headerPrefix", "Header Prefix", false, false)
		a.oauthPairsEditor(c, p, d, state)
		if ui.Button(c, "Clear All Sign-in Sessions").Disabled(a.oauthBrowserCount > 0).Clicked() {
			a.background(func() (func(), error) {
				err := mygo.App.ClearBrowsingData()
				return func() {
					if err != nil {
						state.error = err.Error()
					} else {
						state.error = ""
					}
				}, nil
			})
		}
	}
	manual := d.Auth["tokenSource"] == "manual" || d.Auth["tokenSource"] == "" && d.Auth["accessToken"] != ""
	if ui.Checkbox(c, &manual, "Use an explicit token").Changed() {
		a.deferUntilInputs(func() {
			d.Auth["tokenSource"] = "automatic"
			if manual {
				d.Auth["tokenSource"] = "manual"
			}
			d.Dirty = true
		})
	}
	if manual {
		a.oauthText(c, p, d, "accessToken", "Explicit Access Token", true, false)
	}
	a.oauthTokenView(c, p, d, state)
	a.deferUntilInputs(func() { a.syncOAuthState(d, state) })
}
func (a *App) oauthPairsEditor(c *ui.Context, p colors, d *Draft, state *oauthEditorState) {
	labels := []string{"Authorization Params", "Token Headers", "Token Body Params", "Refresh Headers", "Refresh Body Params"}
	keys := []string{"authorizationParams", "tokenHeaders", "tokenBodyParams", "refreshHeaders", "refreshBodyParams"}
	group := state.pairTab
	if ui.Select(c, &group, labels).Label("OAuth parameter group").FillWidth().Changed() {
		a.deferUntilInputs(func() { state.pairTab = group })
	}
	for i, label := range labels {
		if state.pairTab != label {
			continue
		}
		key := keys[i]
		if state.pairSource[key] != d.Auth[key] {
			var rows []any
			if text := d.Auth[key]; text != "" {
				if json.Unmarshal([]byte(text), &rows) != nil {
					ui.Text(c, "Invalid custom parameter JSON").TextColor(p.red)
					return
				}
			}
			state.pairs[key] = kvRows(engine.Object{"rows": rows}, "rows")
			state.pairSource[key] = d.Auth[key]
		}
		rows := state.pairs[key]
		dirty := false
		ui.Box(c).Height(170).Children(func() { a.kvEditor(c, p, &rows, "Name", "Value", &dirty) })
		state.pairs[key] = rows
		data, _ := json.Marshal(rowObjects(rows), json.Deterministic(true))
		if dirty || len(rowObjects(rows)) > 0 && string(data) != state.pairSource[key] {
			d.Auth[key] = string(data)
			state.pairSource[key] = string(data)
			d.Dirty = true
		}
	}
}
func (a *App) oauthTokenView(c *ui.Context, p colors, d *Draft, state *oauthEditorState) {
	ui.Column(c).Padding(12).Gap(10).Border(1, p.border).Radius(5).Children(func() {
		if state.token == nil {
			ui.Text(c, "No stored token").TextColor(p.muted)
		} else {
			label := "No expiry provided"
			if !state.token.ExpiresAt.IsZero() {
				c.After(time.Second)
				prefix := "Expires "
				if state.token.Expired(time.Now()) {
					prefix = "Expired "
				}
				label = prefix + state.token.ExpiresAt.Local().Format("2 Jan 2006 15:04:05")
			}
			ui.Text(c, label).FontSize(12).TextColor(p.muted)
			value := state.token.Value()
			ui.Row(c).Gap(8).Children(func() {
				entry := ui.TextInput(c, &value).ReadOnly(true).Label("Stored OAuth token").Grow(1)
				if !state.showToken {
					entry.Password()
				}
				label := "Show"
				if state.showToken {
					label = "Hide"
				}
				if ui.Button(c, label).Clicked() {
					state.showToken = !state.showToken
				}
				if ui.Button(c, "Copy Token").Clicked() {
					c.WriteClipboard(value)
				}
			})
		}
		if state.error != "" {
			ui.Text(c, state.error).TextColor(p.red).MaxLines(4).Selectable()
		}
		ui.Row(c).Gap(8).Children(func() {
			if state.busy {
				ui.Text(c, map[string]string{"Fetch": "Fetching token…", "Refresh": "Refreshing token…", "Delete": "Deleting token…"}[state.operation]).Grow(1).TextColor(p.muted)
				if ui.Button(c, "Cancel Authorization").Clicked() {
					if state.cancel != nil {
						state.cancel()
					}
				}
				return
			}
			if ui.PrimaryButton(c, "Fetch Token").Clicked() {
				a.runOAuth(d, "Fetch")
			}
			if state.token != nil {
				if ui.Button(c, "Refresh Token").Disabled(s(state.token.Response, "refresh_token") == "").Clicked() {
					a.runOAuth(d, "Refresh")
				}
				if ui.Button(c, "Delete Token").Clicked() {
					a.runOAuth(d, "Delete")
				}
				if ui.Button(c, "Token Response").Clicked() {
					state.showResponse = !state.showResponse
				}
			}
		})
		if state.showResponse && state.token != nil {
			data, _ := json.Marshal(state.token.Response, jsontext.WithIndent("  "), json.Deterministic(true))
			ui.Box(c).Height(220).Children(func() { a.codeView(c, p, string(data), "json", "OAuth token response "+d.ID) })
		}
	})
}

func (a *App) authorizationContext(ctx context.Context) context.Context {
	ctx = engine.WithOAuthBrowser(ctx, a.oauthBrowser)
	ctx = engine.WithTemplatePrompter(ctx, a.templatePrompter)
	return engine.WithOAuthExternalBrowser(ctx, mygo.Shell.OpenExternal)
}

func oauthAuthStrings(auth engine.Object) map[string]string {
	result := map[string]string{}
	for key, value := range auth {
		switch value := value.(type) {
		case nil:
			result[key] = ""
		case string:
			result[key] = value
		case bool:
			result[key] = strconv.FormatBool(value)
		case []any, map[string]any:
			data, _ := json.Marshal(value)
			result[key] = string(data)
		default:
			result[key] = fmt.Sprint(value)
		}
	}
	return result
}
