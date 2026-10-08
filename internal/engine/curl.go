package engine

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
)

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func curlFormQuote(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\""
}

func (e *Engine) Curl(ctx context.Context, id, environment string) (string, error) {
	model, err := e.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	resolved, err := e.resolve(ctx, model, environment)
	if err != nil {
		return "", err
	}
	model = resolved.Model
	method, kind := str(model, "method"), str(model, "bodyType")
	if err = ValidateHTTPMethod(method); err != nil {
		return "", err
	}
	target, err := buildURL(model)
	if err != nil {
		return "", err
	}
	parts := []string{"curl", "--globoff"}
	option := func(flag, value string) { parts = append(parts, flag, shellQuote(value)) }
	if method == "HEAD" && kind == "" {
		parts = append(parts, "--head")
	} else {
		option("-X", method)
	}
	if !boolean(resolved.Settings, "settingValidateCertificates") {
		parts = append(parts, "--insecure")
	}
	if timeout := number(resolved.Settings, "settingRequestTimeout"); timeout > 0 {
		option("--max-time", strconv.FormatFloat(timeout/1000, 'f', -1, 64))
	}
	authKind, auth := str(model, "authenticationType"), obj(model, "authentication")
	if !authApplies(model) {
		authKind = ""
	}
	headers := http.Header{}
	hasContentType, suppressContentType := false, false
	for _, row := range objects(array(model, "headers")) {
		name := str(row, "name")
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "Content-Type") {
			hasContentType = true
			suppressContentType = !enabled(row)
		}
		if !enabled(row) {
			option("--header", name+":")
			continue
		}
		value := str(row, "value")
		if kind == "multipart/form-data" && strings.EqualFold(name, "Content-Type") && openAPIMediaType(value) == "multipart/form-data" {
			continue
		}
		headers.Add(name, value)
	}
	var inlineBody []byte
	contentType := ""
	if kind != "binary" && kind != "multipart/form-data" {
		inlineBody, contentType, err = requestBody(model)
		if err != nil {
			return "", err
		}
		if bytes.IndexByte(inlineBody, 0) >= 0 {
			return "", errors.New("use a binary file body to copy data containing NUL as cURL")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(inlineBody))
	if err != nil {
		return "", err
	}
	request.Header = headers
	if !hasContentType && contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	switch authKind {
	case "digest":
		parts = append(parts, "--digest")
		option("--user", str(auth, "username")+":"+str(auth, "password"))
	case "windows", "ntlm":
		parts = append(parts, "--ntlm")
		username := str(auth, "username")
		if domain := str(auth, "domain"); domain != "" {
			username = domain + "\\" + username
		}
		option("--user", username+":"+str(auth, "password"))
	case "awsv4", "aws":
		option("--aws-sigv4", "aws:amz:"+cmp.Or(str(auth, "region"), "us-east-1")+":"+cmp.Or(str(auth, "service"), "sts"))
		option("--user", str(auth, "accessKeyId")+":"+str(auth, "secretAccessKey"))
		if token := str(auth, "sessionToken"); token != "" {
			headers.Set("X-Amz-Security-Token", token)
		}
	case "", "none", "basic", "bearer", "apikey", "oauth2", "oauth1", "jwt":
		authModel := model
		if authKind == "apikey" && cmp.Or(str(auth, "key"), str(auth, "name")) == "" {
			// Yaak's Copy as cURL names an unnamed key X-Api-Key, or token in the query.
			key := "X-Api-Key"
			if cmp.Or(str(auth, "location"), str(auth, "in")) == "query" {
				key = "token"
			}
			authModel = maps.Clone(model)
			authModel["authentication"] = maps.Clone(auth)
			obj(authModel, "authentication")["key"] = key
		}
		if err = e.authenticate(request, authModel, resolved.oauthOptions(environment)); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("copy as cURL does not support authentication type %q", authKind)
	}
	option("--url", request.URL.String())
	if cookies := headers.Values("Cookie"); len(cookies) > 1 {
		headers.Set("Cookie", strings.Join(cookies, "; "))
	}
	for _, row := range objects(headerModels(headers)) {
		value := str(row, "name") + ": " + str(row, "value")
		if str(row, "value") == "" {
			value = str(row, "name") + ";"
		}
		option("--header", value)
	}
	switch kind {
	case "binary":
		path := str(obj(model, "body"), "filePath")
		if strings.TrimSpace(path) == "" {
			return "", errors.New("choose a file before copying cURL")
		}
		if !hasContentType {
			option("--header", "Content-Type: application/octet-stream")
		}
		option("--data-binary", "@"+path)
	case "multipart/form-data":
		parts = append(parts, "--form-escape")
		count := 0
		for _, row := range objects(array(obj(model, "body"), "form")) {
			if !enabled(row) || str(row, "name") == "" {
				continue
			}
			name := str(row, "name")
			if strings.ContainsAny(name, "=\r\n\x00") {
				return "", errors.New("cURL form names cannot contain '=' or control characters")
			}
			if err = ValidateMultipartMetadata(str(row, "filename"), str(row, "contentType")); err != nil {
				return "", err
			}
			value, contentType := str(row, "value"), str(row, "contentType")
			if IsFileFormField(row) {
				path := str(row, "file")
				if strings.TrimSpace(path) == "" {
					return "", fmt.Errorf("%s: choose a file before copying cURL", name)
				}
				value = "@" + curlFormQuote(path)
				if filename := str(row, "filename"); filename != "" {
					value += ";filename=" + curlFormQuote(filename)
				}
				if contentType == "" {
					contentType = GuessContentType(path)
				}
			} else if contentType == "" {
				if strings.ContainsRune(value, 0) {
					return "", errors.New("use a file field to copy data containing NUL as cURL")
				}
				option("--form-string", name+"="+value)
				count++
				continue
			} else {
				value = curlFormQuote(value)
			}
			if contentType != "" {
				value += ";type=" + contentType
			}
			option("--form", name+"="+value)
			count++
		}
		if count == 0 {
			body, contentType, err := requestBody(model)
			if err != nil {
				return "", err
			}
			if !suppressContentType {
				option("--header", "Content-Type: "+contentType)
			}
			option("--data-binary", string(body))
		}
	default:
		if len(inlineBody) > 0 || kind != "" && (kind != "graphql" || !strings.EqualFold(method, "GET")) {
			option("--data-raw", string(inlineBody))
		}
	}
	for _, part := range parts {
		if strings.ContainsRune(part, 0) {
			return "", errors.New("cURL arguments cannot contain NUL")
		}
	}
	return strings.Join(parts, " "), nil
}
