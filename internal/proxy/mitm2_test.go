package proxy_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/http2"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// h2UpstreamServer starts an HTTP/2-enabled TLS test server.
func h2UpstreamServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{
		NextProtos: []string{"h2", "http/1.1"},
	}
	h2s := &http2.Server{}
	require.NoError(t, http2.ConfigureServer(srv.Config, h2s))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// h2ClientOverTunnel wraps a raw conn in a TLS client that negotiates HTTP/2
// and returns an *http.Client that routes all requests over that connection.
func h2ClientOverTunnel(t *testing.T, conn net.Conn, serverName string) *http.Client {
	t.Helper()
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true, //nolint:gosec — test only
		NextProtos:         []string{"h2"},
	})
	require.NoError(t, tlsConn.Handshake())
	assert.Equal(t, "h2", tlsConn.ConnectionState().NegotiatedProtocol, "proxy must negotiate h2 with the client")
	t.Cleanup(func() { tlsConn.Close() })

	transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			return tlsConn, nil
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}

	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestMITM_H2_ForwardsGET(t *testing.T) {
	st := newTestStore(t)

	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "h2 works")
	}))

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	client := h2ClientOverTunnel(t, conn, host)

	resp, err := client.Get("https://" + targetHost + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "h2 works", string(b))
}

func TestMITM_H2_LogsTransaction(t *testing.T) {
	st := newTestStore(t)

	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "logged")
	}))

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	client := h2ClientOverTunnel(t, conn, host)

	resp, err := client.Get("https://" + targetHost + "/log-test")
	require.NoError(t, err)
	resp.Body.Close()

	tx := waitForTransaction(t, st)
	assert.True(t, tx.TLS)
	assert.Equal(t, "GET", tx.Method)
	assert.Equal(t, http.StatusOK, tx.StatusCode)
}

func TestMITM_H2_PostWithBody(t *testing.T) {
	st := newTestStore(t)

	var receivedBody string
	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	client := h2ClientOverTunnel(t, conn, host)

	body := `{"key":"value"}`
	req, err := http.NewRequest(http.MethodPost, "https://"+targetHost+"/api", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, body, receivedBody)
}

func TestMITM_H2_CacheHeadersStripped(t *testing.T) {
	st := newTestStore(t)

	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(http.StatusOK)
	}))

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	client := h2ClientOverTunnel(t, conn, host)

	resp, err := client.Get("https://" + targetHost + "/")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Empty(t, resp.Header.Get("ETag"))
	assert.Equal(t, "no-store, no-cache, must-revalidate", resp.Header.Get("Cache-Control"))
}

func TestMITM_H2_InterceptDrop_Returns403(t *testing.T) {
	st := newTestStore(t)

	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "should not reach")
	}))

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
	client := h2ClientOverTunnel(t, conn, host)

	resp, err := client.Get("https://" + targetHost + "/secret")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMITM_H2_InterceptForward_ModifiesRequest(t *testing.T) {
	st := newTestStore(t)

	var receivedHeader string
	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Injected")
		w.WriteHeader(http.StatusOK)
	}))

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	st.Intercept.SetEnabled(true)
	go func() {
		pending := <-st.Intercept.Queue()
		pending.Request.Header.Set("X-Injected", "h2-injected")
		pending.Reply <- store.Decision{
			Forward: true,
			Request: pending.Request,
			Body:    pending.Body,
		}
	}()

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	client := h2ClientOverTunnel(t, conn, host)

	resp, err := client.Get("https://" + targetHost + "/inject")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "h2-injected", receivedHeader)
}

func TestMITM_H2_MultipleConcurrentStreams(t *testing.T) {
	st := newTestStore(t)

	upstream := h2UpstreamServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path:%s", r.URL.Path)
	}))

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	client := h2ClientOverTunnel(t, conn, host)

	results := make(chan string, 3)
	for _, path := range []string{"/a", "/b", "/c"} {
		p := path
		go func() {
			resp, err := client.Get("https://" + targetHost + p)
			if err != nil {
				results <- "error"
				return
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			results <- string(b)
		}()
	}

	got := make(map[string]bool)
	for i := 0; i < 3; i++ {
		got[<-results] = true
	}
	assert.True(t, got["path:/a"])
	assert.True(t, got["path:/b"])
	assert.True(t, got["path:/c"])
}
