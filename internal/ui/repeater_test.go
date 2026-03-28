package ui

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	internalhttp "github.com/shiv/internal/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseHostFromRaw ──────────────────────────────────────────────────────────

func TestParseHostFromRaw_HostOnly(t *testing.T) {
	raw := "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"
	host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
	assert.Equal(t, "example.com", host)
	assert.Equal(t, 80, port)
	assert.False(t, useTLS)
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
	assert.Equal(t, 80, port)
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

func TestSendRawRequest_MultipleRequests_IndependentConnections(t *testing.T) {
	// Verify that two sequential SendRaw calls use independent connections
	// and both get correct responses.
	responses := []string{
		"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nfirst",
		"HTTP/1.1 201 Created\r\nContent-Length: 6\r\n\r\nsecond",
	}

	for i, expected := range responses {
		host, port := startMockHTTPServer(t, expected)
		raw := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port)
		result, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
			Host: host, Port: port, TLS: false, RawReq: raw,
		})
		require.NoError(t, err, "request %d failed", i)
		if i == 0 {
			assert.Contains(t, result.Raw, "200")
			assert.Contains(t, result.Raw, "first")
		} else {
			assert.Contains(t, result.Raw, "201")
			assert.Contains(t, result.Raw, "second")
		}
	}
}

func TestSendRawRequest_CookieJarApplied(t *testing.T) {
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
	raw := fmt.Sprintf("GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", addr.Port)
	internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host:      "127.0.0.1",
		Port:      addr.Port,
		TLS:       false,
		RawReq:    raw,
		CookieJar: map[string]string{"session": "abc123", "user": "test"},
	})

	req := <-received
	assert.Contains(t, req, "Cookie:")
	assert.Contains(t, req, "session=abc123")
}

// ── WebSocket send via repeater ───────────────────────────────────────────────

// wsEchoServer starts a plain (non-TLS) WebSocket echo server for repeater tests.
func wsEchoServer(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	// Convert http:// to ws://
	return strings.Replace(srv.URL, "http://", "ws://", 1)
}

func TestRepeaterWS_SendRaw_EchoesMessage(t *testing.T) {
	wsURL := wsEchoServer(t)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL+"/", nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("repeater test")))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "repeater test", string(payload))
}

func TestRepeaterWS_SendRaw_MultipleMessages(t *testing.T) {
	wsURL := wsEchoServer(t)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL+"/", nil)
	require.NoError(t, err)
	defer conn.Close()

	messages := []string{"first", "second", "third"}
	for _, msg := range messages {
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(msg)))
		_, payload, err := conn.ReadMessage()
		require.NoError(t, err)
		assert.Equal(t, msg, string(payload))
	}
}

func TestRepeaterWS_SendRaw_EmptyMessage(t *testing.T) {
	wsURL := wsEchoServer(t)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL+"/", nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("")))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "", string(payload))
}

func TestRepeaterWS_SendRaw_BinaryMessage(t *testing.T) {
	wsURL := wsEchoServer(t)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL+"/", nil)
	require.NoError(t, err)
	defer conn.Close()

	payload := []byte{0x00, 0x01, 0x02, 0xFF}
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, payload))
	msgType, received, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, msgType)
	assert.Equal(t, payload, received)
}

func TestRepeaterWS_ConnectionRefused(t *testing.T) {
	dialer := websocket.Dialer{}
	_, _, err := dialer.Dial("ws://127.0.0.1:1/", nil)
	assert.Error(t, err)
}

func TestRepeaterWS_CleanClose(t *testing.T) {
	wsURL := wsEchoServer(t)

	dialer := websocket.Dialer{}
	conn, _, err := dialer.Dial(wsURL+"/", nil)
	require.NoError(t, err)

	// Send a message, receive echo, then close cleanly.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("bye")))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "bye", string(payload))

	// Clean close.
	err = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	require.NoError(t, err)
	conn.Close()
}
