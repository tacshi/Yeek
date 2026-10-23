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
	behavior := args["behavior"]
	if behavior == "never" && response == nil {
		return "", nil
	}
	if behavior == "always" && isTemplatePreview(ctx) {
		behavior = "smart"
	}
	send := behavior == "smart" && response == nil || behavior == "always" || behavior == "ttl" && responseExpired(response, args["ttl"])
	if send {
		cookieJar, _ := ctx.Value(selectedCookieJarKey{}).(string)
		response, err = e.SendHTTP(ctx, id, SendOptions{EnvironmentID: environment, CookieJarID: cookieJar})
		if err != nil {
			return "", err
		}
	}
	if response == nil || str(response, "error") != "" {
		return "", nil
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
	if len(ids) == 0 {
		return value
	}
	switch v := value.(type) {
	case string:
		return remapTemplateText(v, ids)
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

func remapTemplateText(text string, ids map[string]string) string {
	var out strings.Builder
	copied, cursor := 0, 0
	for {
		relative := strings.Index(text[cursor:], "${[")
		if relative < 0 {
			break
		}
		start := cursor + relative
		cursor = start + 3
		escapes := 0
		for k := start - 1; k >= 0 && text[k] == '\\'; k-- {
			escapes++
		}
		if escapes%2 != 0 {
			continue
		}
		end, quoted := cursor, false
		for end < len(text) {
			if quoted && text[end] == '\\' {
				end += 2
				continue
			}
			if text[end] == '\'' {
				quoted = !quoted
			}
			if !quoted && strings.HasPrefix(text[end:], "]}") {
				end += 2
				break
			}
			end++
		}
		if end > len(text) || !strings.HasSuffix(text[start:end], "]}") {
			continue
		}
		if tokens, err := parseTemplate(text[start:end]); err == nil && len(tokens) == 1 && tokens[0].tag != nil {
			value := *tokens[0].tag
			if remapTemplateArgument(&value, ids) {
				out.WriteString(text[copied:start])
				out.WriteString("${[ " + formatTemplateValue(value) + " ]}")
				copied = end
			}
		}
		cursor = end
	}
	out.WriteString(text[copied:])
	return out.String()
}

func remapTemplateArgument(value *templateValue, ids map[string]string) bool {
	if value.kind != "function" {
		return false
	}
	requestArg := ""
	switch value.text {
	case "response", "response.body.raw", "response.body.path", "response.header":
		requestArg = "request"
	case "request.body", "request.body.raw", "request.body.path", "request.header", "request.param", "request.name":
		requestArg = "requestId"
	}
	changed := false
	for _, name := range value.order {
		arg := value.args[name]
		if name == requestArg && arg.kind == "string" {
			if id, ok := ids[arg.text]; ok && id != arg.text {
				arg.text = id
				changed = true
			}
		}
		if remapTemplateArgument(&arg, ids) {
			changed = true
		}
		value.args[name] = arg
	}
	return changed
}

func formatTemplateValue(v templateValue) string {
	switch v.kind {
	case "string":
		return formatTemplateString(v.text)
	case "null":
		return "null"
	case "function":
		parts := []string{}
		for _, name := range v.order {
			if arg := v.args[name]; arg.kind != "null" {
				parts = append(parts, name+"="+formatTemplateValue(arg))
			}
		}
		return v.text + "(" + strings.Join(parts, ", ") + ")"
	}
	return v.text
}

func responseExpired(response Object, ttl string) bool {
	if response == nil {
		return true
	}
	digits := strings.TrimLeft(ttl, " ")
	end := 0
	for end < len(digits) && (digits[end] >= '0' && digits[end] <= '9' || end == 0 && (digits[0] == '-' || digits[0] == '+')) {
		end++
	}
	seconds, _ := strconv.Atoi(digits[:end])
	if seconds == 0 {
		return false
	}
	created, err := time.Parse("2006-01-02T15:04:05.999999999", str(response, "createdAt"))
	return err == nil && created.Add(time.Duration(seconds)*time.Second).Before(time.Now().UTC())
}
