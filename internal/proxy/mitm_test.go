package proxy_test

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startProxyServer starts the proxy as a real TCP listener and returns its address.
// httptest.NewRecorder does not support hijacking so we need a real listener for CONNECT.
func startProxyServer(t *testing.T, st *store.Store) string {
	t.Helper()
	p := newTestProxy(t, st)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{Handler: p}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	return ln.Addr().String()
}

// tlsUpstream starts an httptest TLS server.
func tlsUpstream(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// doConnectTunnel sends CONNECT to the proxy and returns the raw conn after the 200 response.
func doConnectTunnel(t *testing.T, proxyAddr, targetHost string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetHost, targetHost)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "proxy must respond 200 to CONNECT")

	return conn
}

// tlsOverTunnel wraps conn in a TLS client trusting the proxy's self-signed cert.
func tlsOverTunnel(t *testing.T, conn net.Conn, serverName string) *tls.Conn {
	t.Helper()
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, //nolint:gosec — test only
	})
	require.NoError(t, tlsConn.Handshake())
	t.Cleanup(func() { tlsConn.Close() })
	return tlsConn
}

// sendHTTPOverConn writes a raw HTTP/1.1 request and reads the response.
func sendHTTPOverConn(t *testing.T, conn io.ReadWriter, method, path, host, body string) *http.Response {
	t.Helper()
	var req strings.Builder
	fmt.Fprintf(&req, "%s %s HTTP/1.1\r\n", method, path)
	fmt.Fprintf(&req, "Host: %s\r\n", host)
	if body != "" {
		fmt.Fprintf(&req, "Content-Length: %d\r\n", len(body))
	}
	req.WriteString("Connection: close\r\n\r\n")
	if body != "" {
		req.WriteString(body)
	}

	_, err := io.WriteString(conn, req.String())
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	return resp
}

// waitForTransaction blocks until a transaction appears on st.Updates or times out.
func waitForTransaction(t *testing.T, st *store.Store) store.Transaction {
	t.Helper()
	select {
	case tx := <-st.Updates:
		return tx
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for transaction to be logged")
		return store.Transaction{}
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestMITM_ConnectTunnel_ForwardsRequest(t *testing.T) {
	st := newTestStore(t)
	upstream := tlsUpstream(t, http.StatusOK, "mitm works")

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tlsOverTunnel(t, conn, host)

	resp := sendHTTPOverConn(t, tlsConn, "GET", "/", targetHost, "")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "mitm works", string(b))
}

func TestMITM_ConnectTunnel_LogsTransactionWithTLSTrue(t *testing.T) {
	st := newTestStore(t)
	upstream := tlsUpstream(t, http.StatusOK, "logged")

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tlsOverTunnel(t, conn, host)

	resp := sendHTTPOverConn(t, tlsConn, "GET", "/log-test", targetHost, "")
	resp.Body.Close()

	tx := waitForTransaction(t, st)
	assert.True(t, tx.TLS, "transaction must be flagged as TLS")
	assert.Equal(t, "GET", tx.Method)
	assert.Equal(t, http.StatusOK, tx.StatusCode)
}

func TestMITM_ConnectTunnel_CacheHeadersStripped(t *testing.T) {
	st := newTestStore(t)

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tlsOverTunnel(t, conn, host)

	resp := sendHTTPOverConn(t, tlsConn, "GET", "/", targetHost, "")
	resp.Body.Close()

	assert.Empty(t, resp.Header.Get("ETag"))
	assert.Equal(t, "no-store, no-cache, must-revalidate", resp.Header.Get("Cache-Control"))
}

func TestMITM_ConnectTunnel_InterceptDrop_Returns403(t *testing.T) {
	st := newTestStore(t)
	upstream := tlsUpstream(t, http.StatusOK, "should not reach")

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	st.Intercept.SetEnabled(true)
	go func() {
		pending := <-st.Intercept.Queue()
		pending.Reply <- store.Decision{Forward: false}
	}()

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tlsOverTunnel(t, conn, host)

	resp := sendHTTPOverConn(t, tlsConn, "GET", "/secret", targetHost, "")
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMITM_ConnectTunnel_InterceptForward_ModifiesRequest(t *testing.T) {
	st := newTestStore(t)

	var receivedHeader string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Injected")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	st.Intercept.SetEnabled(true)
	go func() {
		pending := <-st.Intercept.Queue()
		pending.Request.Header.Set("X-Injected", "mitm-injected")
		pending.Reply <- store.Decision{
			Forward: true,
			Request: pending.Request,
			Body:    pending.Body,
		}
	}()

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tlsOverTunnel(t, conn, host)

	resp := sendHTTPOverConn(t, tlsConn, "GET", "/inject", targetHost, "")
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "mitm-injected", receivedHeader)
}

func TestMITM_ConnectTunnel_PostWithBody(t *testing.T) {
	st := newTestStore(t)

	var receivedBody string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(upstream.Close)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tlsOverTunnel(t, conn, host)

	body := `{"key":"value"}`
	var req bytes.Buffer
	fmt.Fprintf(&req, "POST /api HTTP/1.1\r\n")
	fmt.Fprintf(&req, "Host: %s\r\n", targetHost)
	fmt.Fprintf(&req, "Content-Type: application/json\r\n")
	fmt.Fprintf(&req, "Content-Length: %d\r\n", len(body))
	fmt.Fprintf(&req, "Connection: close\r\n\r\n")
	fmt.Fprintf(&req, "%s", body)
	_, err = tlsConn.Write(req.Bytes())
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), nil)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, body, receivedBody)
}
