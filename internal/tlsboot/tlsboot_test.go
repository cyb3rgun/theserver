package tlsboot

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s holds no PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cert
}

func TestEnsureCertificateCreatesOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	hosts := []string{"localhost", "127.0.0.1", "range-pc"}

	certPath, keyPath, fingerprint, created, err := EnsureCertificate(dir, hosts)
	if err != nil {
		t.Fatalf("EnsureCertificate: %v", err)
	}
	if !created {
		t.Error("the first call did not report that it created the certificate")
	}
	if certPath != filepath.Join(dir, CertFileName) || keyPath != filepath.Join(dir, KeyFileName) {
		t.Errorf("paths are %s and %s", certPath, keyPath)
	}

	cert := readCert(t, certPath)
	if !slices.Equal(cert.DNSNames, []string{"localhost", "range-pc"}) {
		t.Errorf("DNS names are %v", cert.DNSNames)
	}
	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")) {
		t.Errorf("IP addresses are %v", cert.IPAddresses)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Errorf("the key is %T, want ECDSA P-256", cert.PublicKey)
	}
	if years := cert.NotAfter.Sub(time.Now()).Hours() / 24 / 365; years < 9.9 || years > 10.1 {
		t.Errorf("the certificate lasts %.1f years, want 10", years)
	}
	if !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Error("the certificate is not for server authentication")
	}
	if err := cert.VerifyHostname("range-pc"); err != nil {
		t.Errorf("VerifyHostname: %v", err)
	}

	sum := sha256.Sum256(cert.Raw)
	if want := strings.ToUpper(strings.Join(hexPairs(sum[:]), ":")); fingerprint != want {
		t.Errorf("fingerprint is %s, want %s", fingerprint, want)
	}

	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Errorf("the pair does not load: %v", err)
	}

	certBefore, _ := os.ReadFile(certPath)
	keyBefore, _ := os.ReadFile(keyPath)

	_, _, again, created, err := EnsureCertificate(dir, []string{"other-host"})
	if err != nil {
		t.Fatalf("second EnsureCertificate: %v", err)
	}
	if created {
		t.Error("the second call created a certificate again")
	}
	if again != fingerprint {
		t.Errorf("the second call returned fingerprint %s, want %s", again, fingerprint)
	}
	certAfter, _ := os.ReadFile(certPath)
	keyAfter, _ := os.ReadFile(keyPath)
	if !bytes.Equal(certBefore, certAfter) || !bytes.Equal(keyBefore, keyAfter) {
		t.Error("the second call changed the files")
	}
}

func TestEnsureCertificateRefusesHalfAPair(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CertFileName), []byte("placed by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := EnsureCertificate(dir, DefaultHosts()); err == nil {
		t.Fatal("EnsureCertificate accepted a directory with a certificate but no key")
	}
	data, _ := os.ReadFile(filepath.Join(dir, CertFileName))
	if string(data) != "placed by hand" {
		t.Error("the certificate placed by hand was overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFileName)); err == nil {
		t.Error("a key was written next to the foreign certificate")
	}
}

func TestFingerprintRejectsGarbage(t *testing.T) {
	if _, err := Fingerprint([]byte("not a certificate")); err == nil {
		t.Error("Fingerprint accepted text that is not PEM")
	}
	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})
	if _, err := Fingerprint(bad); err == nil {
		t.Error("Fingerprint accepted a PEM block that is not a certificate")
	}
}

func TestDefaultHosts(t *testing.T) {
	hosts := DefaultHosts()
	for _, want := range []string{"localhost", "127.0.0.1"} {
		if !slices.Contains(hosts, want) {
			t.Errorf("DefaultHosts %v lacks %s", hosts, want)
		}
	}
}

func hexPairs(b []byte) []string {
	const digits = "0123456789abcdef"
	pairs := make([]string, len(b))
	for i, v := range b {
		pairs[i] = string([]byte{digits[v>>4], digits[v&0x0f]})
	}
	return pairs
}
