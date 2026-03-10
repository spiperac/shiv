package cert_test

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shiv/internal/cert"
)

// newTestCA creates a fresh CA in a temp dir and returns it along with
// the dir path. The caller owns cleanup via t.Cleanup.
func newTestCA(t *testing.T) (*cert.CA, string) {
	t.Helper()
	dir := t.TempDir()
	ca, err := cert.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	return ca, dir
}

// -----------------------------------------------------------------------
// CA generation
// -----------------------------------------------------------------------

func TestCAGeneration(t *testing.T) {
	ca, dir := newTestCA(t)

	// Files must exist on disk.
	for _, name := range []string{"ca.key", "ca.crt"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", name, err)
		}
	}

	x := ca.X509Cert()
	if x == nil {
		t.Fatal("X509Cert() returned nil")
	}
	if !x.IsCA {
		t.Error("CA cert must have IsCA = true")
	}
	if x.Subject.CommonName != "Shiv Local CA" {
		t.Errorf("unexpected CN: %q", x.Subject.CommonName)
	}
	if time.Until(x.NotAfter) < 9*365*24*time.Hour {
		t.Errorf("CA cert validity too short: expires %v", x.NotAfter)
	}
}

// -----------------------------------------------------------------------
// CA persistence — load from existing files
// -----------------------------------------------------------------------

func TestCAPersistence(t *testing.T) {
	_, dir := newTestCA(t)

	// Load again from the same directory — must succeed without regenerating.
	ca2, err := cert.LoadFromDir(dir)
	if err != nil {
		t.Fatalf("second LoadFromDir: %v", err)
	}

	// Serial numbers must match — same cert was loaded, not regenerated.
	_, dir2 := newTestCA(t)
	ca3, err := cert.LoadFromDir(dir2)
	if err != nil {
		t.Fatalf("third LoadFromDir (fresh dir): %v", err)
	}

	x2 := ca2.X509Cert()
	x3 := ca3.X509Cert()

	// Same dir → same serial. Different dir → different serial (fresh CA).
	// Load ca again from dir for serial comparison.
	ca1reload, _ := cert.LoadFromDir(dir)
	x1reload := ca1reload.X509Cert()

	if x1reload.SerialNumber.Cmp(x2.SerialNumber) != 0 {
		t.Errorf("reloaded CA has different serial: got %v want %v",
			x2.SerialNumber, x1reload.SerialNumber)
	}
	if x2.SerialNumber.Cmp(x3.SerialNumber) == 0 {
		t.Error("independent CAs should have different serials")
	}
}

// -----------------------------------------------------------------------
// Per-host cert — basic properties
// -----------------------------------------------------------------------

