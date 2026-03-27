package ui

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"testing"

	internalhttp "github.com/shiv/internal/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseHostFromRaw ──────────────────────────────────────────────────────────

func TestParseHostFromRaw_HostOnly(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 443, port)
	assert.True(t, useTLS)
}

func TestParseHostFromRaw_HostWithPort80(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com:80\r\n\r\n"
	host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 80, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_HostWithPort443(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com:443\r\n\r\n"
	host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 443, port)
	assert.True(t, useTLS)
}

func TestParseHostFromRaw_CustomPort(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com:8080\r\n\r\n"
	host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 8080, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_NoHostHeader(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nContent-Type: text/plain\r\n\r\n"
	host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
	assert.Empty(t, host)
	assert.Equal(t, 0, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_CaseInsensitive(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHOST: example.com\r\n\r\n"
	host, _, _ := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
}

func TestParseHostFromRaw_LFOnly(t *testing.T) {
	raw := "GET / HTTP/1.1\nHost: example.com\n\n"
	host, port, _ := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 443, port)
}

// ── Decompress ────────────────────────────────────────────────────────────────

func TestDecompressRepeaterBody_Gzip(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write([]byte("hello gzip"))
	require.NoError(t, err)
	w.Close()

	hdr := http.Header{"Content-Encoding": []string{"gzip"}}
	out := internalhttp.Decompress(hdr, buf.Bytes())
	assert.Equal(t, "hello gzip", string(out))
}

func TestDecompressRepeaterBody_NoEncoding(t *testing.T) {
	body := []byte("plain body")
	out := internalhttp.Decompress(http.Header{}, body)
	assert.Equal(t, body, out)
}

func TestDecompressRepeaterBody_BadGzip(t *testing.T) {
	body := []byte("not gzip data")
	hdr := http.Header{"Content-Encoding": []string{"gzip"}}
	out := internalhttp.Decompress(hdr, body)
	assert.Equal(t, body, out)
}

// ── SendRaw ───────────────────────────────────────────────────────────────────

func startMockHTTPServer(t *testing.T, response string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		fmt.Fprint(conn, response)
	}()

	t.Cleanup(func() { ln.Close() })

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func TestSendRawRequest_200OK(t *testing.T) {
	body := "Hello, World!"
	mockResp := fmt.Sprintf(
		"HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s",
		len(body), body,
	)
	host, port := startMockHTTPServer(t, mockResp)

	raw := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
	result, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host: host, Port: port, TLS: false, RawReq: raw,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Raw, "200 OK")
	assert.Contains(t, result.Raw, "Hello, World!")
}

func TestSendRawRequest_404(t *testing.T) {
	mockResp := "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"
	host, port := startMockHTTPServer(t, mockResp)

	raw := fmt.Sprintf("GET /missing HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
	result, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host: host, Port: port, TLS: false, RawReq: raw,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Raw, "404")
}

func TestSendRawRequest_WithBody(t *testing.T) {
	mockResp := "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nOK"
	host, port := startMockHTTPServer(t, mockResp)

	raw := fmt.Sprintf(
		"POST /api HTTP/1.1\r\nHost: %s:%d\r\nContent-Type: application/json\r\n\r\n{\"key\":\"val\"}",
		host, port,
	)
	result, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host: host, Port: port, TLS: false, RawReq: raw,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Raw, "200 OK")
}

func TestSendRawRequest_ConnectionRefused(t *testing.T) {
	_, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host: "127.0.0.1", Port: 1, TLS: false,
		RawReq: "GET / HTTP/1.1\r\nHost: 127.0.0.1:1\r\n\r\n",
	})
	assert.Error(t, err)
}

func TestSendRawRequest_StripAcceptEncoding(t *testing.T) {
	received := make(chan string, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		received <- string(buf[:n])
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	}()

	addr := ln.Addr().(*net.TCPAddr)
	raw := fmt.Sprintf(
		"GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nAccept-Encoding: gzip, deflate\r\n\r\n",
		addr.Port,
	)
	internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host: "127.0.0.1", Port: addr.Port, TLS: false, RawReq: raw,
	})

	req := <-received
	assert.NotContains(t, req, "Accept-Encoding")
}
