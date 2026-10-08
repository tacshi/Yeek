package engine

import (
	"cmp"
	"crypto"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- required for HTTP Digest interoperability.
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- required for OAuth 1.0 interoperability.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/golang-jwt/jwt/v5"
)

func extendedAuth(req *http.Request, kind string, a Object) error {
	switch kind {
	case "digest":
		return nil
	case "windows", "ntlm":
		user := str(a, "username")
		if domain := str(a, "domain"); domain != "" {
			user = domain + "\\" + user
		}
		req.SetBasicAuth(user, str(a, "password"))
		return nil
	case "jwt":
		token, err := signJWT(a)
		if err != nil {
			return err
		}
		if str(a, "location") == "query" {
			q := req.URL.Query()
			q.Set(cmp.Or(str(a, "name"), "token"), token)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set(cmp.Or(str(a, "name"), "Authorization"), strings.TrimSpace(cmp.Or(str(a, "headerPrefix"), "Bearer")+" "+token))
		}
		return nil
	case "awsv4", "aws":
		var body io.ReadCloser = http.NoBody
		var err error
		if req.GetBody != nil {
			body, err = req.GetBody()
		}
		if err != nil {
			return err
		}
		defer func() { _ = body.Close() }()
		hash := sha256.New()
		if _, err = io.Copy(hash, requestContextReader{req.Context(), body}); err != nil {
			return err
		}
		return awsv4.NewSigner().SignHTTP(req.Context(), aws.Credentials{AccessKeyID: str(a, "accessKeyId"), SecretAccessKey: str(a, "secretAccessKey"), SessionToken: str(a, "sessionToken")}, req, hex.EncodeToString(hash.Sum(nil)), cmp.Or(str(a, "service"), "sts"), cmp.Or(str(a, "region"), "us-east-1"), time.Now())
	case "oauth1":
		return signOAuth1(req, a)
	default:
		return fmt.Errorf("unknown authentication type %q", kind)
	}
}
func requestBytes(req *http.Request) ([]byte, error) {
	if req.GetBody == nil {
		return nil, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return io.ReadAll(body)
}
func signJWT(a Object) (string, error) {
	alg := cmp.Or(str(a, "algorithm"), "HS256")
	method := jwt.GetSigningMethod(alg)
	if method == nil || alg == "none" {
		return "", errors.New("choose a supported JWT signing algorithm")
	}
	claims := jwt.MapClaims{}
	if err := json.Unmarshal([]byte(cmp.Or(str(a, "payload"), "{}")), &claims); err != nil {
		return "", fmt.Errorf("JWT payload: %w", err)
	}
	token := jwt.NewWithClaims(method, claims)
	headers := Object{}
	if text := str(a, "headers"); text != "" {
		if err := json.Unmarshal([]byte(text), &headers); err != nil {
			return "", err
		}
		for k, v := range headers {
			if k != "alg" {
				token.Header[k] = v
			}
		}
	}
	secret := []byte(str(a, "secret"))
	var key any = secret
	var err error
	if boolean(a, "secretBase64") || str(a, "secretBase64") == "true" {
		secret, err = base64.StdEncoding.DecodeString(string(secret))
		if err != nil {
			return "", err
		}
		key = secret
	}
	switch {
	case strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS"):
		key, err = jwt.ParseRSAPrivateKeyFromPEM(secret)
	case strings.HasPrefix(alg, "ES"):
		key, err = jwt.ParseECPrivateKeyFromPEM(secret)
	case alg == "EdDSA":
		key, err = jwt.ParseEdPrivateKeyFromPEM(secret)
	}
	if err != nil {
		return "", err
	}
	return token.SignedString(key)
}
func oauthEscape(value string) string { return strings.ReplaceAll(url.QueryEscape(value), "+", "%20") }
func signOAuth1(req *http.Request, a Object) error {
	method := cmp.Or(str(a, "signatureMethod"), "HMAC-SHA1")
	oauth := url.Values{"oauth_consumer_key": {str(a, "consumerKey")}, "oauth_nonce": {cmp.Or(str(a, "nonce"), uuid.NewV4().String())}, "oauth_timestamp": {cmp.Or(str(a, "timestamp"), strconv.FormatInt(time.Now().Unix(), 10))}, "oauth_signature_method": {method}, "oauth_version": {cmp.Or(str(a, "version"), "1.0")}}
	for _, pair := range [][2]string{{"tokenKey", "oauth_token"}, {"callback", "oauth_callback"}, {"verifier", "oauth_verifier"}} {
		if value := str(a, pair[0]); value != "" {
			oauth.Set(pair[1], value)
		}
	}
	params := req.URL.Query()
	for k, vs := range oauth {
		for _, v := range vs {
			params.Add(k, v)
		}
	}
	if strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		data, err := requestBytes(req)
		if err != nil {
			return err
		}
		form, err := url.ParseQuery(string(data))
		if err != nil {
			return err
		}
		for k, vs := range form {
			for _, v := range vs {
				params.Add(k, v)
			}
		}
	}
	encoded := []string{}
	for k, vs := range params {
		for _, v := range vs {
			encoded = append(encoded, oauthEscape(k)+"="+oauthEscape(v))
		}
	}
	slices.Sort(encoded)
	baseURL := req.URL.Clone()
	baseURL.RawQuery = ""
	baseURL.Fragment = ""
	if baseURL.Path == "" {
		baseURL.Path = "/"
	}
	base := strings.ToUpper(req.Method) + "&" + oauthEscape(baseURL.String()) + "&" + oauthEscape(strings.Join(encoded, "&"))
	key := oauthEscape(str(a, "consumerSecret")) + "&" + oauthEscape(str(a, "tokenSecret"))
	var signature []byte
	switch method {
	case "HMAC-SHA1":
		h := hmac.New(sha1.New, []byte(key))
		_, _ = h.Write([]byte(base))
		signature = h.Sum(nil)
	case "HMAC-SHA256":
		h := hmac.New(sha256.New, []byte(key))
		_, _ = h.Write([]byte(base))
		signature = h.Sum(nil)
	case "RSA-SHA1":
		private, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(str(a, "privateKey")))
		if err != nil {
			return err
		}
		digest := sha1.Sum([]byte(base)) // #nosec G401 -- RSA-SHA1 is a selectable OAuth 1 protocol algorithm.
		signature, err = rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA1, digest[:])
		if err != nil {
			return err
		}
	case "PLAINTEXT":
		oauth.Set("oauth_signature", key)
	default:
		return errors.New("unsupported OAuth 1 signature method")
	}
	if signature != nil {
		oauth.Set("oauth_signature", base64.StdEncoding.EncodeToString(signature))
	}
	parts := []string{}
	if realm := str(a, "realm"); realm != "" {
		parts = append(parts, `realm="`+oauthEscape(realm)+`"`)
	}
	keys := []string{}
	for k := range oauth {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		parts = append(parts, k+`="`+oauthEscape(oauth.Get(k))+`"`)
	}
	req.Header.Set("Authorization", "OAuth "+strings.Join(parts, ", "))
	return nil
}

