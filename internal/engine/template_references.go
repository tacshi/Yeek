package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type referenceDepthKey struct{}
type selectedCookieJarKey struct{}

func (e *Engine) referenceTemplate(ctx context.Context, name string, args map[string]string, workspace, environment string) (string, error) {
	depth, _ := ctx.Value(referenceDepthKey{}).(int)
	if depth >= 16 {
		return "", errors.New("request templates contain a cycle or exceed 16 levels")
	}
	ctx = context.WithValue(ctx, referenceDepthKey{}, depth+1)
	if name == "cookie.value" {
		selected, _ := ctx.Value(selectedCookieJarKey{}).(string)
		jar, err := e.selectedCookieJar(ctx, workspace, selected)
		if err != nil {
			return "", err
		}
		domainFilter := strings.TrimLeft(strings.ToLower(strings.TrimSpace(args["domain"])), ".")
		for _, cookie := range objects(array(jar, "cookies")) {
			if str(cookie, "name") != args["name"] {
				continue
			}
			domain, _ := CookieDomain(cookie)
			if domainFilter != "" && strings.TrimLeft(strings.ToLower(domain), ".") != domainFilter {
				continue
			}
			return str(cookie, "value"), nil
		}
		return "", nil
	}
	id := cmp.Or(args["requestId"], args["request"])
	request, err := e.Store.Get(ctx, id)
	if err != nil {
		return "", fmt.Errorf("referenced request %q does not exist", id)
	}
	if str(request, "model") != "http_request" {
		return "", errors.New("template needs an HTTP request")
	}
	if str(request, "workspaceId") != workspace {
		return "", errors.New("referenced request must belong to this workspace")
	}
	if strings.HasPrefix(name, "request.") {
		if name == "request.name" {
			return str(request, "name"), nil
		}
		resolved, err := e.resolve(ctx, request, environment)
		if err != nil {
			return "", err
		}
		switch name {
		case "request.header":
			for _, header := range objects(array(resolved.Model, "headers")) {
				if enabled(header) && strings.EqualFold(str(header, "name"), args["header"]) {
					return str(header, "value"), nil
				}
			}
			return "", nil
		case "request.param":
			u, err := buildURL(resolved.Model)
			if err != nil {
				return "", err
			}
			return u.Query().Get(args["param"]), nil
		case "request.body", "request.body.raw", "request.body.path":
			data, _, err := requestBody(resolved.Model)
			if err != nil {
				return "", err
			}
			if name == "request.body.path" {
				return templatePath(string(data), args["path"], args)
			}
			return string(data), nil
		}
	}
	responses, err := e.Store.Find(ctx, "http_response", "requestId", id)
	if err != nil {
		return "", err
	}
	var response Object
	for i := len(responses) - 1; i >= 0; i-- {
		if str(responses[i], "state") == "closed" {
			response = responses[i]
			break
		}
	}
	send := response == nil && args["behavior"] != "never"
	if args["behavior"] == "always" {
		send = true
	}
	if args["behavior"] == "ttl" && response != nil {
		ttl, _ := strconv.ParseFloat(args["ttl"], 64)
		created, _ := time.Parse("2006-01-02T15:04:05.999999999", str(response, "createdAt"))
		send = time.Since(created).Seconds() >= ttl
	}
	if isTemplatePreview(ctx) {
		send = false
	}
	if send {
		cookieJar, _ := ctx.Value(selectedCookieJarKey{}).(string)
		response, err = e.SendHTTP(ctx, id, SendOptions{EnvironmentID: environment, CookieJarID: cookieJar})
		if err != nil {
			return "", err
		}
	}
	if response == nil {
		return "", errors.New("referenced request has no response")
	}
	if message := str(response, "error"); message != "" {
		return "", fmt.Errorf("referenced request failed: %s", message)
	}
	switch name {
	case "response.header":
		for _, header := range objects(array(response, "headers")) {
			if strings.EqualFold(str(header, "name"), args["header"]) {
				return str(header, "value"), nil
			}
		}
		return "", nil
	case "response.body", "response.body.raw", "response.body.path":
		body, err := e.Body(str(response, "id"))
		if err != nil {
			return "", err
		}
		if name == "response.body.path" {
			return templatePath(string(body), args["path"], args)
		}
		return string(body), nil
	case "response.url":
		u, err := url.Parse(str(response, "url"))
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}
	return "", fmt.Errorf("unknown request template %q", name)
}
func rewriteTemplateReferences(value any, ids map[string]string) any {
	switch v := value.(type) {
	case string:
		for old, next := range ids {
			for _, arg := range []string{"requestId", "request"} {
				for _, quote := range []string{"'", "\""} {
					v = strings.ReplaceAll(v, arg+"="+quote+old+quote, arg+"="+quote+next+quote)
				}
			}
		}
		return v
	case map[string]any:
		copy := Object{}
		for k, value := range v {
			copy[k] = rewriteTemplateReferences(value, ids)
		}
		return copy
	case []any:
		copy := make([]any, len(v))
		for i, value := range v {
			copy[i] = rewriteTemplateReferences(value, ids)
		}
		return copy
	default:
		return value
	}
}
