package engine

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file ports Yaak's importer-curl plugin (plugins/importer-curl/src).
// On top of Yaak's flags it reads the ones Yeek's own Copy as cURL writes
// (--json, --form-string, --data-binary @file, --aws-sigv4, --ntlm, -m, -k,
// -L, -I) so a copied command imports back unchanged.

var (
	curlDataFlags = []string{"d", "data", "data-raw", "data-urlencode", "data-binary", "data-ascii"}
	// Yeek's additions come after Yaak's list.
	curlSupportedFlags = append([]string{"cookie", "b", "d", "data", "data-ascii", "data-binary", "data-raw", "data-urlencode", "digest", "form", "F", "get", "G", "header", "H", "request", "X", "url", "url-query", "user", "u"},
		"json", "form-string", "max-time", "m", "insecure", "k", "location", "L", "head", "I", "aws-sigv4", "ntlm", "basic")
	curlBooleanFlags    = []string{"G", "get", "digest", "insecure", "k", "location", "L", "head", "I", "ntlm", "basic"}
	curlCommandRE       = regexp.MustCompile(`^\s*curl `)
	curlFlagRE          = regexp.MustCompile(`^-{1,2}[\w-]+`)
	curlNextFlagRE      = regexp.MustCompile(`^--?[a-zA-Z]`)
	curlContinuationRE  = regexp.MustCompile(`\\[ \t]*\r?\n`)
	curlBoundaryRE      = regexp.MustCompile(`(?i)boundary=([^\s;]+)`)
	curlDispositionRE   = regexp.MustCompile(`(?i)Content-Disposition:\s*form-data;\s*name="([^"]+)"(?:;\s*filename="([^"]+)")?`)
	curlPercentRunRE    = regexp.MustCompile(`(%[0-9A-Fa-f]{2})+`)
	curlGraphQLPathRE   = regexp.MustCompile(`\b(graphql|gql)\b`)
	curlGraphQLOpRE     = regexp.MustCompile(`^(query|mutation|subscription|fragment)\b`)
	curlGraphQLBlockRE  = regexp.MustCompile(`^\{[\s\S]*\}$`)
	curlGraphQLBracesRE = regexp.MustCompile(`\{[\s\S]*\}`)
)

// curlValueShortFlags are short flags that take a value, derived like
// Yaak's VALUE_SHORT_FLAGS.
var curlValueShortFlags = func() []string {
	var flags []string
	for _, name := range curlSupportedFlags {
		if len(name) == 1 && !slices.Contains(curlBooleanFlags, name) {
			flags = append(flags, name)
		}
	}
	return flags
}()

// expandShortFlags splits a short cluster the way curl reads it: one option
// per character until one takes a value, which swallows the rest. So -XPOST
// is -X POST but -fsSL is four flags.
func expandShortFlags(token string) []string {
	if !strings.HasPrefix(token, "-") || strings.HasPrefix(token, "--") || len(token) <= 2 {
		return []string{token}
	}
	var expanded []string
	for i := 1; i < len(token); i++ {
		name := token[i : i+1]
		expanded = append(expanded, "-"+name)
		if slices.Contains(curlValueShortFlags, name) {
			if value := token[i+1:]; value != "" {
				expanded = append(expanded, value)
			}
			break
		}
	}
	return expanded
}

// splitCurlCommands splits pasted text into shell commands on ;, newlines and
// CRLF outside quotes, after joining line continuations (tolerating spaces
// or tabs after the backslash).
func splitCurlCommands(raw string) []string {
	joined := curlContinuationRE.ReplaceAllString(raw, " ")
	escaped := func(i int) bool {
		n := 0
		for j := i - 1; j >= 0 && joined[j] == '\\'; j-- {
			n++
		}
		return n%2 != 0
	}
	var commands []string
	var current strings.Builder
	single, double, dollar := false, false, false
	flush := func() {
		if text := strings.TrimSpace(current.String()); text != "" {
			commands = append(commands, text)
		}
		current.Reset()
	}
	for i := 0; i < len(joined); i++ {
		ch := joined[i]
		var next byte
		if i+1 < len(joined) {
			next = joined[i+1]
		}
		switch {
		// Unlike Yaak, an opener must be unescaped: \' outside quotes is a
		// literal quote, as in the '\'' idiom Copy as cURL writes.
		case !double && !dollar && ch == '\'' && !single && !escaped(i):
			single = true
		case single && ch == '\'':
			single = false
		case !single && !dollar && ch == '"' && !double && !escaped(i):
			double = true
		case double && ch == '"' && !escaped(i):
			double = false
		case !single && !double && !dollar && ch == '$' && next == '\'' && !escaped(i):
			dollar = true
			current.WriteString("$'")
			i++
			continue
		case dollar && ch == '\'' && !escaped(i):
			dollar = false
		case !single && !double && !dollar && !escaped(i) && (ch == ';' || ch == '\n' || ch == '\r' && next == '\n'):
			if ch == '\r' {
				i++
			}
			flush()
			continue
		}
		current.WriteByte(ch)
	}
	flush()
	return commands
}

