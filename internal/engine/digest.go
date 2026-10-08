package engine

import (
	"crypto/md5" // #nosec G501 -- required for HTTP Digest interoperability.
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// This file ports Yaak's auth-digest plugin (plugins/auth-digest/src).

// authChallenge is one challenge from a WWW-Authenticate header.
type authChallenge struct {
	Scheme string
	Params map[string]string
	// Token68 is the NTLM TlRMTVNT… form, which carries no parameters.
	Token68 string
}

type digestChallenge struct {
	Realm, Nonce string
	Opaque       *string
	Qop          []string
	// Algorithm is echoed back verbatim, so it keeps the server's spelling.
	Algorithm *string
	Stale     bool
	Userhash  bool
}

type digestOptions struct {
	Username, Password, Method, URI string
	Body                            *string
	Challenge                       digestChallenge
	Cnonce                          string
	Nc                              int
}

const digestToken = "[!#$%&'*+\\-.^_`|~0-9A-Za-z]+"

var (
	digestParamRE   = regexp.MustCompile(`^(` + digestToken + `)\s*=\s*([\s\S]*)$`)
	digestSchemeRE  = regexp.MustCompile(`^(` + digestToken + `)(?:\s+([\s\S]*))?$`)
	digestToken68RE = regexp.MustCompile(`^[A-Za-z0-9\-._~+/]+=*$`)
	digestUnescape  = regexp.MustCompile(`\\([\s\S])`)
)

var (
	digestAlgorithms = []string{"MD5", "MD5-sess", "SHA-256", "SHA-256-sess"}
	digestQops       = []string{"auth", "auth-int"}
)

// splitOnCommas splits on commas outside quoted strings. Challenges and
// their parameters are both comma-separated, so parseChallenges regroups
// the flat list.
func splitOnCommas(value string) []string {
	var parts []string
	var current strings.Builder
	quoted := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case quoted && c == '\\' && i+1 < len(value):
			current.WriteByte(c)
			i++
			current.WriteByte(value[i])
		case c == '"':
			quoted = !quoted
			current.WriteByte(c)
		case c == ',' && !quoted:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	parts = append(parts, current.String())
	result := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			result = append(result, p)
		}
	}
	return result
}

func unquoteDigest(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		return digestUnescape.ReplaceAllString(trimmed[1:len(trimmed)-1], "$1")
	}
	return trimmed
}

func parseChallenges(headerValues []string) []authChallenge {
	challenges := []authChallenge{}
	for _, headerValue := range headerValues {
		current := -1
		for _, part := range splitOnCommas(headerValue) {
			if param := digestParamRE.FindStringSubmatch(part); param != nil && current >= 0 {
				challenges[current].Params[strings.ToLower(param[1])] = unquoteDigest(param[2])
				continue
			}
			scheme := digestSchemeRE.FindStringSubmatch(part)
			if scheme == nil {
				continue
			}
			challenges = append(challenges, authChallenge{Scheme: scheme[1], Params: map[string]string{}})
			current = len(challenges) - 1
			rest := strings.TrimSpace(scheme[2])
			if rest == "" {
				continue
			}
			if digestToken68RE.MatchString(rest) {
				challenges[current].Token68 = rest
				continue
			}
			if first := digestParamRE.FindStringSubmatch(rest); first != nil {
				challenges[current].Params[strings.ToLower(first[1])] = unquoteDigest(first[2])
			}
		}
	}
	return challenges
}

func toDigestChallenge(params map[string]string) digestChallenge {
	c := digestChallenge{Realm: params["realm"], Nonce: params["nonce"], Stale: strings.EqualFold(params["stale"], "true"), Userhash: strings.EqualFold(params["userhash"], "true")}
	if v, ok := params["opaque"]; ok {
		c.Opaque = &v
	}
	if v, ok := params["algorithm"]; ok {
		c.Algorithm = &v
	}
	if qop, ok := params["qop"]; ok {
		for v := range strings.SplitSeq(qop, ",") {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
				c.Qop = append(c.Qop, v)
			}
		}
	}
	return c
}

