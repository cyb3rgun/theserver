// Package tlsboot gives theserver a certificate on its first start (D-018).
//
// When the data directory holds no certificate yet, EnsureCertificate creates
// a self signed one with the standard library: ECDSA P-256, valid for ten
// years, with the requested host names and addresses as subject alternative
// names. Operators install a real certificate by replacing the two files.
// Devices in S01 pin the SHA-256 fingerprint of the certificate or skip
// verification explicitly.
package tlsboot

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File names inside the certificate directory.
const (
	CertFileName = "server.crt"
	KeyFileName  = "server.key"
)

// Validity is how long a generated certificate lasts.
const Validity = 10 * 365 * 24 * time.Hour

// EnsureCertificate returns the certificate and key in dir, creating both if
// neither exists. created reports whether this call made them. A directory
// that holds only one of the two files is an error, so a half replaced pair is
// never overwritten.
func EnsureCertificate(dir string, hosts []string) (certPath, keyPath, fingerprint string, created bool, err error) {
	certPath = filepath.Join(dir, CertFileName)
	keyPath = filepath.Join(dir, KeyFileName)

	certExists, err := exists(certPath)
	if err != nil {
		return "", "", "", false, err
	}
	keyExists, err := exists(keyPath)
	if err != nil {
		return "", "", "", false, err
	}

	switch {
	case certExists && keyExists:
		fingerprint, err = FingerprintFile(certPath)
		return certPath, keyPath, fingerprint, false, err
	case certExists || keyExists:
		return "", "", "", false, fmt.Errorf("%s holds only one of %s and %s; replace both or remove both", dir, CertFileName, KeyFileName)
	}

	certPEM, keyPEM, err := generate(hosts, time.Now())
	if err != nil {
		return "", "", "", false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", "", false, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := writeNew(keyPath, keyPEM, 0o600); err != nil {
		return "", "", "", false, err
	}
	if err := writeNew(certPath, certPEM, 0o644); err != nil {
		os.Remove(keyPath)
		return "", "", "", false, err
	}

	fingerprint, err = Fingerprint(certPEM)
	return certPath, keyPath, fingerprint, true, err
}

// DefaultHosts are the names a generated certificate covers: localhost, the
// loopback addresses and the host name of the machine.
func DefaultHosts() []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if name, err := os.Hostname(); err == nil && name != "" {
		hosts = append(hosts, name)
	}
	return hosts
}

// Fingerprint is the SHA-256 of the first certificate in certPEM, as upper
// case hexadecimal pairs separated by colons.
func Fingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("no PEM certificate found")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", fmt.Errorf("parse certificate: %w", err)
	}
	return FingerprintDER(block.Bytes), nil
}

// FingerprintDER is the SHA-256 of a DER certificate in the same format.
func FingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(pairs, ":")
}

// FingerprintFile reads a PEM certificate file and returns its fingerprint.
func FingerprintFile(certPath string) (string, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	fingerprint, err := Fingerprint(certPEM)
	if err != nil {
		return "", fmt.Errorf("%s: %w", certPath, err)
	}
	return fingerprint, nil
}

func generate(hosts []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "theserver", Organization: []string{"CYB3RGUN"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else if host != "" {
			template.DNSNames = append(template.DNSNames, host)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// writeNew writes a file that must not exist yet.
func writeNew(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