// ConvertCurl is Yaak's convertCurl: each curl command in the text becomes a
// request. Text that doesn't start with "curl " imports nothing.
func ConvertCurl(raw string) ([]Object, error) {
	if !curlCommandRE.MatchString(raw) {
		return nil, errors.New("paste a command beginning with curl")
	}
	var requests []Object
	for _, command := range splitCurlCommands(raw) {
		tokens, err := splitShell(command)
		if err != nil {
			return nil, err
		}
		var args []string
		for _, token := range tokens {
			args = append(args, expandShortFlags(token)...)
		}
		if len(args) == 0 || args[0] != "curl" {
			continue
		}
		request, err := importCurlCommand(args)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// ParseCurl imports the first request of a cURL command.
func ParseCurl(command string) (Object, error) {
	requests, err := ConvertCurl(command)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, errors.New("paste a command beginning with curl")
	}
	return requests[0], nil
}

type curlFlags map[string][]any

type curlFlag struct {
	name  string
	value any
}

func (f curlFlags) strings(names ...string) []string {
	var values []string
	for _, name := range names {
		for _, v := range f[name] {
			if s, ok := v.(string); ok {
				values = append(values, s)
			}
		}
	}
	return values
}

// first is Yaak's getPairValue: the first value of the first name given.
func (f curlFlags) first(names ...string) (any, bool) {
	for _, name := range names {
		if len(f[name]) > 0 {
			return f[name][0], true
		}
	}
	return nil, false
}

func (f curlFlags) firstString(fallback string, names ...string) string {
	if v, ok := f.first(names...); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

func (f curlFlags) has(names ...string) bool {
	_, ok := f.first(names...)
	return ok
}

func splitOnce(s, sep string) (string, string, bool) { return strings.Cut(s, sep) }

func curlRow(name, value string) Object { return Object{"name": name, "value": value, "enabled": true} }

// decodePercentEncoding decodes a form value, keeping it when it isn't valid
// percent-encoding (curl sends a -d value verbatim, so 100% is fine). Each
// valid run decodes on its own so 50%25 and 100% becomes "50% and 100%".
func decodePercentEncoding(text string) string {
	if decoded, err := url.PathUnescape(text); err == nil {
		return decoded
	}
	return curlPercentRunRE.ReplaceAllStringFunc(text, func(run string) string {
		if decoded, err := url.PathUnescape(run); err == nil && utf8.ValidString(decoded) {
			return decoded
		}
		return run
	})
}

func importCurlCommand(entries []string) (Object, error) {
	flags := curlFlags{}
	var ordered []curlFlag
	var singletons []string
	for i := 1; i < len(entries); i++ {
		entry := strings.TrimSpace(entries[i])
		if curlFlagRE.MatchString(entry) {
			singleDash := entry[0] == '-' && entry[1] != '-'
			name := strings.TrimPrefix(strings.TrimPrefix(entry, "-"), "-")
			// curl also accepts --name=value.
			var value any
			if k, v, ok := strings.Cut(name, "="); ok && !singleDash {
				name, value = k, v
			}
			if !slices.Contains(curlSupportedFlags, name) {
				continue
			}
			if value == nil {
				hasValue := !slices.Contains(curlBooleanFlags, name)
				nextIsFlag := i+1 < len(entries) && curlNextFlagRE.MatchString(entries[i+1])
				switch {
				case singleDash && len(name) > 1:
					name, value = name[:1], name[1:]
				case i+1 < len(entries) && hasValue && !nextIsFlag:
					value = entries[i+1]
					i++
				default:
					value = true
				}
			}
			flags[name] = append(flags[name], value)
			ordered = append(ordered, curlFlag{name, value})
		} else if entry != "" {
			singletons = append(singletons, entry)
		}
	}

	// URL and query parameters
	urlArg := ""
	if len(singletons) > 0 {
		urlArg = singletons[0]
	}
	urlArg = flags.firstString(urlArg, "url")
	base, search, hasSearch := splitOnce(urlArg, "?")
	parameters := []any{}
	if hasSearch {
		for p := range strings.SplitSeq(search, "&") {
			name, value, _ := splitOnce(p, "=")
			parameters = append(parameters, curlRow(decodePercentEncoding(name), decodePercentEncoding(value)))
		}
	}
	for _, p := range flags.strings("url-query") {
		name, value, _ := splitOnce(p, "=")
		parameters = append(parameters, curlRow(name, value))
	}

	// Authentication from -u
	username, password, _ := splitOnce(flags.firstString("", "u", "user"), ":")
	authType := any(nil)
	auth := Object{}
	if username != "" {
		authType = "basic"
		if flags.has("digest") {
			authType = "digest"
		}
		auth = Object{"username": strings.TrimSpace(username), "password": strings.TrimSpace(password)}
	}

	// Headers
	headers := []any{}
	for _, header := range flags.strings("header", "H") {
		name, value, hasColon := splitOnce(header, ":")
		if strings.TrimSpace(value) == "" {
			row := curlRow(strings.TrimSuffix(strings.TrimSpace(name), ";"), "")
			// Yeek: curl's "Name:" removes a header it would add, which is
			// how Copy as cURL writes a disabled header.
			if hasColon && name != "" && !strings.HasSuffix(strings.TrimSpace(name), ";") {
				row["enabled"] = false
			}
			headers = append(headers, row)
			continue
		}
		headers = append(headers, curlRow(strings.TrimSpace(name), strings.TrimSpace(value)))
	}

	// Cookies
	var cookies []string
	for _, cookie := range flags.strings("cookie", "b") {
		// A value without "=" names a cookie file, which can't become a header.
		if name, value, ok := splitOnce(cookie, "="); ok {
			cookies = append(cookies, name+"="+value)
		}
	}
	if value := strings.Join(cookies, "; "); value != "" {
		if i := slices.IndexFunc(headers, func(h any) bool { return strings.EqualFold(str(h.(Object), "name"), "cookie") }); i >= 0 {
			h := headers[i].(Object)
			h["value"] = str(h, "value") + "; " + value
		} else {
			headers = append(headers, curlRow("Cookie", value))
		}
	}

	// An Authorization header wins over -u.
	if extractedType, extracted, filtered := extractCurlAuthorization(headers); extractedType != "" {
		authType, auth, headers = extractedType, extracted, filtered
	}

	// Body
	var contentType Object
	for _, h := range objects(headers) {
		if strings.EqualFold(str(h, "name"), "content-type") {
			contentType = h
			break
		}
	}
	mimeType := ""
	if contentType != nil {
		mimeType, _, _ = strings.Cut(str(contentType, "value"), ";")
		mimeType = strings.TrimSpace(mimeType)
	}
	boundary := ""
	if contentType != nil {
		if m := curlBoundaryRE.FindStringSubmatch(str(contentType, "value")); m != nil {
			boundary = m[1]
		}
	}
	rawData := flags.strings("data-raw", "d", "data", "data-binary", "data-ascii")
	var multipartFromRaw []any
	if mimeType == "multipart/form-data" && boundary != "" && len(rawData) > 0 {
		multipartFromRaw = parseCurlMultipart(strings.Join(rawData, ""), boundary)
	}
	dataParameters := curlDataParameters(flags)
	formFields := []any{}
	// Unlike Yaak (which lists --form before -F), fields keep command order,
	// since Copy as cURL interleaves --form and --form-string.
	for _, flag := range ordered {
		input, ok := flag.value.(string)
		if !ok || !slices.Contains([]string{"form", "F", "form-string"}, flag.name) {
			continue
		}
		field, err := parseCurlForm(input, flag.name == "form-string")
		if err != nil {
			// Yaak takes anything after the first "=" as the value.
			key, value, _ := splitOnce(input, "=")
			field = Object{"name": key, "enabled": true}
			if file, ok := strings.CutPrefix(value, "@"); ok {
				field["file"] = file
			} else {
				field["value"] = value
			}
		}
		formFields = append(formFields, field)
	}
	jsonData := flags.strings("json")

	body := Object{}
	bodyType := any(nil)
	asGET := flags.has("G", "get")
	hasDataBody := len(dataParameters) > 0 && !asGET
	hasFormBody := multipartFromRaw != nil || len(formFields) > 0
	textBody := func(text string, mime string) {
		if graphql := parseGraphQLJSONBody(mime, text, base); graphql != nil {
			bodyType, body = "graphql", graphql
			return
		}
		switch mime {
		case "application/json", "text/xml", "text/plain":
			bodyType = mime
		default:
			// Yaak's "other" body type is Yeek's text/plain.
			bodyType = "text/plain"
		}
		body = Object{"text": text}
	}
	switch {
	case multipartFromRaw != nil:
		bodyType, body = "multipart/form-data", Object{"form": multipartFromRaw}
	case len(dataParameters) > 0 && asGET:
		for _, p := range dataParameters {
			parameters = append(parameters, curlRow(decodePercentEncoding(str(p, "name")), decodePercentEncoding(str(p, "value"))))
		}
	case len(dataParameters) == 1 && str(dataParameters[0], "filePath") != "" && len(rawData) == 1 && !slices.Contains(flags.strings("data-raw"), rawData[0]):
		// Yeek: -d @file sends that file, which is a binary file body.
		bodyType, body = "binary", Object{"filePath": str(dataParameters[0], "filePath")}
	case len(dataParameters) > 0 && (mimeType == "" || mimeType == "application/x-www-form-urlencoded"):
		bodyType = cmpOrString(mimeType, "application/x-www-form-urlencoded")
		form := []any{}
		for _, p := range dataParameters {
			form = append(form, curlRow(decodePercentEncoding(str(p, "name")), decodePercentEncoding(str(p, "value"))))
		}
		body = Object{"form": form}
		headers = append(headers, curlRow("Content-Type", "application/x-www-form-urlencoded"))
	case len(dataParameters) > 0:
		var parts []string
		for _, p := range dataParameters {
			name, value := str(p, "name"), str(p, "value")
			if name != "" && value != "" {
				parts = append(parts, name+"="+value)
			} else {
				parts = append(parts, name+value)
			}
		}
		textBody(strings.Join(parts, "&"), mimeType)
	case len(formFields) > 0:
		bodyType = cmpOrString(mimeType, "multipart/form-data")
		body = Object{"form": formFields}
		if mimeType == "" {
			headers = append(headers, curlRow("Content-Type", "multipart/form-data"))
		}
	case len(jsonData) > 0 && !asGET:
		// Yeek: curl's --json is a JSON body.
		textBody(strings.Join(jsonData, ""), cmpOrString(mimeType, "application/json"))
		hasDataBody = true
	}

	// Method
	method := strings.ToUpper(flags.firstString("", "X", "request"))
	if method == "" {
		switch {
		case hasDataBody || hasFormBody || bodyType == "binary":
			method = "POST"
		case flags.has("I", "head"):
			method = "HEAD"
		default:
			method = "GET"
		}
	}

	m, _ := defaultModel("http_request")
	fields := Object{"name": "", "urlParameters": parameters, "url": base, "method": method, "headers": headers, "authentication": auth, "authenticationType": authType, "body": body, "bodyType": bodyType, "folderId": nil, "sortPriority": 0}
	maps.Copy(m, fields)
	if !hasSearch {
		m["url"] = urlArg
	}
	return m, applyCurlExtras(m, flags)
}

// applyCurlExtras reads the flags Yeek supports beyond Yaak's importer.
func applyCurlExtras(m Object, flags curlFlags) error {
	auth := obj(m, "authentication")
	if flags.has("ntlm") && str(m, "authenticationType") != "" {
		m["authenticationType"] = "windows"
	}
	if region := flags.firstString("", "aws-sigv4"); region != "" {
		fields := strings.Split(region, ":")
		if len(fields) == 4 {
			aws := Object{"region": fields[2], "service": fields[3]}
			if str(m, "authenticationType") == "basic" {
				aws["accessKeyId"], aws["secretAccessKey"] = auth["username"], auth["password"]
			}
			for _, h := range objects(array(m, "headers")) {
				if strings.EqualFold(str(h, "name"), "X-Amz-Security-Token") {
					aws["sessionToken"] = h["value"]
				}
			}
			m["authenticationType"], m["authentication"] = "awsv4", aws
		}
	}
	if v := flags.firstString("", "m", "max-time"); v != "" {
		if seconds, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(seconds) && seconds >= 0 && seconds <= 86400 {
			m["settingRequestTimeout"] = Object{"enabled": true, "value": seconds * 1000}
		}
	}
	if flags.has("k", "insecure") {
		m["settingValidateCertificates"] = Object{"enabled": true, "value": false}
	}
	if flags.has("L", "location") {
		m["settingFollowRedirects"] = Object{"enabled": true, "value": true}
	}
	return nil
}

func cmpOrString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// extractCurlAuthorization turns a Bearer or Basic Authorization header into
// the request's authentication, dropping the header.
func extractCurlAuthorization(headers []any) (string, Object, []any) {
	i := slices.IndexFunc(headers, func(h any) bool { return strings.EqualFold(str(h.(Object), "name"), "authorization") })
	if i < 0 {
		return "", nil, headers
	}
	value := strings.TrimSpace(str(headers[i].(Object), "value"))
	space := strings.Index(value, " ")
	if space <= 0 {
		return "", nil, headers
	}
	scheme, credentials := strings.ToLower(value[:space]), strings.TrimSpace(value[space+1:])
	filtered := slices.Delete(slices.Clone(headers), i, i+1)
	switch scheme {
	case "bearer":
		return "bearer", Object{"token": credentials, "prefix": "Bearer"}, filtered
	case "basic":
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err := encoding.DecodeString(credentials)
			if err != nil {
				continue
			}
			if user, password, ok := strings.Cut(string(decoded), ":"); ok && user != "" {
				return "basic", Object{"username": user, "password": password}, filtered
			}
			break
		}
	}
	return "", nil, headers
}

// curlDataParameters is Yaak's pairsToDataParameters. -d content is
// &-separated, but --data-urlencode encodes its whole argument.
func curlDataParameters(flags curlFlags) []Object {
	var parameters []Object
	for _, flag := range curlDataFlags {
		for _, p := range flags.strings(flag) {
			params := []string{p}
			if flag != "data-urlencode" {
				params = strings.Split(p, "&")
			}
			for _, param := range params {
				name, value, _ := splitOnce(param, "=")
				if file, ok := strings.CutPrefix(param, "@"); ok {
					parameters = append(parameters, Object{"name": name, "value": "", "filePath": file, "enabled": true})
					continue
				}
				if flag == "data-urlencode" {
					value = encodeURIComponent(value)
				}
				parameters = append(parameters, Object{"name": name, "value": value, "enabled": true})
			}
		}
	}
	return parameters
}

// parseCurlMultipart reads a multipart body Chrome DevTools puts in
// --data-raw.
func parseCurlMultipart(raw, boundary string) []any {
	var fields []any
	for part := range strings.SplitSeq(raw, "--"+boundary) {
		if part == "" || strings.TrimSpace(part) == "--" {
			continue
		}
		split := strings.Index(part, "\r\n\r\n")
		if split < 0 {
			continue
		}
		content := strings.TrimSuffix(part[split+4:], "\r\n")
		m := curlDispositionRE.FindStringSubmatch(part[:split])
		if m == nil {
			continue
		}
		field := Object{"name": m[1], "enabled": true}
		if m[2] != "" {
			field["file"] = m[2]
		} else {
			field["value"] = content
		}
		fields = append(fields, field)
	}
	return fields
}

// parseGraphQLJSONBody is Yaak's GraphQL detection for a JSON body: a
// query/variables/operationName envelope that scores as a GraphQL document.
func parseGraphQLJSONBody(mimeType, text, rawURL string) Object {
	if mimeType != "application/json" {
		return nil
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal([]byte(text), &members); err != nil || members == nil {
		return nil
	}
	var query string
	if raw, ok := members["query"]; !ok || json.Unmarshal(raw, &query) != nil {
		return nil
	}
	for key := range members {
		if key != "query" && key != "variables" && key != "operationName" {
			return nil
		}
	}
	path := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Scheme != "" {
		path = u.Path
	}
	score, document := 0, false
	if curlGraphQLPathRE.MatchString(strings.ToLower(path)) {
		score += 2
	}
	trimmed := strings.TrimSpace(query)
	if curlGraphQLOpRE.MatchString(trimmed) {
		score += 3
	} else if curlGraphQLBlockRE.MatchString(trimmed) {
		score, document = score+3, true
	}
	if curlGraphQLBracesRE.MatchString(query) {
		score, document = score+1, true
	}
	var operation string
	operationIsString := members["operationName"] != nil && json.Unmarshal(members["operationName"], &operation) == nil
	if operationIsString && strings.TrimSpace(operation) != "" {
		score++
	}
	variables, hasVariables := members["variables"]
	if hasVariables {
		switch variables.Kind() {
		case '{', '[', '"':
			score++
		}
	}
	if !document || score < 4 {
		return nil
	}
	result := Object{"query": query}
	if hasVariables && variables.Kind() != 'n' {
		var s string
		if json.Unmarshal(variables, &s) == nil {
			result["variables"] = s
		} else {
			pretty := slices.Clone(variables)
			_ = pretty.Indent(jsontext.WithIndent("  "))
			result["variables"] = string(pretty)
		}
	}
	if operationIsString {
		result["operationName"] = operation
	}
	return result
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
		field["file"] = value
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

// splitShell tokenizes one command like a POSIX shell (via Yaak's shlex):
// '...' is literal, "..." takes backslash escapes for $ ` " \ and newline,
// $'...' takes ANSI-C escapes, and a backslash outside quotes escapes the
// next character.
func splitShell(s string) ([]string, error) {
	args := []string{}
	var b strings.Builder
	started := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			if i+1 >= len(s) {
				return nil, errors.New("cURL has an unfinished quote or escape")
			}
			i++
			if s[i] != '\n' {
				b.WriteByte(s[i])
			}
			started = true
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("cURL has an unfinished quote or escape")
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 1
			started = true
		case c == '$' && i+1 < len(s) && s[i+1] == '\'':
			n, err := readANSIQuoted(s[i+2:], &b)
			if err != nil {
				return nil, err
			}
			i += n + 1
			started = true
		case c == '"' || c == '$' && i+1 < len(s) && s[i+1] == '"':
			if c == '$' {
				i++
			}
			closed := false
			for i++; i < len(s); i++ {
				if s[i] == '"' {
					closed = true
					break
				}
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
					if s[i] != '\n' {
						b.WriteByte(s[i])
					}
					continue
				}
				b.WriteByte(s[i])
			}
			if !closed {
				return nil, errors.New("cURL has an unfinished quote or escape")
			}
			started = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if started {
				args = append(args, b.String())
				b.Reset()
				started = false
			}
		default:
			b.WriteByte(c)
			started = true
		}
	}
	if started {
		args = append(args, b.String())
	}
	return args, nil
}

