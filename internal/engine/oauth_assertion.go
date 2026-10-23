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

	"yeek/locales"
)

func buildOAuthClientAssertion(auth Object, endpoint string) (string, error) {
	algorithm := str(auth, "clientAssertionAlgorithm")
	if algorithm == "" {
		algorithm = "HS256"
	}
	method := jwt.GetSigningMethod(algorithm)
	if method == nil {
		return "", errors.New(locales.ChooseASupportedJWTClientAssertion())
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
			return "", errors.New(locales.ClientAssertionSecretIsNotValid())
		}
		secret = decoded
	}
	var key any
	var err error
	if strings.HasPrefix(strings.TrimSpace(string(secret)), "{") {
		var jwk Object
		if err = json.Unmarshal(secret, &jwk); err != nil {
			return "", errors.New(locales.ClientAssertionJWKIsNotValid())
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
			err = errors.New(locales.UnsupportedJWTClientAssertionAlgorithm())
		}
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", locales.ClientAssertionKey(), err)
	}
	return token.SignedString(key)
}
func oauthJWK(jwk Object, algorithm string) (any, error) {
	read := func(name string) ([]byte, error) {
		value, err := base64.RawURLEncoding.DecodeString(str(jwk, name))
		if err != nil || len(value) == 0 {
			return nil, errors.New(locales.JWKIsMissingAValidName(name))
		}
		return value, nil
	}
	switch str(jwk, "kty") {
	case "oct":
		if !strings.HasPrefix(algorithm, "HS") {
			return nil, errors.New(locales.OctJWKRequiresAnHMACAlgorithm())
		}
		return read("k")
	case "RSA":
		if !strings.HasPrefix(algorithm, "RS") && !strings.HasPrefix(algorithm, "PS") {
			return nil, errors.New(locales.RSAJWKRequiresAnRSAAlgorithm())
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
			return nil, errors.New(locales.InvalidRSAJWKExponent())
		}
		padded := make([]byte, 4)
		copy(padded[4-len(exponent):], exponent)
		e := binary.BigEndian.Uint32(padded)
		if e < 3 || e > math.MaxInt32 {
			return nil, errors.New(locales.InvalidRSAJWKExponent())
		}
		key := &rsa.PrivateKey{N: values["n"], E: int(e), D: values["d"], Primes: []*big.Int{values["p"], values["q"]}}
		if err := key.Validate(); err != nil {
			return nil, errors.New(locales.RSAJWKPrivateKeyIsInconsistent())
		}
		key.Precompute()
		return key, nil
	case "EC":
		var curve elliptic.Curve
		switch str(jwk, "crv") {
		case "P-256":
			if algorithm != "ES256" {
				return nil, errors.New(locales.P256RequiresES256())
			}
			curve = elliptic.P256()
		case "P-384":
			if algorithm != "ES384" {
				return nil, errors.New(locales.P384RequiresES384())
			}
			curve = elliptic.P384()
		case "P-521":
			if algorithm != "ES512" {
				return nil, errors.New(locales.P521RequiresES512())
			}
			curve = elliptic.P521()
		default:
			return nil, errors.New(locales.UnsupportedECJWKCurve())
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
			return nil, errors.New(locales.ECJWKIsInvalid())
		}
		public, err := key.PublicKey.Bytes()
		if err != nil {
			return nil, err
		}
		expected := append(append([]byte{4}, x...), y...)
		if !bytes.Equal(public, expected) {
			return nil, errors.New(locales.ECJWKPrivateKeyIsInconsistent())
		}
		return key, nil
	case "OKP":
		if algorithm != "EdDSA" || str(jwk, "crv") != "Ed25519" {
			return nil, errors.New(locales.OKPJWKRequiresEd25519())
		}
		seed, err := read("d")
		if err != nil {
			return nil, err
		}
		if len(seed) != ed25519.SeedSize {
			return nil, errors.New(locales.Ed25519JWKSeedHasTheWrong())
		}
		key := ed25519.NewKeyFromSeed(seed)
		if public := str(jwk, "x"); public != "" {
			data, err := read("x")
			if err != nil || !key.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(data)) {
				return nil, errors.New(locales.Ed25519JWKPrivateKeyIsInconsistent())
			}
		}
		return key, nil
	default:
		return nil, errors.New(locales.JWKMustContainAnOctRSA())
	}
}