// resolveDigestAlgorithm accepts MD5, MD5-sess, SHA-256 and SHA-256-sess,
// tolerating the SHA256 spelling some servers use.
func resolveDigestAlgorithm(algorithm *string) (newHash func() hash.Hash, sess bool, ok bool) {
	value := "md5"
	if algorithm != nil {
		value = strings.ToLower(strings.TrimSpace(*algorithm))
	}
	value, sess = strings.CutSuffix(value, "-sess")
	switch strings.ReplaceAll(value, "-", "") {
	case "md5":
		return md5.New, sess, true // #nosec G401 -- HTTP Digest supports legacy MD5 by protocol.
	case "sha256":
		return sha256.New, sess, true
	}
	return nil, false, false
}

func unsupportedDigestAlgorithm(algorithm *string) error {
	name := "MD5"
	if algorithm != nil {
		name = *algorithm
	}
	return fmt.Errorf("Unsupported Digest algorithm: %s. Supported algorithms are %s", name, strings.Join(digestAlgorithms, ", "))
}

func unsupportedDigestQop(qop []string) error {
	return fmt.Errorf("Unsupported Digest qop: %s. Supported values are %s", strings.Join(qop, ", "), strings.Join(digestQops, " and "))
}

// digestChallengeProblem reports anything that stops this challenge being
// answered, so selection can pass over it for the next one offered.
func digestChallengeProblem(c digestChallenge) error {
	if _, _, ok := resolveDigestAlgorithm(c.Algorithm); !ok {
		return unsupportedDigestAlgorithm(c.Algorithm)
	}
	if c.Nonce == "" {
		return errors.New(`Digest challenge is missing the required "nonce" parameter`)
	}
	if c.Qop != nil && !slices.ContainsFunc(c.Qop, func(q string) bool { return slices.Contains(digestQops, q) }) {
		return unsupportedDigestQop(c.Qop)
	}
	return nil
}

// selectDigestChallenge picks the first answerable challenge, since servers
// list them strongest-first (RFC 7616 §3.7).
func selectDigestChallenge(challenges []authChallenge, realm string) (digestChallenge, error) {
	var digests, offered []string
	var params []map[string]string
	for _, c := range challenges {
		offered = append(offered, c.Scheme)
		if strings.EqualFold(c.Scheme, "digest") {
			params = append(params, c.Params)
		}
	}
	if len(params) == 0 {
		if len(offered) == 0 {
			return digestChallenge{}, errors.New("Server did not offer Digest authentication (no WWW-Authenticate header in the response)")
		}
		return digestChallenge{}, fmt.Errorf("Server did not offer Digest authentication. It offered: %s", strings.Join(offered, ", "))
	}
	inRealm := params
	if realm != "" {
		inRealm = nil
		for _, p := range params {
			digests = append(digests, strconv.Quote(p["realm"]))
			if r, ok := p["realm"]; ok && r == realm {
				inRealm = append(inRealm, p)
			}
		}
	}
	if len(inRealm) == 0 {
		return digestChallenge{}, fmt.Errorf("Server did not offer a Digest realm named %q. It offered: %s", realm, strings.Join(digests, ", "))
	}
	for _, p := range inRealm {
		if c := toDigestChallenge(p); digestChallengeProblem(c) == nil {
			return c, nil
		}
	}
	return digestChallenge{}, digestChallengeProblem(toDigestChallenge(inRealm[0]))
}

// selectDigestQop prefers auth-int only when the body is in hand. A nil body
// is empty or wasn't handed over; when auth-int is all that's offered it is
// still used over the empty body.
func selectDigestQop(qop []string, body *string) (string, error) {
	if slices.Contains(qop, "auth-int") && (body != nil || !slices.Contains(qop, "auth")) {
		return "auth-int", nil
	}
	if slices.Contains(qop, "auth") {
		return "auth", nil
	}
	return "", unsupportedDigestQop(qop)
}

