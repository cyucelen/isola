package cert

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cyucelen/isola/internal/git"
)

func TestEnsureCerts_GeneratesValidCA(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}

	// Parse CA cert.
	caPEM, err := os.ReadFile(paths.CACert)
	if err != nil {
		t.Fatalf("reading CA cert: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("failed to decode CA cert PEM")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing CA cert: %v", err)
	}

	if !ca.IsCA {
		t.Error("CA cert should have IsCA=true")
	}
	if ca.Subject.CommonName != "Isola Dev CA" {
		t.Errorf("CA CN = %q, want %q", ca.Subject.CommonName, "Isola Dev CA")
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("CA should have KeyUsageCertSign")
	}
	if ca.KeyUsage&x509.KeyUsageCRLSign == 0 {
		t.Error("CA should have KeyUsageCRLSign")
	}
}

func TestEnsureCerts_Idempotent(t *testing.T) {
	dir := t.TempDir()

	// First call generates certs.
	paths1, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("first EnsureCerts() error: %v", err)
	}

	// Record modification times.
	info1, _ := os.Stat(paths1.CACert)
	modTime1 := info1.ModTime()

	// Second call should skip regeneration.
	paths2, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("second EnsureCerts() error: %v", err)
	}

	info2, _ := os.Stat(paths2.CACert)
	modTime2 := info2.ModTime()

	if !modTime1.Equal(modTime2) {
		t.Error("EnsureCerts should not regenerate when all files exist")
	}
}

func TestEnsureCerts_RegeneratesOnMissing(t *testing.T) {
	dir := t.TempDir()

	// First call generates certs.
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("first EnsureCerts() error: %v", err)
	}

	// Remove one file.
	if err := os.Remove(paths.CAKey); err != nil {
		t.Fatalf("removing CA key: %v", err)
	}

	// Second call should regenerate all files.
	_, err = EnsureCerts(dir)
	if err != nil {
		t.Fatalf("second EnsureCerts() error: %v", err)
	}

	// Verify file exists again.
	if !fileExists(filepath.Join(dir, "ca.key")) {
		t.Error("ca.key should be regenerated")
	}
}

func TestEnsureCerts_KeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}

	for _, keyPath := range []string{paths.CAKey} {
		info, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("stat %s: %v", keyPath, err)
		}
		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("%s permissions = %o, want 0600", keyPath, perm)
		}
	}
}

// loadTestCA reads and parses the CA cert produced by EnsureCerts for use as a
// verification root.
func loadTestCA(t *testing.T, caCertPath string) *x509.Certificate {
	t.Helper()
	caPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		t.Fatalf("reading CA cert: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("failed to decode CA cert PEM")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing CA cert: %v", err)
	}
	return ca
}

func TestNewSNIGetCertificate_VerifiesSubdomain(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}

	getCert, err := NewSNIGetCertificate(paths)
	if err != nil {
		t.Fatalf("NewSNIGetCertificate() error: %v", err)
	}

	ca := loadTestCA(t, paths.CACert)
	roots := x509.NewCertPool()
	roots.AddCert(ca)

	// A per-worktree subdomain must verify for its own host — the case the
	// "*.localhost" wildcard failed for strict clients — while bare localhost
	// keeps working.
	for _, host := range []string{"feature-x.localhost", "localhost"} {
		host := host
		t.Run(host, func(t *testing.T) {
			cert, err := getCert(&tls.ClientHelloInfo{ServerName: host})
			if err != nil {
				t.Fatalf("getCert(%q) error: %v", host, err)
			}
			if cert.Leaf == nil {
				t.Fatal("returned certificate has nil Leaf")
			}

			if err := cert.Leaf.VerifyHostname(host); err != nil {
				t.Errorf("VerifyHostname(%q) failed: %v", host, err)
			}

			if _, err := cert.Leaf.Verify(x509.VerifyOptions{
				DNSName:   host,
				Roots:     roots,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}); err != nil {
				t.Errorf("chain verification for %q failed: %v", host, err)
			}
		})
	}
}

