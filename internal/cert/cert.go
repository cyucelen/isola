package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// CertPaths holds the file paths for the generated CA certificate and key.
type CertPaths struct {
	CACert string
	CAKey  string
}

// CAPath returns the CA certificate path for a cert directory, without
// requiring the CA to have been generated yet. It is the single source of
// truth for the "<dir>/ca.crt" layout that callers (trust, up) reference.
func CAPath(dir string) string {
	return filepath.Join(dir, "ca.crt")
}

// EnsureCerts ensures the dev CA certificate and key exist in dir, returning
// their paths. If ca.crt and ca.key already exist it returns without
// regenerating. Per-host leaf certificates are minted on demand from this CA
// by NewSNIGetCertificate, so no server certificate is persisted here.
func EnsureCerts(dir string) (CertPaths, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return CertPaths{}, fmt.Errorf("creating cert directory: %w", err)
	}

	paths := CertPaths{
		CACert: CAPath(dir),
		CAKey:  filepath.Join(dir, "ca.key"),
	}

	if fileExists(paths.CACert) && fileExists(paths.CAKey) {
		return paths, nil
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return CertPaths{}, fmt.Errorf("generating CA key: %w", err)
	}

	caSerial, err := randomSerial()
	if err != nil {
		return CertPaths{}, err
	}

	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			CommonName:   "Isola Dev CA",
			Organization: []string{"Isola"},
		},
		NotBefore:             now,
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return CertPaths{}, fmt.Errorf("creating CA certificate: %w", err)
	}

	if err := writePEM(paths.CACert, "CERTIFICATE", caCertDER, 0644); err != nil {
		return CertPaths{}, fmt.Errorf("writing CA cert: %w", err)
	}
	if err := writeKeyPEM(paths.CAKey, caKey); err != nil {
		return CertPaths{}, fmt.Errorf("writing CA key: %w", err)
	}

	return paths, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generating serial number: %w", err)
	}
	return serial, nil
}

func writePEM(path, blockType string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func writeKeyPEM(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "EC PRIVATE KEY", der, 0600)
}