func TestHostCertProperties(t *testing.T) {
	ca, _ := newTestCA(t)

	hostnames := []string{"example.com", "sub.example.com", "localhost"}
	for _, h := range hostnames {
		t.Run(h, func(t *testing.T) {
			tlsCert, err := ca.TLSCertForHost(h)
			if err != nil {
				t.Fatalf("TLSCertForHost(%q): %v", h, err)
			}

			leaf := tlsCert.Leaf
			if leaf == nil {
				t.Fatal("Leaf is nil")
			}
			if leaf.Subject.CommonName != h {
				t.Errorf("CN = %q, want %q", leaf.Subject.CommonName, h)
			}

			found := false
			for _, dns := range leaf.DNSNames {
				if dns == h {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("hostname %q not in DNSNames %v", h, leaf.DNSNames)
			}

			if time.Until(leaf.NotAfter) < 364*24*time.Hour {
				t.Errorf("host cert validity too short: expires %v", leaf.NotAfter)
			}
		})
	}
}

// -----------------------------------------------------------------------
// Per-host cert — IP address
// -----------------------------------------------------------------------

func TestHostCertIP(t *testing.T) {
	ca, _ := newTestCA(t)

	tlsCert, err := ca.TLSCertForHost("127.0.0.1")
	if err != nil {
		t.Fatalf("TLSCertForHost(127.0.0.1): %v", err)
	}
	leaf := tlsCert.Leaf
	if len(leaf.IPAddresses) == 0 {
		t.Fatal("expected IP in IPAddresses, got none")
	}
	if leaf.IPAddresses[0].String() != "127.0.0.1" {
		t.Errorf("got IP %v, want 127.0.0.1", leaf.IPAddresses[0])
	}
	if len(leaf.DNSNames) != 0 {
		t.Errorf("IP cert should have no DNSNames, got %v", leaf.DNSNames)
	}
}

// -----------------------------------------------------------------------
// Chain validation — the acceptance criterion from the design doc
// -----------------------------------------------------------------------

func TestCertChainValidation(t *testing.T) {
	ca, _ := newTestCA(t)

	tlsCert, err := ca.TLSCertForHost("example.com")
	if err != nil {
		t.Fatalf("TLSCertForHost: %v", err)
	}

	// Build a cert pool containing only our CA — simulates what a browser
	// with the CA installed would do.
	pool := x509.NewCertPool()
	pool.AddCert(ca.X509Cert())

	opts := x509.VerifyOptions{
		DNSName: "example.com",
		Roots:   pool,
	}

	leaf := tlsCert.Leaf
	if _, err := leaf.Verify(opts); err != nil {
		t.Errorf("chain verification failed: %v", err)
	}
}

// -----------------------------------------------------------------------
// TLS handshake — end-to-end using net/tls in-process
// -----------------------------------------------------------------------

func TestTLSHandshake(t *testing.T) {
	ca, _ := newTestCA(t)

	serverTLSCert, err := ca.TLSCertForHost("testhost.local")
	if err != nil {
		t.Fatalf("TLSCertForHost: %v", err)
	}
	serverCfg := &tls.Config{
		Certificates: []tls.Certificate{*serverTLSCert},
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca.X509Cert())
	clientCfg := &tls.Config{
		RootCAs:    pool,
		ServerName: "testhost.local",
	}

	// tls.Dial inside newTLSPipe already completes the handshake.
	// If it returns without error, the chain validated successfully.
	serverConn, clientConn := newTLSPipe(t, serverCfg, clientCfg)
	defer serverConn.Close()
	defer clientConn.Close()
}

// newTLSPipe creates an in-process TLS client/server pair over TCP loopback.
func newTLSPipe(t *testing.T, serverCfg, clientCfg *tls.Config) (*tls.Conn, *tls.Conn) {
	t.Helper()

	ch := make(chan *tls.Conn, 1)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- nil
			return
		}
		tlsConn := conn.(*tls.Conn)
		tlsConn.Handshake() // complete server side
		ch <- tlsConn
	}()

	client, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}

	server := <-ch
	if server == nil {
		t.Fatal("server accept failed")
	}

	return server, client
}

// -----------------------------------------------------------------------
// Cache — same *tls.Certificate pointer returned for same host
// -----------------------------------------------------------------------

func TestHostCertCache(t *testing.T) {
	ca, _ := newTestCA(t)

	first, err := ca.TLSCertForHost("cache-test.com")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := ca.TLSCertForHost("cache-test.com")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if first != second {
		t.Error("expected same *tls.Certificate pointer from cache")
	}
}

// -----------------------------------------------------------------------
// Cache — concurrent access does not race or duplicate
// -----------------------------------------------------------------------

func TestHostCertCacheConcurrent(t *testing.T) {
	ca, _ := newTestCA(t)

	const goroutines = 20
	results := make([]*tls.Certificate, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := ca.TLSCertForHost("concurrent.example.com")
			if err != nil {
				t.Errorf("goroutine %d: %v", i, err)
				return
			}
			results[i] = c
		}()
	}
	wg.Wait()

	for i, c := range results {
		if c == nil {
			t.Errorf("goroutine %d returned nil", i)
			continue
		}
		if c != results[0] {
			t.Errorf("goroutine %d returned different pointer", i)
		}
	}
}

// -----------------------------------------------------------------------
// Host + port — port should be stripped before cert lookup
// -----------------------------------------------------------------------

func TestHostCertStripsPort(t *testing.T) {
	ca, _ := newTestCA(t)

	withPort, err := ca.TLSCertForHost("strip-test.com:443")
	if err != nil {
		t.Fatalf("with port: %v", err)
	}
	withoutPort, err := ca.TLSCertForHost("strip-test.com")
	if err != nil {
		t.Fatalf("without port: %v", err)
	}

	if withPort != withoutPort {
		t.Error("port stripping broken: different certs returned for same host")
	}

	if withPort.Leaf.Subject.CommonName != "strip-test.com" {
		t.Errorf("CN should be bare hostname, got %q", withPort.Leaf.Subject.CommonName)
	}
}

// -----------------------------------------------------------------------
// CertPEM — returned bytes are valid PEM
// -----------------------------------------------------------------------

func TestCertPEM(t *testing.T) {
	ca, _ := newTestCA(t)

	pem, err := ca.CertPEM()
	if err != nil {
		t.Fatalf("CertPEM: %v", err)
	}
	if len(pem) == 0 {
		t.Fatal("CertPEM returned empty bytes")
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Error("AppendCertsFromPEM failed — invalid PEM")
	}
}
