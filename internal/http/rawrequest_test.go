package http

import (
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseHostFromRaw ──────────────────────────────────────────────────────────

func TestParseHostFromRaw_HostOnly(t *testing.T) {
	host, port, useTLS := ParseHostFromRaw("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 443, port)
	assert.True(t, useTLS)
}

func TestParseHostFromRaw_HostWithPort443(t *testing.T) {
	host, port, useTLS := ParseHostFromRaw("GET / HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 443, port)
	assert.True(t, useTLS)
}

func TestParseHostFromRaw_HostWithPort80(t *testing.T) {
	host, port, useTLS := ParseHostFromRaw("GET / HTTP/1.1\r\nHost: example.com:80\r\n\r\n")
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 80, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_CustomPort(t *testing.T) {
	host, port, useTLS := ParseHostFromRaw("GET / HTTP/1.1\r\nHost: example.com:8080\r\n\r\n")
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 8080, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_IP(t *testing.T) {
	host, port, useTLS := ParseHostFromRaw("GET / HTTP/1.1\r\nHost: 1.2.3.4\r\n\r\n")
	assert.Equal(t, "1.2.3.4", host)
	assert.Equal(t, 80, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_NoHostHeader(t *testing.T) {
	host, port, useTLS := ParseHostFromRaw("GET / HTTP/1.1\r\nContent-Type: text/plain\r\n\r\n")
	assert.Empty(t, host)
	assert.Equal(t, 0, port)
	assert.False(t, useTLS)
}

func TestParseHostFromRaw_CaseInsensitive(t *testing.T) {
	host, _, _ := ParseHostFromRaw("GET / HTTP/1.1\r\nHOST: example.com\r\n\r\n")
	assert.Equal(t, "example.com", host)
}

func TestParseHostFromRaw_LFOnly(t *testing.T) {
	host, port, _ := ParseHostFromRaw("GET / HTTP/1.1\nHost: example.com\n\n")
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 443, port)
}

// ── ParseRawHeaders ───────────────────────────────────────────────────────────

func TestParseRawHeaders_Single(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n"
	h := ParseRawHeaders(raw)
	assert.Equal(t, "text/html", h.Get("Content-Type"))
}

func TestParseRawHeaders_Multiple(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nX-Custom: value\r\n\r\n"
	h := ParseRawHeaders(raw)
	assert.Equal(t, "text/html", h.Get("Content-Type"))
	assert.Equal(t, "value", h.Get("X-Custom"))
}

func TestParseRawHeaders_StopsAtBlankLine(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\nbody here"
	h := ParseRawHeaders(raw)
	assert.Equal(t, "text/html", h.Get("Content-Type"))
	assert.Empty(t, h.Get("body here"))
}

func TestParseRawHeaders_Empty(t *testing.T) {
	h := ParseRawHeaders("HTTP/1.1 200 OK\r\n\r\n")
	assert.Equal(t, http.Header{}, h)
}

// ── normalizeLineEndings ──────────────────────────────────────────────────────

func TestNormalizeLineEndings_LFtoCRLF(t *testing.T) {
	out := normalizeLineEndings("GET / HTTP/1.1\nHost: example.com\n\n")
	assert.Contains(t, out, "\r\n")
	assert.NotContains(t, out, "\n\n")
}

func TestNormalizeLineEndings_AlreadyCRLF(t *testing.T) {
	in := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	out := normalizeLineEndings(in)
	assert.Equal(t, in, out)
}

func TestNormalizeLineEndings_FoldsContinuation(t *testing.T) {
	in := "GET / HTTP/1.1\r\nHost: example.com\r\n continued\r\n\r\n"
	out := normalizeLineEndings(in)
	assert.NotContains(t, out, "\r\n continued")
	assert.Contains(t, out, " continued")
}

// ── rewriteHeaders ────────────────────────────────────────────────────────────

func TestRewriteHeaders_StripsAcceptEncoding(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\nAccept-Encoding: gzip\r\n\r\n"
	out := rewriteHeaders(raw, nil)
	assert.NotContains(t, out, "Accept-Encoding")
}

func TestRewriteHeaders_StripsContentLength(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: example.com\r\nContent-Length: 999\r\n\r\nbody"
	out := rewriteHeaders(raw, nil)
	assert.NotContains(t, out, "Content-Length: 999")
	assert.Contains(t, out, fmt.Sprintf("Content-Length: %d", len("body")))
}

func TestRewriteHeaders_PreservesCookieWhenNoJar(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\nCookie: session=abc\r\n\r\n"
	out := rewriteHeaders(raw, nil)
	assert.Contains(t, out, "Cookie: session=abc")
}

func TestRewriteHeaders_ReplacesCookieFromJar(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\nCookie: session=old\r\n\r\n"
	out := rewriteHeaders(raw, map[string]string{"session": "new"})
	assert.NotContains(t, out, "session=old")
	assert.Contains(t, out, "session=new")
}

func TestRewriteHeaders_StripsDefaultPort443(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com:443\r\n\r\n"
	out := rewriteHeaders(raw, nil)
	assert.Contains(t, out, "Host: example.com\r\n")
	assert.NotContains(t, out, "Host: example.com:443")
}

func TestRewriteHeaders_StripsDefaultPort80(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com:80\r\n\r\n"
	out := rewriteHeaders(raw, nil)
	assert.Contains(t, out, "Host: example.com\r\n")
	assert.NotContains(t, out, "Host: example.com:80")
}

func TestRewriteHeaders_KeepsNonDefaultPort(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com:8080\r\n\r\n"
	out := rewriteHeaders(raw, nil)
	assert.Contains(t, out, "Host: example.com:8080")
}

// ── SendRaw (mock TCP server) ─────────────────────────────────────────────────

func startMockServer(t *testing.T, response string) (string, int) {
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

func TestSendRaw_200OK(t *testing.T) {
	host, port := startMockServer(t, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello")
	raw := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
	result, err := SendRaw(RawRequestOptions{Host: host, Port: port, RawReq: raw})
	require.NoError(t, err)
	assert.Equal(t, 200, result.StatusCode)
	assert.Equal(t, "hello", string(result.Body))
}

func TestSendRaw_404(t *testing.T) {
	host, port := startMockServer(t, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
	raw := fmt.Sprintf("GET /missing HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
	result, err := SendRaw(RawRequestOptions{Host: host, Port: port, RawReq: raw})
	require.NoError(t, err)
	assert.Equal(t, 404, result.StatusCode)
}

func TestSendRaw_ConnectionRefused(t *testing.T) {
	_, err := SendRaw(RawRequestOptions{
		Host: "127.0.0.1", Port: 1,
		RawReq: "GET / HTTP/1.1\r\nHost: 127.0.0.1:1\r\n\r\n",
	})
	assert.Error(t, err)
}

func TestSendRaw_CookieJarApplied(t *testing.T) {
	received := make(chan string, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		received <- string(buf[:n])
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	}()
	addr := ln.Addr().(*net.TCPAddr)
	raw := fmt.Sprintf("GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", addr.Port)
	SendRaw(RawRequestOptions{
		Host: "127.0.0.1", Port: addr.Port,
		RawReq:    raw,
		CookieJar: map[string]string{"session": "abc123"},
	})
	req := <-received
	assert.Contains(t, req, "Cookie: session=abc123")
}

func TestSendRaw_AcceptEncodingStripped(t *testing.T) {
	received := make(chan string, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		received <- string(buf[:n])
		fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	}()
	addr := ln.Addr().(*net.TCPAddr)
	raw := fmt.Sprintf("GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nAccept-Encoding: gzip\r\n\r\n", addr.Port)
	SendRaw(RawRequestOptions{Host: "127.0.0.1", Port: addr.Port, RawReq: raw})
	req := <-received
	assert.NotContains(t, req, "Accept-Encoding")
}