type digestTransport struct {
	base http.RoundTripper
	auth Object
}

var digestParams = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^,\s]+))`)

func (d digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := d.base.RoundTrip(req)
	if err != nil || res.StatusCode != 401 {
		return res, err
	}
	challenge := ""
	for _, h := range res.Header.Values("WWW-Authenticate") {
		if strings.HasPrefix(strings.ToLower(h), "digest ") {
			challenge = h[7:]
			break
		}
	}
	if challenge == "" {
		return res, nil
	}
	params := map[string]string{}
	for _, match := range digestParams.FindAllStringSubmatch(challenge, -1) {
		params[match[1]] = cmp.Or(match[2], match[3])
	}
	algorithm := strings.ToUpper(cmp.Or(params["algorithm"], "MD5"))
	hash := func(text string) string {
		if strings.HasPrefix(algorithm, "SHA-256") {
			sum := sha256.Sum256([]byte(text))
			return hex.EncodeToString(sum[:])
		}
		sum := md5.Sum([]byte(text))
		return hex.EncodeToString(sum[:])
	} // #nosec G401 -- HTTP Digest supports legacy MD5 by protocol.
	if algorithm != "MD5" && algorithm != "MD5-SESS" && algorithm != "SHA-256" && algorithm != "SHA-256-SESS" {
		return res, fmt.Errorf("unsupported Digest algorithm %s", algorithm)
	}
	qop := ""
	if params["qop"] != "" {
		for option := range strings.SplitSeq(params["qop"], ",") {
			if strings.TrimSpace(option) == "auth" {
				qop = "auth"
				break
			}
		}
		if qop == "" {
			_ = res.Body.Close()
			return nil, errors.New("server requires unsupported Digest qop")
		}
	}
	nonce := params["nonce"]
	cnonce := strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	uri := req.URL.RequestURI()
	a1 := hash(str(d.auth, "username") + ":" + params["realm"] + ":" + str(d.auth, "password"))
	if strings.HasSuffix(algorithm, "-SESS") {
		a1 = hash(a1 + ":" + nonce + ":" + cnonce)
	}
	a2 := hash(req.Method + ":" + uri)
	digest := hash(a1 + ":" + nonce + ":" + a2)
	if qop != "" {
		digest = hash(a1 + ":" + nonce + ":00000001:" + cnonce + ":" + qop + ":" + a2)
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		retry.Body, err = req.GetBody()
		if err != nil {
			_ = res.Body.Close()
			return nil, err
		}
	}
	quoted := func(v string) string { return strconv.Quote(v) }
	value := "Digest username=" + quoted(str(d.auth, "username")) + ", realm=" + quoted(params["realm"]) + ", nonce=" + quoted(nonce) + ", uri=" + quoted(uri) + ", response=" + quoted(digest) + ", algorithm=" + algorithm
	if qop != "" {
		value += ", qop=auth, nc=00000001, cnonce=" + quoted(cnonce)
	}
	if opaque := params["opaque"]; opaque != "" {
		value += ", opaque=" + quoted(opaque)
	}
	retry.Header.Set("Authorization", value)
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	_ = res.Body.Close()
	return d.base.RoundTrip(retry)
}
