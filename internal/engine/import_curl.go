package engine

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

func ParseCurl(command string) (Object, error) {
	args, err := splitShell(command)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 || args[0] != "curl" {
		return nil, errors.New("paste a command beginning with curl")
	}
	m, _ := defaultModel("http_request")
	m["name"] = "Imported cURL"
	headers := []any{}
	explicitMethod := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		value := ""
		valueSet := false
		if strings.HasPrefix(arg, "--") {
			if k, v, ok := strings.Cut(arg, "="); ok {
				arg, value = k, v
				valueSet = true
			}
		} else if len(arg) > 2 && strings.ContainsAny(arg[1:2], "XHduFm") {
			value, arg, valueSet = arg[2:], arg[:2], true
		}
		take := func() (string, error) {
			if valueSet {
				return value, nil
			}
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s needs a value", arg)
			}
			return args[i], nil
		}
		switch arg {
		case "-X", "--request":
			v, err := take()
			if err != nil {
				return nil, err
			}
			m["method"] = v
			explicitMethod = true
		case "-H", "--header":
			v, err := take()
			if err != nil {
				return nil, err
			}
			name, value, ok := strings.Cut(v, ":")
			if !ok {
				if strings.HasSuffix(v, ";") {
					headers = append(headers, Object{"name": strings.TrimSuffix(v, ";"), "value": "", "enabled": true})
					continue
				}
				return nil, errors.New("header must contain a colon")
			}
			headers = append(headers, Object{"name": name, "value": strings.TrimSpace(value), "enabled": strings.TrimSpace(value) != ""})
		case "-F", "--form", "--form-string":
			v, err := take()
			if err != nil {
				return nil, err
			}
			field, err := parseCurlForm(v, arg == "--form-string")
			if err != nil {
				return nil, err
			}
			body := obj(m, "body")
			body["form"] = append(array(body, "form"), field)
			m["body"], m["bodyType"] = body, "multipart/form-data"
			if !explicitMethod {
				m["method"] = "POST"
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--json":
			v, err := take()
			if err != nil {
				return nil, err
			}
			if !explicitMethod {
				m["method"] = "POST"
			}
			m["bodyType"] = "text/plain"
			if arg == "--json" {
				m["bodyType"] = "application/json"
			}
			if strings.HasPrefix(v, "@") && arg != "--data-raw" {
				m["bodyType"] = "binary"
				m["body"] = Object{"filePath": strings.TrimPrefix(v, "@")}
			} else {
				if previous := str(obj(m, "body"), "text"); previous != "" {
					v = previous + "&" + v
				}
				m["body"] = Object{"text": v}
			}
		case "-u", "--user":
			v, err := take()
			if err != nil {
				return nil, err
			}
			user, password, _ := strings.Cut(v, ":")
			auth := obj(m, "authentication")
			if str(m, "authenticationType") == "awsv4" {
				auth["accessKeyId"], auth["secretAccessKey"] = user, password
			} else {
				auth["username"], auth["password"] = user, password
				if str(m, "authenticationType") == "" {
					m["authenticationType"] = "basic"
				}
			}
			m["authentication"] = auth
		case "--digest", "--ntlm", "--basic":
			m["authenticationType"] = strings.TrimPrefix(arg, "--")
		case "--aws-sigv4":
			v, err := take()
			if err != nil {
				return nil, err
			}
			fields := strings.Split(v, ":")
			if len(fields) != 4 {
				return nil, errors.New("specify region and service in --aws-sigv4")
			}
			auth := obj(m, "authentication")
			auth["region"], auth["service"] = fields[2], fields[3]
			if user, ok := auth["username"]; ok {
				auth["accessKeyId"], auth["secretAccessKey"] = user, auth["password"]
				delete(auth, "username")
				delete(auth, "password")
			}
			m["authenticationType"], m["authentication"] = "awsv4", auth
		case "-m", "--max-time":
			v, err := take()
			if err != nil {
				return nil, err
			}
			seconds, err := strconv.ParseFloat(v, 64)
			if err != nil || math.IsNaN(seconds) || seconds < 0 || seconds > 86400 {
				return nil, errors.New("cURL timeout must be between 0 and 86400 seconds")
			}
			m["settingRequestTimeout"] = Object{"enabled": true, "value": seconds * 1000}
		case "--url":
			v, err := take()
			if err != nil {
				return nil, err
			}
			m["url"] = v
		case "-I", "--head":
			m["method"] = "HEAD"
			explicitMethod = true
		case "-k", "--insecure":
			m["settingValidateCertificates"] = Object{"enabled": true, "value": false}
		case "-L", "--location":
			m["settingFollowRedirects"] = Object{"enabled": true, "value": true}
		case "--compressed", "--silent", "-s", "-S", "--show-error", "-v", "--verbose", "--globoff", "-g", "--form-escape":
		default:
			if strings.HasPrefix(arg, "-") {
				return nil, fmt.Errorf("cURL option %s is not supported by the importer", arg)
			}
			if _, err := url.ParseRequestURI(arg); err == nil {
				m["url"] = arg
			} else {
				return nil, fmt.Errorf("invalid cURL argument %q", arg)
			}
		}
	}
	if str(m, "url") == "" {
		return nil, errors.New("cURL command has no URL")
	}
	m["headers"] = headers
	for _, h := range objects(headers) {
		if enabled(h) && strings.EqualFold(str(h, "name"), "Content-Type") && str(m, "bodyType") != "binary" && str(m, "bodyType") != "multipart/form-data" {
			media := openAPIMediaType(str(h, "value"))
			switch {
			case strings.Contains(media, "json"):
				m["bodyType"] = "application/json"
			case strings.Contains(media, "xml"):
				m["bodyType"] = "application/xml"
			case media == "application/x-www-form-urlencoded":
				values, err := url.ParseQuery(str(obj(m, "body"), "text"))
				if err != nil {
					return nil, err
				}
				form := []any{}
				for name, vs := range values {
					for _, value := range vs {
						form = append(form, Object{"name": name, "value": value, "enabled": true})
					}
				}
				m["bodyType"], m["body"] = media, Object{"form": form}
			default:
				m["bodyType"] = "text/plain"
			}
		}
		if str(m, "authenticationType") == "awsv4" && strings.EqualFold(str(h, "name"), "X-Amz-Security-Token") {
			obj(m, "authentication")["sessionToken"] = h["value"]
		}
	}
	return m, nil
}
func parseCurlForm(input string, literal bool) (Object, error) {
	name, text, found := strings.Cut(input, "=")
	if !found {
		return nil, errors.New("cURL form fields need name=value")
	}
	field := Object{"name": name, "enabled": true}
	if literal {
		field["value"] = text
		return field, nil
	}
	file := strings.HasPrefix(text, "@")
	if file {
		text = strings.TrimPrefix(text, "@")
	}
	if strings.HasPrefix(text, "<") {
		return nil, errors.New("file-backed text fields need an fs.readFile template")
	}
	value, rest, err := curlFormWord(text)
	if err != nil {
		return nil, err
	}
	if file {
		field["type"], field["file"] = "file", value
	} else {
		field["value"] = value
	}
	for rest != "" {
		rest = strings.TrimPrefix(rest, ";")
		key, after, exists := strings.Cut(rest, "=")
		if !exists {
			return nil, errors.New("invalid cURL form attribute")
		}
		key = strings.TrimSpace(key)
		value, tail, err := curlFormWord(after)
		if err != nil {
			return nil, err
		}
		switch key {
		case "filename":
			field["filename"] = value
		case "type":
			field["contentType"] = value
		case "headers", "encoder":
			return nil, fmt.Errorf("cURL multipart attribute %s is not supported", key)
		default:
			if str(field, "contentType") != "" {
				field["contentType"] = str(field, "contentType") + "; " + key + "=" + value
			} else {
				return nil, fmt.Errorf("unknown cURL form attribute %s", key)
			}
		}
		rest = tail
	}
	return field, nil
}
func curlFormWord(text string) (string, string, error) {
	if !strings.HasPrefix(text, "\"") {
		value, rest, _ := strings.Cut(text, ";")
		return value, rest, nil
	}
	var value strings.Builder
	for i := 1; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) && (text[i+1] == '\\' || text[i+1] == '"') {
			i++
			value.WriteByte(text[i])
			continue
		}
		if text[i] == '"' {
			rest := strings.TrimSpace(text[i+1:])
			if rest != "" && !strings.HasPrefix(rest, ";") {
				return "", "", errors.New("unexpected text after quoted cURL form value")
			}
			return value.String(), rest, nil
		}
		value.WriteByte(text[i])
	}
	return "", "", errors.New("cURL form value has an unfinished quote")
}
func splitShell(s string) ([]string, error) {
	args := []string{}
	var b strings.Builder
	quote := rune(0)
	escaped := false
	started := false
	for i, r := range s {
		if escaped {
			escaped = false
			if r == '\n' {
				continue
			}
			b.WriteRune(r)
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			if quote == '"' && i+1 < len(s) && !strings.ContainsRune("$`\"\\\n", rune(s[i+1])) {
				b.WriteRune(r)
				continue
			}
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if unicode.IsSpace(r) {
			if started {
				args = append(args, b.String())
				b.Reset()
				started = false
			}
			continue
		}
		b.WriteRune(r)
		started = true
	}
	if quote != 0 || escaped {
		return nil, errors.New("cURL has an unfinished quote or escape")
	}
	if started {
		args = append(args, b.String())
	}
	return args, nil
}
