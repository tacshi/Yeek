package desktop

import (
	"regexp"
	"strings"
)

// completionPreset is a suggestion for a whole field: label is shown, and
// value, when set, is inserted instead, as Yaak's User-Agent presets do.
type completionPreset struct{ label, value, info string }

func (o completionPreset) text() string {
	if o.value != "" {
		return o.value
	}
	return o.label
}

// headerMinMatch is how many characters Yaak's header completions wait for
// unless asked for.
const headerMinMatch = 3

// headerValuePresets is Yaak's headerOptionsMap.
func headerValuePresets(name string) []completionPreset {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "content-type":
		return mimeTypeOptions
	case "accept":
		return append([]completionPreset{{label: "*/*"}}, mimeTypeOptions...)
	case "accept-encoding", "content-encoding":
		return encodingOptions
	case "connection":
		return connectionOptions
	case "accept-charset":
		return charsetOptions
	case "accept-language":
		return acceptLanguageOptions
	case "cache-control":
		return cacheControlOptions
	case "user-agent":
		return userAgentOptions
	case "pragma":
		return []completionPreset{{label: "no-cache"}}
	case "te":
		return []completionPreset{{label: "trailers"}, {label: "compress"}, {label: "deflate"}, {label: "gzip"}}
	case "dnt":
		return []completionPreset{{label: "1"}, {label: "0"}}
	case "upgrade-insecure-requests":
		return []completionPreset{{label: "1"}}
	case "x-requested-with":
		return []completionPreset{{label: "XMLHttpRequest"}}
	}
	return nil
}

// secretHeader is Yaak's valueType: values of these headers are obscured
// as passwords are.
func secretHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, word := range []string{"authorization", "api-key", "access-token", "auth", "secret", "token"} {
		if strings.Contains(name, word) {
			return true
		}
	}
	return name == "cookie" || name == "set-cookie"
}

var headerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9-_]+$`)

// validHeaderName is Yaak's validateHttpHeader, which takes template tags
// for valid text.
func validHeaderName(name string) bool {
	if name == "" {
		return true
	}
	r := []rune(name)
	var b strings.Builder
	at := 0
	for _, tag := range templateTags(name) {
		b.WriteString(string(r[at:tag.Start]))
		b.WriteString("123")
		at = tag.End
	}
	b.WriteString(string(r[at:]))
	return headerNamePattern.MatchString(b.String())
}

// genericOptions is Yaak's genericCompletion: the presets matching all of
// the field before the caret, once it is long enough or asked for, except
// one it already equals.
func genericOptions(presets []completionPreset, minMatch int, source string, caret int, explicit bool) []templateOption {
	r := []rune(source)
	typed := string(r[:max(0, min(caret, len(r)))])
	if len([]rune(typed)) < minMatch && !explicit {
		return nil
	}
	query := strings.ToLower(typed)
	var prefixed, contained []templateOption
	for _, o := range presets {
		label := strings.ToLower(o.label)
		if o.label == typed {
			continue
		}
		option := templateOption{name: o.label, apply: o.text(), info: o.info, generic: true, end: caret}
		switch {
		case strings.HasPrefix(label, query):
			prefixed = append(prefixed, option)
		case query != "" && strings.Contains(label, query):
			contained = append(contained, option)
		}
	}
	return append(prefixed, contained...)
}