// readANSIQuoted reads the body of $'...' up to its closing quote, writing
// the decoded text, and returns how many bytes it consumed (including the
// quote).
func readANSIQuoted(s string, b *strings.Builder) (int, error) {
	simple := map[byte]byte{'a': '\a', 'b': '\b', 'e': 0x1b, 'E': 0x1b, 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"', '?': '?'}
	hexDigits := func(from, max int) (uint64, int) {
		end := from
		for end < len(s) && end-from < max && strings.IndexByte("0123456789abcdefABCDEF", s[end]) >= 0 {
			end++
		}
		if end == from {
			return 0, 0
		}
		v, _ := strconv.ParseUint(s[from:end], 16, 32)
		return v, end - from
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			return i + 1, nil
		}
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		i++
		e := s[i]
		if r, ok := simple[e]; ok {
			b.WriteByte(r)
			continue
		}
		switch e {
		case 'x':
			if v, n := hexDigits(i+1, 2); n > 0 {
				b.WriteByte(byte(v))
				i += n
				continue
			}
		case 'u', 'U':
			max := 4
			if e == 'U' {
				max = 8
			}
			if v, n := hexDigits(i+1, max); n > 0 {
				b.WriteRune(rune(v))
				i += n
				continue
			}
		case 'c':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i] & 0x1f)
				continue
			}
		}
		if e >= '0' && e <= '7' {
			end := i
			for end < len(s) && end-i < 3 && s[end] >= '0' && s[end] <= '7' {
				end++
			}
			v, _ := strconv.ParseUint(s[i:end], 8, 16)
			b.WriteByte(byte(v))
			i = end - 1
			continue
		}
		b.WriteByte('\\')
		b.WriteByte(e)
	}
	return 0, errors.New("cURL has an unfinished quote or escape")
}
