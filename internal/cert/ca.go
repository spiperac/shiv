// Package cert handles all certificate concerns for Shiv:
//   - Root CA generation and persistent storage
//   - Per-hostname TLS certificate generation and signing
//   - In-memory cert cache (sync.Map, rebuilt each launch)
//   - OS trust store installation (see install.go)
package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	caKeySize    = 4096
	hostKeySize  = 2048
	caValidity   = 10 * 365 * 24 * time.Hour
	hostValidity = 365 * 24 * time.Hour
)

// CA holds the loaded root certificate authority keypair and an
// in-memory cache of already-generated per-host certificates.
type CA struct {
	cert  *x509.Certificate
	key   *rsa.PrivateKey
	cache sync.Map // map[string]*tls.Certificate
	fresh bool
}

// Fresh returns true if the CA was generated this run (not loaded from disk).
// Use this to decide whether to attempt OS trust store installation.
func (ca *CA) Fresh() bool {
	return ca.fresh
}

// Dir returns the directory where Shiv stores the CA files.
// Uses os.UserConfigDir()/shiv — created if it does not exist.
func Dir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cert: cannot find user config dir: %w", err)
	}
	dir := filepath.Join(configDir, "shiv")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cert: cannot create config dir %s: %w", dir, err)
	}
	return dir, nil
}

// Load loads the CA from disk, generating it if it does not exist.
// The CA keypair is stored in dir/ca.key and dir/ca.crt.
func Load() (*CA, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "ca.key")
	crtPath := filepath.Join(dir, "ca.crt")

	// If both files exist, load them.
	if fileExists(keyPath) && fileExists(crtPath) {
		return loadFromDisk(keyPath, crtPath)
	}

	// Otherwise generate a fresh CA and persist it.
	return generate(keyPath, crtPath)
}

// LoadFromDir is like Load but uses an explicit directory. Useful in tests.
func LoadFromDir(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("cert: cannot create dir %s: %w", dir, err)
	}
	keyPath := filepath.Join(dir, "ca.key")
	crtPath := filepath.Join(dir, "ca.crt")

	if fileExists(keyPath) && fileExists(crtPath) {
		return loadFromDisk(keyPath, crtPath)
	}
	return generate(keyPath, crtPath)
}

// CertPEMPath returns the path to the CA certificate PEM file.
func CertPEMPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ca.crt"), nil
}

// CertPEM returns the PEM-encoded CA certificate bytes.
func (ca *CA) CertPEM() ([]byte, error) {
	return encodeCertPEM(ca.cert.Raw), nil
}

// TLSCertForHost returns a *tls.Certificate for the given hostname,
// generating and caching one if it does not exist yet.
func (ca *CA) TLSCertForHost(host string) (*tls.Certificate, error) {
	// Strip port if present.
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	if cachedCert, ok := ca.cache.Load(hostname); ok {
		return cachedCert.(*tls.Certificate), nil
	}

	tlsCert, err := ca.generateHostCert(hostname)
	if err != nil {
		return nil, err
	}

	// Store-if-absent to avoid races generating the same cert twice.
	actual, _ := ca.cache.LoadOrStore(hostname, tlsCert)
	return actual.(*tls.Certificate), nil
}

// X509Cert returns the underlying *x509.Certificate for the CA.
// Used by tests and trust store installation.
func (ca *CA) X509Cert() *x509.Certificate {
	return ca.cert
}

// ---------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------

func generate(keyPath, crtPath string) (*CA, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, caKeySize)
	if err != nil {
		return nil, fmt.Errorf("cert: CA key generation failed: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Shiv Local CA",
			Organization: []string{"Shiv"},
		},
		NotBefore:             time.Now().Add(-1 * time.Minute), // small back-date for clock skew
		NotAfter:              time.Now().Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("cert: CA cert creation failed: %w", err)
	}

	parsedCert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, fmt.Errorf("cert: CA cert parse failed: %w", err)
	}

	// Persist key.
	if err := writePEM(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey), 0600); err != nil {
		return nil, err
	}
	// Persist cert.
	if err := writePEM(crtPath, "CERTIFICATE", derBytes, 0644); err != nil {
		return nil, err
	}

	return &CA{cert: parsedCert, key: caKey, fresh: true}, nil
}

func loadFromDisk(keyPath, crtPath string) (*CA, error) {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("cert: read CA key: %w", err)
	}
	crtPEM, err := os.ReadFile(crtPath)
	if err != nil {
		return nil, fmt.Errorf("cert: read CA cert: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, errors.New("cert: CA key PEM decode failed")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cert: CA key parse: %w", err)
	}

	certBlock, _ := pem.Decode(crtPEM)
	if certBlock == nil {
		return nil, errors.New("cert: CA cert PEM decode failed")
	}
	parsedCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cert: CA cert parse: %w", err)
	}

	return &CA{cert: parsedCert, key: caKey}, nil
}

func (ca *CA) generateHostCert(hostname string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, hostKeySize)
	if err != nil {
		return nil, fmt.Errorf("cert: host key generation failed for %s: %w", hostname, err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	certTemplate := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: hostname,
		},
		NotBefore: time.Now().Add(-1 * time.Minute),
		NotAfter:  time.Now().Add(hostValidity),
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	// Support both IP addresses and DNS names.
	if ip := net.ParseIP(hostname); ip != nil {
		certTemplate.IPAddresses = []net.IP{ip}
	} else {
		certTemplate.DNSNames = []string{hostname}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, certTemplate, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("cert: host cert creation failed for %s: %w", hostname, err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{derBytes, ca.cert.Raw},
		PrivateKey:  key,
	}

	// Parse and attach for field access (e.g. in tests).
	leaf, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, fmt.Errorf("cert: host cert parse failed: %w", err)
	}
	tlsCert.Leaf = leaf

	return tlsCert, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("cert: serial generation failed: %w", err)
	}
	return serial, nil
}

func writePEM(path, pemType string, derBytes []byte, mode os.FileMode) error {
	pemFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("cert: open %s for write: %w", path, err)
	}
	defer pemFile.Close()
	return pem.Encode(pemFile, &pem.Block{Type: pemType, Bytes: derBytes})
}

func encodeCertPEM(derBytes []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
