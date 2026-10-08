package engine

import (
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OAuthEndpointError struct {
	Status            int
	Code, Description string
}

func (e *OAuthEndpointError) Error() string {
	message := fmt.Sprintf("token endpoint returned HTTP %d", e.Status)
	if e.Code != "" {
		message += ": " + e.Code
	}
	if e.Description != "" {
		message += " — " + e.Description
	}
	return message
}
func (e *Engine) fetchOAuthToken(ctx context.Context, auth Object, options OAuthOptions) (OAuthToken, error) {
	grant := str(auth, "grantType")
	form := []oauthPair{{"grant_type", grant}}
	switch grant {
	case "authorization_code", "implicit":
		result, redirect, verifier, err := e.authorizeOAuth(ctx, auth, options)
		if err != nil {
			return OAuthToken{}, err
		}
		if grant == "implicit" {
			return newOAuthToken(result, str(auth, "tokenName"))
		}
		form = append(form, oauthPair{"code", str(result, "code")}, oauthPair{"redirect_uri", redirect})
		if verifier != "" {
			form = append(form, oauthPair{"code_verifier", verifier})
		}
	case "password":
		form = append(form, oauthPair{"username", str(auth, "username")}, oauthPair{"password", str(auth, "password")})
	case "refresh_token":
		if str(auth, "refreshToken") == "" {
			return OAuthToken{}, errors.New("enter a refresh token")
		}
		form = append(form, oauthPair{"refresh_token", str(auth, "refreshToken")})
	case "client_credentials":
	default:
		return OAuthToken{}, fmt.Errorf("unsupported OAuth grant %q", grant)
	}
	endpoint := cmp.Or(str(auth, "accessTokenUrl"), str(auth, "tokenUrl"))
	u, err := oauthHTTPURL(endpoint, "token URL")
	if err != nil {
		return OAuthToken{}, err
	}
	if grant != "authorization_code" && str(auth, "scope") != "" {
		form = append(form, oauthPair{"scope", str(auth, "scope")})
	}
	if str(auth, "audience") != "" {
		form = append(form, oauthPair{"audience", str(auth, "audience")})
	}
	headers := []oauthPair{{"User-Agent", "Yeek"}, {"Accept", "application/json, application/x-www-form-urlencoded"}, {"Content-Type", "application/x-www-form-urlencoded"}, {"Accept-Encoding", "gzip"}}
	credentials := str(auth, "credentials")
	if str(auth, "clientCredentialsMethod") == "client_assertion" {
		assertion, err := buildOAuthClientAssertion(auth, endpoint)
		if err != nil {
			return OAuthToken{}, err
		}
		form = append(form, oauthPair{"client_id", str(auth, "clientId")}, oauthPair{"client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, oauthPair{"client_assertion", assertion})
	} else {
		switch credentials {
		case "body", "":
			form = append(form, oauthPair{"client_id", str(auth, "clientId")}, oauthPair{"client_secret", str(auth, "clientSecret")})
		case "basic", "header":
			basic, _ := http.NewRequest(http.MethodPost, u.String(), nil)
			basic.SetBasicAuth(url.QueryEscape(str(auth, "clientId")), url.QueryEscape(str(auth, "clientSecret")))
			headers = append(headers, oauthPair{"Authorization", basic.Header.Get("Authorization")})
		case "none":
			form = append(form, oauthPair{"client_id", str(auth, "clientId")})
		default:
			return OAuthToken{}, errors.New("choose body, basic, or none for OAuth client credentials")
		}
	}
	customHeaders, err := parseOAuthPairs(auth["tokenHeaders"])
	if err != nil {
		return OAuthToken{}, fmt.Errorf("token headers: %w", err)
	}
	customBody, err := parseOAuthPairs(auth["tokenBodyParams"])
	if err != nil {
		return OAuthToken{}, fmt.Errorf("token parameters: %w", err)
	}
	if grant == "refresh_token" {
		refreshHeaders, err := parseOAuthPairs(auth["refreshHeaders"])
		if err != nil {
			return OAuthToken{}, fmt.Errorf("refresh headers: %w", err)
		}
		refreshBody, err := parseOAuthPairs(auth["refreshBodyParams"])
		if err != nil {
			return OAuthToken{}, fmt.Errorf("refresh parameters: %w", err)
		}
		customHeaders = mergeOAuthPairs(customHeaders, refreshHeaders, true)
		customBody = mergeOAuthPairs(customBody, refreshBody, false)
	}
	headers, form = mergeOAuthPairs(headers, customHeaders, true), mergeOAuthPairs(form, customBody, false)
	values := url.Values{}
	for _, pair := range form {
		values.Add(pair.Name, pair.Value)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return OAuthToken{}, err
	}
	for _, pair := range headers {
		if strings.ContainsAny(pair.Name+pair.Value, "\r\n\x00") {
			return OAuthToken{}, errors.New("OAuth headers cannot contain line breaks or NUL")
		}
		if strings.EqualFold(pair.Name, "Host") {
			request.Host = pair.Value
		} else {
			request.Header.Add(pair.Name, pair.Value)
		}
	}
	workspace, _ := defaultModel("workspace")
	if options.WorkspaceID != "" {
		workspace, err = e.Store.Get(ctx, options.WorkspaceID)
		if err != nil {
			return OAuthToken{}, err
		}
	}
	settings := maps.Clone(workspace)
	maps.Copy(settings, options.Settings)
	transport, err := e.transport(ctx, resolvedRequest{Model: Object{"url": u.String()}, Workspace: workspace, Settings: settings})
	if err != nil {
		return OAuthToken{}, err
	}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many token endpoint redirects")
		}
		if !strings.EqualFold(next.URL.Scheme, u.Scheme) || !strings.EqualFold(next.URL.Host, u.Host) {
			return errors.New("token endpoint redirected to another origin; configure its final URL")
		}
		if next.Method != http.MethodPost {
			return errors.New("token endpoint redirected to a GET request; configure its final URL")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("fetch token: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	var reader io.Reader = response.Body
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return OAuthToken{}, err
		}
		defer func() { _ = gzipReader.Close() }()
		reader = gzipReader
	}
	data, err := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
	if err != nil {
		return OAuthToken{}, err
	}
	if len(data) > 2<<20 {
		return OAuthToken{}, errors.New("token endpoint response exceeds 2 MiB")
	}
	result := Object{}
	if json.Unmarshal(data, &result) != nil {
		fields, err := url.ParseQuery(string(data))
		if err != nil || (fields.Get("access_token") == "" && fields.Get("id_token") == "" && fields.Get("error") == "") {
			return OAuthToken{}, fmt.Errorf("token endpoint returned HTTP %d with an unrecognized response", response.StatusCode)
		}
		for key, values := range fields {
			if len(values) > 0 {
				result[key] = values[0]
			}
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || str(result, "error") != "" {
		return OAuthToken{}, &OAuthEndpointError{Status: response.StatusCode, Code: oauthErrorText(str(result, "error")), Description: oauthErrorText(str(result, "error_description"))}
	}
	return newOAuthToken(result, str(auth, "tokenName"))
}
func oauthHTTPURL(raw, name string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%s must be an HTTP or HTTPS URL without credentials or a fragment", name)
	}
	return u, nil
}
func oauthErrorText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) > 512 {
		text = string([]rune(text)[:512]) + "…"
	}
	return text
}