// TestNewSNIGetCertificate_LongWorktreeHost covers the TLS half of the
// long-branch failure. The leaf is minted for whatever SNI name arrives, so a
// worktree host is only presentable if every label in it is within the 63-byte
// DNS limit: browsers refuse to match a SAN containing an over-long label, which
// is why issuing a certificate for the unshortened host could never work. This
// asserts the host isola now derives is one that both verifies and stays legal.
func TestNewSNIGetCertificate_LongWorktreeHost(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}
	getCert, err := NewSNIGetCertificate(paths)
	if err != nil {
		t.Fatalf("NewSNIGetCertificate() error: %v", err)
	}
	ca := loadTestCA(t, paths.CACert)
	roots := x509.NewCertPool()
	roots.AddCert(ca)

	label := git.HostLabel("dependabot/npm_and_yarn/services/manager-dashboard/ai-sdk/react-4.0.40")
	host := label + ".mono.localhost"

	cert, err := getCert(&tls.ClientHelloInfo{ServerName: host})
	if err != nil {
		t.Fatalf("getCert(%q) error: %v", host, err)
	}
	if cert.Leaf == nil {
		t.Fatal("returned certificate has nil Leaf")
	}

	// Every SAN label must be within the limit, or a browser rejects the match
	// with ERR_CERT_COMMON_NAME_INVALID even though the name is present.
	for _, name := range cert.Leaf.DNSNames {
		for _, l := range strings.Split(name, ".") {
			if len(l) > 63 {
				t.Errorf("SAN %q has a %d-byte label %q; browsers will not match it", name, len(l), l)
			}
		}
	}
	if len(cert.Leaf.Subject.CommonName) > 64 {
		t.Errorf("CN %q is %d bytes, over the 64-byte X.509 limit", cert.Leaf.Subject.CommonName, len(cert.Leaf.Subject.CommonName))
	}

	if err := cert.Leaf.VerifyHostname(host); err != nil {
		t.Errorf("VerifyHostname(%q) failed: %v", host, err)
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{
		DNSName:   host,
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("chain verification for %q failed: %v", host, err)
	}
}

func TestNewSNIGetCertificate_CachesPerHost(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}

	getCert, err := NewSNIGetCertificate(paths)
	if err != nil {
		t.Fatalf("NewSNIGetCertificate() error: %v", err)
	}

	first, err := getCert(&tls.ClientHelloInfo{ServerName: "feature-x.localhost"})
	if err != nil {
		t.Fatalf("first getCert error: %v", err)
	}
	second, err := getCert(&tls.ClientHelloInfo{ServerName: "feature-x.localhost"})
	if err != nil {
		t.Fatalf("second getCert error: %v", err)
	}
	if first != second {
		t.Error("expected cached certificate to be reused for the same host")
	}

	other, err := getCert(&tls.ClientHelloInfo{ServerName: "feature-y.localhost"})
	if err != nil {
		t.Fatalf("getCert for other host error: %v", err)
	}
	if other == first {
		t.Error("expected a distinct certificate for a different host")
	}
}

func TestNewSNIGetCertificate_EmptySNIFallsBack(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}

	getCert, err := NewSNIGetCertificate(paths)
	if err != nil {
		t.Fatalf("NewSNIGetCertificate() error: %v", err)
	}

	// A client connecting by IP sends no SNI; the handshake must still get a
	// usable loopback certificate rather than an error.
	cert, err := getCert(&tls.ClientHelloInfo{ServerName: ""})
	if err != nil {
		t.Fatalf("getCert with empty SNI error: %v", err)
	}
	if cert.Leaf == nil {
		t.Fatal("returned certificate has nil Leaf")
	}
	if err := cert.Leaf.VerifyHostname("localhost"); err != nil {
		t.Errorf("fallback certificate should verify localhost: %v", err)
	}
}

func TestNewSNIGetCertificate_NonLocalhostSNIFallsBack(t *testing.T) {
	dir := t.TempDir()
	paths, err := EnsureCerts(dir)
	if err != nil {
		t.Fatalf("EnsureCerts() error: %v", err)
	}

	getCert, err := NewSNIGetCertificate(paths)
	if err != nil {
		t.Fatalf("NewSNIGetCertificate() error: %v", err)
	}

	// An untrusted, non-localhost SNI must not mint a certificate for that
	// name; it collapses to the loopback leaf so arbitrary names cannot drive
	// unbounded minting.
	cert, err := getCert(&tls.ClientHelloInfo{ServerName: "attacker.example.com"})
	if err != nil {
		t.Fatalf("getCert error: %v", err)
	}
	if err := cert.Leaf.VerifyHostname("attacker.example.com"); err == nil {
		t.Error("certificate should NOT verify a non-localhost SNI host")
	}
	if err := cert.Leaf.VerifyHostname("localhost"); err != nil {
		t.Errorf("fallback certificate should verify localhost: %v", err)
	}
}
