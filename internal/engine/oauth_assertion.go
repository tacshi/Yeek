package engine

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

func buildOAuthClientAssertion(auth Object, endpoint string) (string, error) {
	algorithm := str(auth, "clientAssertionAlgorithm")
	if algorithm == "" {
		algorithm = "HS256"
	}
	method := jwt.GetSigningMethod(algorithm)
	if method == nil {
		return "", errors.New("choose a supported JWT client assertion algorithm")
	}
	now := time.Now().Unix()
	token := jwt.NewWithClaims(method, jwt.MapClaims{"iss": str(auth, "clientId"), "sub": str(auth, "clientId"), "aud": endpoint, "iat": now, "exp": now + 300, "jti": uuid.New().String()})
	if algorithm == "none" {
		return token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	}
	secret := []byte(str(auth, "clientAssertionSecret"))
	if oauthBool(auth, "clientAssertionSecretBase64", false) {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(secret)))
		if err != nil {
			return "", errors.New("client assertion secret is not valid base64")
		}
		secret = decoded
	}
	var key any
	var err error
	if strings.HasPrefix(strings.TrimSpace(string(secret)), "{") {
		var jwk Object
		if err = json.Unmarshal(secret, &jwk); err != nil {
			return "", errors.New("client assertion JWK is not valid JSON")
		}
		key, err = oauthJWK(jwk, algorithm)
		if kid := str(jwk, "kid"); kid != "" {
			token.Header["kid"] = kid
		}
	} else {
		switch {
		case strings.HasPrefix(algorithm, "HS"):
			key = secret
		case strings.HasPrefix(algorithm, "RS"), strings.HasPrefix(algorithm, "PS"):
			key, err = jwt.ParseRSAPrivateKeyFromPEM(secret)
		case strings.HasPrefix(algorithm, "ES"):
			key, err = jwt.ParseECPrivateKeyFromPEM(secret)
		case algorithm == "EdDSA":
			key, err = jwt.ParseEdPrivateKeyFromPEM(secret)
		default:
			err = errors.New("unsupported JWT client assertion algorithm")
		}
	}
	if err != nil {
		return "", fmt.Errorf("client assertion key: %w", err)
	}
	return token.SignedString(key)
}
func oauthJWK(jwk Object, algorithm string) (any, error) {
	read := func(name string) ([]byte, error) {
		value, err := base64.RawURLEncoding.DecodeString(str(jwk, name))
		if err != nil || len(value) == 0 {
			return nil, fmt.Errorf("JWK is missing a valid %s value", name)
		}
		return value, nil
	}
	switch str(jwk, "kty") {
	case "oct":
		if !strings.HasPrefix(algorithm, "HS") {
			return nil, errors.New("oct JWK requires an HMAC algorithm")
		}
		return read("k")
	case "RSA":
		if !strings.HasPrefix(algorithm, "RS") && !strings.HasPrefix(algorithm, "PS") {
			return nil, errors.New("RSA JWK requires an RSA algorithm")
		}
		values := map[string]*big.Int{}
		for _, name := range []string{"n", "d", "p", "q"} {
			data, err := read(name)
			if err != nil {
				return nil, err
			}
			values[name] = new(big.Int).SetBytes(data)
		}
		exponent, err := read("e")
		if err != nil {
			return nil, err
		}
		if len(exponent) > 4 {
			return nil, errors.New("invalid RSA JWK exponent")
		}
		padded := make([]byte, 4)
		copy(padded[4-len(exponent):], exponent)
		e := binary.BigEndian.Uint32(padded)
		if e < 3 || e > math.MaxInt32 {
			return nil, errors.New("invalid RSA JWK exponent")
		}
		key := &rsa.PrivateKey{N: values["n"], E: int(e), D: values["d"], Primes: []*big.Int{values["p"], values["q"]}}
		if err := key.Validate(); err != nil {
			return nil, errors.New("RSA JWK private key is inconsistent")
		}
		key.Precompute()
		return key, nil
	case "EC":
		var curve elliptic.Curve
		switch str(jwk, "crv") {
		case "P-256":
			if algorithm != "ES256" {
				return nil, errors.New("P-256 requires ES256")
			}
			curve = elliptic.P256()
		case "P-384":
			if algorithm != "ES384" {
				return nil, errors.New("P-384 requires ES384")
			}
			curve = elliptic.P384()
		case "P-521":
			if algorithm != "ES512" {
				return nil, errors.New("P-521 requires ES512")
			}
			curve = elliptic.P521()
		default:
			return nil, errors.New("unsupported EC JWK curve")
		}
		x, err := read("x")
		if err != nil {
			return nil, err
		}
		y, err := read("y")
		if err != nil {
			return nil, err
		}
		d, err := read("d")
		if err != nil {
			return nil, err
		}
		key, err := ecdsa.ParseRawPrivateKey(curve, d)
		if err != nil {
			return nil, errors.New("EC JWK is invalid")
		}
		public, err := key.PublicKey.Bytes()
		if err != nil {
			return nil, err
		}
		expected := append(append([]byte{4}, x...), y...)
		if !bytes.Equal(public, expected) {
			return nil, errors.New("EC JWK private key is inconsistent")
		}
		return key, nil
	case "OKP":
		if algorithm != "EdDSA" || str(jwk, "crv") != "Ed25519" {
			return nil, errors.New("OKP JWK requires Ed25519")
		}
		seed, err := read("d")
		if err != nil {
			return nil, err
		}
		if len(seed) != ed25519.SeedSize {
			return nil, errors.New("Ed25519 JWK seed has the wrong length")
		}
		key := ed25519.NewKeyFromSeed(seed)
		if public := str(jwk, "x"); public != "" {
			data, err := read("x")
			if err != nil || !key.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(data)) {
				return nil, errors.New("Ed25519 JWK private key is inconsistent")
			}
		}
		return key, nil
	default:
		return nil, errors.New("JWK must contain an oct, RSA, EC, or Ed25519 private key")
	}
}