func quoteDigest(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// encodeDigestExtended is an RFC 5987 ext-value, for usernames a
// quoted-string can't carry. It matches JavaScript's encodeURIComponent
// with ', (, ) and * escaped too.
func encodeDigestExtended(value string) string {
	var b strings.Builder
	for _, c := range []byte(value) {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return "UTF-8''" + b.String()
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func buildDigestAuthorization(o digestOptions) (string, error) {
	// RFC 7616 §4 hashes credentials in Normalization Form C.
	username, password := norm.NFC.String(o.Username), norm.NFC.String(o.Password)
	c := o.Challenge
	newHash, sess, ok := resolveDigestAlgorithm(c.Algorithm)
	if !ok {
		return "", unsupportedDigestAlgorithm(c.Algorithm)
	}
	h := func(value string) string {
		sum := newHash()
		_, _ = io.WriteString(sum, value)
		return hex.EncodeToString(sum.Sum(nil))
	}
	qop := ""
	if c.Qop != nil {
		var err error
		if qop, err = selectDigestQop(c.Qop, o.Body); err != nil {
			return "", err
		}
	}
	nc := fmt.Sprintf("%08x", o.Nc)
	secret := h(username + ":" + c.Realm + ":" + password)
	ha1 := secret
	if sess {
		ha1 = h(secret + ":" + c.Nonce + ":" + o.Cnonce)
	}
	ha2 := h(o.Method + ":" + o.URI)
	if qop == "auth-int" {
		body := ""
		if o.Body != nil {
			body = *o.Body
		}
		ha2 = h(o.Method + ":" + o.URI + ":" + h(body))
	}
	// Without qop the server speaks RFC 2069, where the client contributes
	// nothing and must not send cnonce, nc or qop back.
	response := h(ha1 + ":" + c.Nonce + ":" + ha2)
	if qop != "" {
		response = h(ha1 + ":" + c.Nonce + ":" + nc + ":" + o.Cnonce + ":" + qop + ":" + ha2)
	}
	var params []string
	if isPrintableASCII(username) {
		params = append(params, "username="+quoteDigest(username))
	} else {
		params = append(params, "username*="+encodeDigestExtended(username))
	}
	params = append(params, "realm="+quoteDigest(c.Realm), "uri="+quoteDigest(o.URI))
	if c.Algorithm != nil {
		params = append(params, "algorithm="+*c.Algorithm)
	}
	params = append(params, "nonce="+quoteDigest(c.Nonce))
	if qop != "" {
		params = append(params, "nc="+nc, "cnonce="+quoteDigest(o.Cnonce), "qop="+qop)
	}
	params = append(params, "response="+quoteDigest(response))
	if c.Opaque != nil {
		params = append(params, "opaque="+quoteDigest(*c.Opaque))
	}
	if c.Userhash {
		params = append(params, "userhash=false")
	}
	return "Digest " + strings.Join(params, ", "), nil
}

var absoluteURLRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+\-.]*://`)

// digestRequestTarget is the origin-form request-target the digest covers.
func digestRequestTarget(raw string) (string, error) {
	if !absoluteURLRE.MatchString(raw) {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target, nil
}

// maxDigestBodyBytes matches Yaak's MAX_AUTH_BODY_BYTES: larger bodies,
// and streamed ones (files, multipart), aren't handed to authentication.
const maxDigestBodyBytes = 10 << 20

type digestTransport struct {
	base http.RoundTripper
	auth Object
	// signBody is set for in-memory bodies, which auth-int may hash.
	signBody bool
}

func (d digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Digest needs a server-issued nonce, so like Yaak the challenge is
	// provoked first. The probe carries only the method and URL: a cookie or
	// API key header could authorize the very operation it only asks about.
	probe, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := d.base.RoundTrip(probe)
	if err != nil {
		return nil, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	_ = res.Body.Close()
	challenge, err := selectDigestChallenge(parseChallenges(res.Header.Values("WWW-Authenticate")), str(d.auth, "realm"))
	if err != nil {
		return nil, err
	}
	var body *string
	if d.signBody && req.ContentLength >= 0 && req.ContentLength <= maxDigestBodyBytes {
		raw, err := requestBytes(req)
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 && utf8.Valid(raw) {
			text := string(raw)
			body = &text
		}
	}
	uri, err := digestRequestTarget(req.URL.String())
	if err != nil {
		return nil, err
	}
	value, err := buildDigestAuthorization(digestOptions{Username: str(d.auth, "username"), Password: str(d.auth, "password"), Method: req.Method, URI: uri, Body: body, Challenge: challenge, Cnonce: digestCnonce(), Nc: 1})
	if err != nil {
		return nil, err
	}
	signed := req.Clone(req.Context())
	signed.Header.Set("Authorization", value)
	return d.base.RoundTrip(signed)
}

func digestCnonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
