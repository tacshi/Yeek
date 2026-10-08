package engine

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/youmark/pkcs8"
	"golang.org/x/net/idna"
	"software.sslmate.com/src/go-pkcs12"
)

func NetworkHostname(value string) (string, error) {
	host := strings.TrimSuffix(strings.TrimSpace(value), ".")
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return address.String(), nil
	}
	if host == "" || strings.ContainsAny(host, "/:?#@\\ \t\r\n") {
		return "", errors.New("enter a hostname without a scheme, port, or path")
	}
	ascii := true
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("_.-", r):
		default:
			ascii = false
		}
	}
	if ascii {
		return strings.ToLower(strings.TrimSuffix(host, ".")), nil
	}
	host, err := idna.Lookup.ToASCII(strings.TrimSuffix(host, "."))
	if err != nil {
		return "", errors.New("invalid hostname")
	}
	return strings.ToLower(host), nil
}
func networkPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch u.Scheme {
	case "https", "wss", "grpcs":
		return "443"
	case "socks5", "socks5h":
		return "1080"
	default:
		return "80"
	}
}
func ValidateClientCertificate(model Object) error {
	if _, err := NetworkHostname(str(model, "host")); err != nil {
		return fmt.Errorf("certificate host: %w", err)
	}
	if value := model["port"]; value != nil && value != "" {
		port := number(model, "port")
		if port < 1 || port > 65535 || math.Trunc(port) != port {
			return errors.New("certificate port must be between 1 and 65535")
		}
	}
	pfx, crt, key := str(model, "pfxFile"), str(model, "crtFile"), str(model, "keyFile")
	if pfx != "" && (crt != "" || key != "") {
		return errors.New("choose either a PFX file or a PEM certificate and key")
	}
	if pfx == "" && (crt == "" || key == "") {
		return errors.New("choose a certificate and private key, or a PFX file")
	}
	return nil
}
func certificateMatches(model Object, target *url.URL) bool {
	if !enabled(model) {
		return false
	}
	host, err := NetworkHostname(str(model, "host"))
	if err != nil {
		return false
	}
	want, err := NetworkHostname(target.Hostname())
	if err != nil || host != want {
		return false
	}
	port := number(model, "port")
	return port == 0 || strconv.FormatFloat(port, 'f', -1, 64) == networkPort(target)
}
func readTLSFile(path string) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- certificate paths are selected by the desktop user.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, errors.New("certificate file exceeds 16 MiB")
	}
	return data, nil
}
func LoadClientCertificate(model Object) (tls.Certificate, error) {
	if err := ValidateClientCertificate(model); err != nil {
		return tls.Certificate{}, err
	}
	password := str(model, "passphrase")
	if path := str(model, "pfxFile"); path != "" {
		data, err := readTLSFile(path)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("read PFX: %w", err)
		}
		key, leaf, chain, err := pkcs12.DecodeChain(data, password)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("decode PFX: %w", err)
		}
		if leaf == nil {
			return tls.Certificate{}, errors.New("PFX file contains no certificate")
		}
		pair := tls.Certificate{PrivateKey: key, Leaf: leaf, Certificate: [][]byte{leaf.Raw}}
		for _, ca := range chain {
			pair.Certificate = append(pair.Certificate, ca.Raw)
		}
		return pair, nil
	}
	crt, err := readTLSFile(str(model, "crtFile"))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read certificate: %w", err)
	}
	key, err := readTLSFile(str(model, "keyFile"))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read private key: %w", err)
	}
	block, _ := pem.Decode(key)
	if block == nil {
		return tls.Certificate{}, errors.New("private key file contains no PEM key")
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" {
		private, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, []byte(password))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("decrypt private key: %w", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(private)
		if err != nil {
			return tls.Certificate{}, err
		}
		key = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	} else if block.Headers["Proc-Type"] == "4,ENCRYPTED" {
		//nolint:staticcheck // Legacy PEM is an explicit certificate import format.
		der, err := x509.DecryptPEMBlock(block, []byte(password))
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("decrypt legacy PEM key: %w", err)
		}
		key = pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: der})
	}
	pair, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("client certificate: %w", err)
	}
	if pair.Leaf == nil && len(pair.Certificate) > 0 {
		pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
	}
	return pair, err
}
func certificateAuthorities(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readTLSFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		cert, err := x509.ParseCertificate(data)
		if err != nil {
			return nil, errors.New("CA file contains no valid PEM or DER certificates")
		}
		pool.AddCert(cert)
	}
	return pool, nil
}

func CAFileSummary(path string) (string, error) {
	if err := ValidateCAFile(path); err != nil {
		return "", err
	}
	data, err := readTLSFile(path)
	if err != nil {
		return "", err
	}
	certs := []*x509.Certificate{}
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			if len(certs) == 0 {
				cert, err := x509.ParseCertificate(data)
				if err == nil {
					certs = append(certs, cert)
				}
			}
			break
		}
		data = rest
		if block.Type == "CERTIFICATE" {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				certs = append(certs, cert)
			}
		}
	}
	parts := []string{}
	for _, cert := range certs {
		parts = append(parts, cert.Subject.String()+" · expires "+cert.NotAfter.Local().Format("2006-01-02"))
	}
	return strings.Join(parts, "\n"), nil
}
func ValidateCAFile(path string) error { _, err := certificateAuthorities(path); return err }
