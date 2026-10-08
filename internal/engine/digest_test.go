package engine

import (
	"cmp"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Ported from Yaak's plugins/auth-digest/tests.

func mustChallenge(t *testing.T, headers []string, realm string) digestChallenge {
	t.Helper()
	c, err := selectDigestChallenge(parseChallenges(headers), realm)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustDigest(t *testing.T, o digestOptions) string {
	t.Helper()
	v, err := buildDigestAuthorization(o)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func paramOf(header, name string) string { return parseChallenges([]string{header})[0].Params[name] }

func TestDigestParseChallenges(t *testing.T) {
	if got := parseChallenges([]string{`Digest realm="test", algorithm=MD5, stale=TRUE`}); !reflect.DeepEqual(got, []authChallenge{{Scheme: "Digest", Params: map[string]string{"realm": "test", "algorithm": "MD5", "stale": "TRUE"}}}) {
		t.Errorf("quoted and unquoted: %+v", got)
	}
	header := `Digest qop="auth,auth-int", realm="a \"quoted\" realm"`
	if paramOf(header, "qop") != "auth,auth-int" || paramOf(header, "realm") != `a "quoted" realm` {
		t.Error("commas and escapes inside quoted values")
	}
	if paramOf(`Digest realm = "test"`, "realm") != "test" {
		t.Error("whitespace around equals")
	}
	if got := parseChallenges([]string{`Basic realm="a", Digest realm="b", nonce="n"`}); !reflect.DeepEqual(got, []authChallenge{{Scheme: "Basic", Params: map[string]string{"realm": "a"}}, {Scheme: "Digest", Params: map[string]string{"realm": "b", "nonce": "n"}}}) {
		t.Errorf("multiple challenges in one header: %+v", got)
	}
	if got := parseChallenges([]string{`Digest realm="a"`, "Negotiate"}); len(got) != 2 || got[0].Scheme != "Digest" || got[1].Scheme != "Negotiate" {
		t.Errorf("repeated headers: %+v", got)
	}
	if got := parseChallenges([]string{"NTLM TlRMTVNTUAACAAAAAA=="}); !reflect.DeepEqual(got, []authChallenge{{Scheme: "NTLM", Params: map[string]string{}, Token68: "TlRMTVNTUAACAAAAAA=="}}) {
		t.Errorf("token68: %+v", got)
	}
	if paramOf(`Digest Realm="test", NONCE="n"`, "realm") != "test" || paramOf(`Digest Realm="test", NONCE="n"`, "nonce") != "n" {
		t.Error("lower-cases parameter names")
	}
}

func TestDigestToChallenge(t *testing.T) {
	c := toDigestChallenge(parseChallenges([]string{`Digest realm="r", nonce="n", qop=" auth , AUTH-INT ", stale=true`})[0].Params)
	if !reflect.DeepEqual(c.Qop, []string{"auth", "auth-int"}) || !c.Stale || c.Userhash {
		t.Errorf("qop and flags: %+v", c)
	}
	if c = toDigestChallenge(parseChallenges([]string{`Digest realm="r", nonce="n"`})[0].Params); c.Qop != nil {
		t.Error("missing qop should be absent")
	}
	if c = toDigestChallenge(parseChallenges([]string{`Digest realm="r", nonce="n", userhash=TRUE`})[0].Params); !c.Userhash {
		t.Error("userhash")
	}
}

func TestDigestSelectChallenge(t *testing.T) {
	md5c := `Digest realm="a", nonce="n1", algorithm=MD5`
	sha := `Digest realm="b", nonce="n2", algorithm=SHA-256`
	for name, c := range map[string]struct {
		headers []string
		realm   string
		nonce   string
	}{
		"first preferred":         {[]string{sha, md5c}, "", "n2"},
		"skips unsupported alg":   {[]string{`Digest realm="c", nonce="n0", algorithm=SHA-512-256`, md5c}, "", "n1"},
		"skips unanswerable qop":  {[]string{`Digest realm="c", nonce="n0", qop="auth-conf"`, md5c}, "", "n1"},
		"skips missing nonce":     {[]string{`Digest realm="c"`, md5c}, "", "n1"},
		"case-insensitive scheme": {[]string{`digest realm="a", nonce="n1"`}, "", "n1"},
		"selects by realm":        {[]string{sha, md5c}, "a", "n1"},
	} {
		if got := mustChallenge(t, c.headers, c.realm); got.Nonce != c.nonce {
			t.Errorf("%s: nonce %q", name, got.Nonce)
		}
	}
	for name, c := range map[string]struct {
		headers []string
		realm   string
		want    string
	}{
		"first problem when none answerable": {[]string{`Digest realm="c", nonce="n", qop="auth-conf"`, `Digest realm="d"`}, "", "Unsupported Digest qop: auth-conf"},
		"realm not offered":                  {[]string{sha, md5c}, "nope", `Server did not offer a Digest realm named "nope". It offered: "b", "a"`},
		"no digest":                          {[]string{`Basic realm="a"`, "Negotiate"}, "", "Server did not offer Digest authentication. It offered: Basic, Negotiate"},
		"no challenge":                       {nil, "", "no WWW-Authenticate header in the response"},
		"no supported algorithm":             {[]string{`Digest realm="c", nonce="n", algorithm=SHA-512-256`}, "", "Unsupported Digest algorithm: SHA-512-256"},
		"no nonce":                           {[]string{`Digest realm="c"`}, "", `Digest challenge is missing the required "nonce" parameter`},
	} {
		if _, err := selectDigestChallenge(parseChallenges(c.headers), c.realm); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDigestRFCWorkedExamples(t *testing.T) {
	// RFC 7616 §3.9.1
	headers := []string{
		`Digest realm="http-auth@example.org", qop="auth, auth-int", algorithm=SHA-256, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`,
		`Digest realm="http-auth@example.org", qop="auth, auth-int", algorithm=MD5, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`,
	}
	common := digestOptions{Username: "Mufasa", Password: "Circle of Life", Method: "GET", URI: "/dir/index.html", Cnonce: "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ", Nc: 1}
	sha := common
	sha.Challenge = mustChallenge(t, headers, "")
	if got := mustDigest(t, sha); got != `Digest username="Mufasa", realm="http-auth@example.org", uri="/dir/index.html", algorithm=SHA-256, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", nc=00000001, cnonce="f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ", qop=auth, response="753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"` {
		t.Errorf("SHA-256: %s", got)
	}
	md5o := common
	md5o.Challenge = mustChallenge(t, headers[1:], "")
	if got := mustDigest(t, md5o); got != `Digest username="Mufasa", realm="http-auth@example.org", uri="/dir/index.html", algorithm=MD5, nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", nc=00000001, cnonce="f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ", qop=auth, response="8ca523f5e9506fed4657c9700eebdbec", opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"` {
		t.Errorf("MD5: %s", got)
	}
	// RFC 2617 §3.5
	rfc2617 := digestOptions{Username: "Mufasa", Password: "Circle Of Life", Method: "GET", URI: "/dir/index.html", Cnonce: "0a4f113b", Nc: 1, Challenge: mustChallenge(t, []string{`Digest realm="testrealm@host.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", opaque="5ccc069c403ebaf9f0171e9517f40e41"`}, "")}
	if got := mustDigest(t, rfc2617); !strings.Contains(got, `response="6629fae49393a05397450978507c4ef1"`) {
		t.Errorf("RFC 2617: %s", got)
	}
}

func TestDigestBuildAuthorization(t *testing.T) {
	base := func(challenge string) digestOptions {
		return digestOptions{Username: "user", Password: "pass", Method: "POST", URI: "/api", Cnonce: "abc123", Nc: 1, Challenge: mustChallenge(t, []string{challenge}, "")}
	}
	got := mustDigest(t, base(`Digest realm="r", nonce="n"`))
	for _, absent := range []string{"qop=", "cnonce=", "nc=", "algorithm="} {
		if strings.Contains(got, absent) {
			t.Errorf("RFC 2069 header has %s: %s", absent, got)
		}
	}
	if !strings.Contains(got, `response="24644771b8983deed818b83aeb3ac381"`) {
		t.Errorf("RFC 2069 response: %s", got)
	}
	withBody := base(`Digest realm="r", nonce="n", qop="auth,auth-int"`)
	body := `{"a":1}`
	withBody.Body = &body
	if got = mustDigest(t, withBody); !strings.Contains(got, "qop=auth-int") {
		t.Errorf("auth-int over body: %s", got)
	}
	if got = mustDigest(t, base(`Digest realm="r", nonce="n", qop="auth,auth-int"`)); !strings.Contains(got, "qop=auth,") && !strings.HasSuffix(got, "qop=auth") {
		t.Errorf("falls back to auth: %s", got)
	}
	if got = mustDigest(t, base(`Digest realm="r", nonce="n", qop="auth-int"`)); !strings.Contains(got, "qop=auth-int") {
		t.Errorf("auth-int only: %s", got)
	}
	if _, err := buildDigestAuthorization(digestOptions{Challenge: digestChallenge{Nonce: "n", Qop: []string{"auth-conf"}}}); err == nil || !strings.Contains(err.Error(), "Unsupported Digest qop: auth-conf") {
		t.Errorf("auth-conf: %v", err)
	}
	sess := mustDigest(t, base(`Digest realm="r", nonce="n", qop=auth, algorithm=MD5-sess`))
	plain := mustDigest(t, base(`Digest realm="r", nonce="n", qop=auth, algorithm=MD5`))
	if !strings.Contains(sess, "algorithm=MD5-sess") || sess == plain {
		t.Errorf("-sess: %s", sess)
	}
	if got = mustDigest(t, base(`Digest realm="r", nonce="n", qop=auth, userhash=true`)); !strings.Contains(got, "userhash=false") {
		t.Errorf("userhash: %s", got)
	}
	quoted := base(`Digest realm="r", nonce="n"`)
	quoted.Username = `a"b`
	if got = mustDigest(t, quoted); !strings.Contains(got, `username="a\"b"`) {
		t.Errorf("escaping: %s", got)
	}
	decomposed, precomposed := base(`Digest realm="r", nonce="n"`), base(`Digest realm="r", nonce="n"`)
	decomposed.Username, decomposed.Password = "Jäsøn", "päss"
	precomposed.Username, precomposed.Password = "Jäsøn", "päss"
	if mustDigest(t, decomposed) != mustDigest(t, precomposed) {
		t.Error("NFC normalization")
	}
	extended := base(`Digest realm="r", nonce="n"`)
	extended.Username = "Jäsøn Doe"
	if got = mustDigest(t, extended); !strings.Contains(got, "username*=UTF-8''J%C3%A4s%C3%B8n%20Doe") {
		t.Errorf("RFC 5987: %s", got)
	}
}

func TestDigestRequestTarget(t *testing.T) {
	for in, want := range map[string]string{"https://example.org/dir/index.html?a=b&c=d": "/dir/index.html?a=b&c=d", "https://example.org": "/", "localhost:8080/thing": "/thing"} {
		if got, err := digestRequestTarget(in); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
}

type recordingTransport struct {
	requests  []*http.Request
	challenge []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, req)
	header := http.Header{}
	if len(r.requests) == 1 {
		for _, c := range r.challenge {
			header.Add("WWW-Authenticate", c)
		}
		return &http.Response{StatusCode: 401, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}
	return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
}

func digestApply(t *testing.T, auth Object, method, body string, headers http.Header, challenge ...string) (*recordingTransport, string, error) {
	t.Helper()
	base := &recordingTransport{challenge: challenge}
	req, _ := http.NewRequest(method, "https://example.org/dir/index.html?a=b", strings.NewReader(body))
	for k, v := range headers {
		req.Header[k] = v
	}
	res, err := digestTransport{base: base, auth: auth, signBody: true}.RoundTrip(req)
	if err != nil {
		return base, "", err
	}
	_ = res.Body.Close()
	return base, base.requests[len(base.requests)-1].Header.Get("Authorization"), nil
}

func TestDigestApply(t *testing.T) {
	creds := Object{"username": "user", "password": "pass"}
	qopAuth := `Digest realm="r", nonce="n", qop=auth`
	headers := http.Header{"Cookie": {"session=abc"}, "X-Api-Key": {"secret"}, "Authorization": {"Bearer stale"}}
	base, _, err := digestApply(t, creds, "DELETE", "hello", headers, qopAuth)
	if err != nil {
		t.Fatal(err)
	}
	probe := base.requests[0]
	if probe.Method != "DELETE" || probe.URL.String() != "https://example.org/dir/index.html?a=b" || len(probe.Header) != 0 || probe.Body != nil {
		t.Errorf("probe carried more than method and URL: %s %s %v %v", probe.Method, probe.URL, probe.Header, probe.Body)
	}
	if sent := base.requests[1]; sent.Header.Get("Cookie") != "session=abc" || sent.Header.Get("X-Api-Key") != "secret" {
		t.Errorf("signed request lost headers: %v", sent.Header)
	}
	_, first, _ := digestApply(t, creds, "GET", "", nil, qopAuth)
	_, second, _ := digestApply(t, creds, "GET", "", nil, qopAuth)
	if !strings.Contains(first, `uri="/dir/index.html?a=b"`) || first == second {
		t.Errorf("request-target or fresh cnonce: %s / %s", first, second)
	}
	if _, value, _ := digestApply(t, Object{}, "GET", "", nil, `Digest realm="r", nonce="n"`); !strings.Contains(value, `username=""`) {
		t.Errorf("missing credentials: %s", value)
	}
	if _, _, err = digestApply(t, creds, "GET", "", nil, `Basic realm="r"`); err == nil || !strings.Contains(err.Error(), "Server did not offer Digest authentication. It offered: Basic") {
		t.Errorf("no digest: %v", err)
	}
	if _, value, _ := digestApply(t, Object{"username": "user", "password": "pass", "realm": "two"}, "GET", "", nil, `Digest realm="one", nonce="n1"`, `Digest realm="two", nonce="n2"`); !strings.Contains(value, `nonce="n2"`) {
		t.Errorf("configured realm: %s", value)
	}
}

// parseDigestCredentials reads the Authorization header independently of the
// code under test, like Yaak's test server.
func parseDigestCredentials(header string) map[string]string {
	params := map[string]string{}
	i := strings.Index(header, " ") + 1
	nextComma := func(from int) int {
		if j := strings.Index(header[from:], ","); j >= 0 {
			return from + j
		}
		return len(header)
	}
	for i < len(header) {
		eq := strings.Index(header[i:], "=")
		if eq < 0 {
			break
		}
		name := strings.ToLower(strings.TrimSpace(header[i : i+eq]))
		i += eq + 1
		value := ""
		if i < len(header) && header[i] == '"' {
			for i++; i < len(header) && header[i] != '"'; i++ {
				if header[i] == '\\' {
					i++
				}
				value += string(header[i])
			}
			i++
		} else {
			end := nextComma(i)
			value = strings.TrimSpace(header[i:end])
			i = end
		}
		params[name] = value
		if i >= len(header) {
			break
		}
		i = nextComma(i) + 1
	}
	return params
}

func startDigestServer(t *testing.T, username, password, realm, nonce, algorithm, qop, decoyRealm string) string {
	t.Helper()
	newHash := md5.New
	if strings.HasPrefix(strings.ToLower(algorithm), "sha-256") {
		newHash = sha256.New
	}
	h := func(v string) string {
		var sum hash.Hash = newHash()
		sum.Write([]byte(v))
		return hex.EncodeToString(sum.Sum(nil))
	}
	sess := strings.HasSuffix(strings.ToLower(algorithm), "-sess")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "Digest ") {
			parts := []string{`Digest realm="` + realm + `"`, `nonce="` + nonce + `"`, "algorithm=" + cmp.Or(algorithm, "MD5")}
			if qop != "" {
				parts = append(parts, `qop="`+qop+`"`)
			}
			parts = append(parts, `opaque="0p4qu3"`)
			if decoyRealm != "" {
				w.Header().Add("WWW-Authenticate", `Digest realm="`+decoyRealm+`", nonce="wrong-nonce", algorithm=MD5`)
			}
			w.Header().Add("WWW-Authenticate", strings.Join(parts, ", "))
			w.WriteHeader(401)
			_, _ = io.WriteString(w, "unauthorized")
			return
		}
		p := parseDigestCredentials(authorization)
		secret := h(username + ":" + realm + ":" + password)
		ha1 := secret
		if sess {
			ha1 = h(secret + ":" + p["nonce"] + ":" + p["cnonce"])
		}
		ha2 := h(r.Method + ":" + p["uri"])
		if p["qop"] == "auth-int" {
			ha2 = h(r.Method + ":" + p["uri"] + ":" + h(string(body)))
		}
		expected := h(ha1 + ":" + p["nonce"] + ":" + ha2)
		if _, ok := p["qop"]; ok {
			expected = h(ha1 + ":" + p["nonce"] + ":" + p["nc"] + ":" + p["cnonce"] + ":" + p["qop"] + ":" + ha2)
		}
		if p["username"] == username && p["realm"] == realm && p["nonce"] == nonce && p["uri"] == r.URL.RequestURI() && p["opaque"] == "0p4qu3" && p["response"] == expected {
			_, _ = io.WriteString(w, "welcome")
			return
		}
		w.WriteHeader(401)
		_, _ = io.WriteString(w, "denied")
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestDigestAgainstLiveServer(t *testing.T) {
	send := func(t *testing.T, auth Object, method, url, body string) (int, string) {
		t.Helper()
		e := testEngine(t)
		w := saveTest(t, e, Object{"model": "workspace", "name": "Digest"})
		model := Object{"model": "http_request", "workspaceId": str(w, "id"), "method": method, "url": url, "authenticationType": "digest", "authentication": auth}
		if body != "" {
			model["bodyType"], model["body"] = "application/json", Object{"text": body}
		}
		request := saveTest(t, e, model)
		response, err := e.SendHTTP(t.Context(), str(request, "id"), SendOptions{})
		if err != nil {
			t.Fatal(err)
		}
		text, _ := e.Body(str(response, "id"))
		return int(number(response, "status")), string(text)
	}
	for _, algorithm := range []string{"MD5", "MD5-sess", "SHA-256", "SHA-256-sess"} {
		t.Run(algorithm, func(t *testing.T) {
			url := startDigestServer(t, "Mufasa", "Circle of Life", "http-auth@example.org", "7ypf/xlj9XXwfDPEoM4URrv", algorithm, "auth", "")
			if status, body := send(t, Object{"username": "Mufasa", "password": "Circle of Life"}, "GET", url+"/dir/index.html?a=b", ""); status != 200 || body != "welcome" {
				t.Fatal(status, body)
			}
		})
	}
	t.Run("auth-int POST body", func(t *testing.T) {
		url := startDigestServer(t, "user", "pass", "api@example.org", "n0nc3", "SHA-256", "auth,auth-int", "")
		if status, body := send(t, Object{"username": "user", "password": "pass"}, "POST", url+"/submit", `{"hello":"world"}`); status != 200 || body != "welcome" {
			t.Fatal(status, body)
		}
	})
	t.Run("RFC 2069 without qop", func(t *testing.T) {
		url := startDigestServer(t, "user", "pass", "legacy@example.org", "old-nonce", "", "", "")
		if status, body := send(t, Object{"username": "user", "password": "pass"}, "GET", url+"/legacy", ""); status != 200 || body != "welcome" {
			t.Fatal(status, body)
		}
	})
	t.Run("configured realm among several", func(t *testing.T) {
		url := startDigestServer(t, "user", "pass", "second@example.org", "n0nc3", "", "auth", "first@example.org")
		if status, _ := send(t, Object{"username": "user", "password": "pass", "realm": "second@example.org"}, "GET", url+"/multi", ""); status != 200 {
			t.Fatal(status)
		}
	})
}
