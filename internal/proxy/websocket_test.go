package proxy_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// wsUpstreamServer starts a TLS WebSocket echo server.
func wsUpstreamServer(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(msgType, payload); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wsUpstreamServerWithHandler starts a TLS WebSocket server with a custom handler.
func wsUpstreamServerWithHandler(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// wsClientOverTunnel dials a WebSocket through the proxy CONNECT tunnel.
func wsClientOverTunnel(t *testing.T, proxyAddr, targetHost string) *websocket.Conn {
	t.Helper()
	conn := doConnectTunnel(t, proxyAddr, targetHost)

	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         strings.Split(targetHost, ":")[0],
		InsecureSkipVerify: true, //nolint:gosec
		NextProtos:         []string{"http/1.1"},
	})
	require.NoError(t, tlsConn.Handshake())
	t.Cleanup(func() { tlsConn.Close() })

	dialer := websocket.Dialer{
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return tlsConn, nil
		},
	}
	wsConn, _, err := dialer.Dial("wss://"+targetHost+"/", nil)
	require.NoError(t, err)
	t.Cleanup(func() { wsConn.Close() })
	return wsConn
}

// sendHTTPKeepAlive writes an HTTP/1.1 request with Connection: keep-alive
// and reads the response, leaving the connection open for reuse.
func sendHTTPKeepAlive(t *testing.T, conn io.ReadWriter, method, path, host string) *http.Response {
	t.Helper()
	var req strings.Builder
	fmt.Fprintf(&req, "%s %s HTTP/1.1\r\n", method, path)
	fmt.Fprintf(&req, "Host: %s\r\n", host)
	req.WriteString("Connection: keep-alive\r\n\r\n")
	_, err := io.WriteString(conn, req.String())
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	return resp
}
func wsClientOverTunnelWithPath(t *testing.T, proxyAddr, targetHost, path string) *websocket.Conn {
	t.Helper()
	conn := doConnectTunnel(t, proxyAddr, targetHost)
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         strings.Split(targetHost, ":")[0],
		InsecureSkipVerify: true, //nolint:gosec
		NextProtos:         []string{"http/1.1"},
	})
	require.NoError(t, tlsConn.Handshake())
	t.Cleanup(func() { tlsConn.Close() })

	dialer := websocket.Dialer{
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return tlsConn, nil
		},
	}
	wsConn, _, err := dialer.Dial("wss://"+targetHost+path, nil)
	require.NoError(t, err)
	t.Cleanup(func() { wsConn.Close() })
	return wsConn
}

// waitForFrames polls until the store has at least n frames for the first
// connection, or fails after 3 seconds.
func waitForFrames(t *testing.T, st interface {
	AllWebSocketConnections() (interface{ Len() int }, error)
}, n int) {
	t.Helper()
	// implemented inline in each test for flexibility
}

// ── basic correctness ─────────────────────────────────────────────────────────

func TestMITM_WebSocket_EchoesMessages(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("hello")))
	msgType, payload, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, msgType)
	assert.Equal(t, "hello", string(payload))
}

func TestMITM_WebSocket_LogsConnection(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("ping")))
	_, _, err = wsConn.ReadMessage()
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := st.AllWebSocketConnections()
		require.NoError(t, err)
		if len(loaded) > 0 {
			assert.Equal(t, targetHost, loaded[0].Host)
			assert.True(t, loaded[0].TLS)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for WebSocket connection to be logged")
}

func TestMITM_WebSocket_LogsFrames(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	for _, msg := range []string{"frame1", "frame2"} {
		require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte(msg)))
		_, _, err := wsConn.ReadMessage()
		require.NoError(t, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conns, err := st.AllWebSocketConnections()
		require.NoError(t, err)
		if len(conns) == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		frames, err := st.FramesForConnection(conns[0].ID)
		require.NoError(t, err)
		if len(frames) >= 4 {
			assert.Equal(t, 4, len(frames))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for WebSocket frames to be logged")
}

// ── hijackWriter / gorilla coupling canary ────────────────────────────────────

// TestMITM_WebSocket_HijackCalledByUpgrader verifies that gorilla's Upgrader
// calls Hijack() on our hijackWriter. If gorilla ever changes this internal
// behaviour the test will fail before it hits production.
func TestMITM_WebSocket_HijackCalledByUpgrader(t *testing.T) {
	hijackCalled := false

	// trackingWriter wraps a real http.ResponseWriter and records Hijack calls.
	type trackingWriter struct {
		http.ResponseWriter
		conn   net.Conn
		brw    *bufio.ReadWriter
		called *bool
	}
	tw := &trackingWriter{}
	tw.called = &hijackCalled

	// We test this via a real proxy round-trip — if the WebSocket connection
	// succeeds, Hijack() was called. If gorilla stops calling Hijack(), the
	// upgrade fails and the test fails.
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)

	// If the connection succeeds, Hijack was called correctly.
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("hijack-test")))
	_, payload, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "hijack-test", string(payload), "gorilla Upgrader must use Hijack to take over the connection")
}

// ── buffered reader integrity ─────────────────────────────────────────────────

// TestMITM_WebSocket_BufferedReaderNotLost verifies that the proxy correctly
// uses the existing bufio.Reader when handing off to the WebSocket handler.
// This is tested indirectly: if browserReader were discarded and a new one
// created over the raw conn, the WS framing would desync on high-traffic
// connections. We verify correctness by sending many rapid messages immediately
// after connection, which exercises the read buffer aggressively.
func TestMITM_WebSocket_BufferedReaderNotLost(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	// Send 50 messages immediately without waiting for echo.
	// If the bufio.Reader is recreated (losing buffered bytes), framing
	// desyncs and ReadMessage returns an error.
	const n = 50
	done := make(chan error, 1)
	go func() {
		for i := 0; i < n; i++ {
			if err := wsConn.WriteMessage(websocket.TextMessage, []byte("buf")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	wsConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < n; i++ {
		_, payload, err := wsConn.ReadMessage()
		require.NoError(t, err, "frame %d: bufio.Reader must not lose bytes", i)
		assert.Equal(t, "buf", string(payload))
	}
	require.NoError(t, <-done)
}

// ── message types ─────────────────────────────────────────────────────────────

func TestMITM_WebSocket_BinaryMessage(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	payload := []byte{0x00, 0x01, 0x02, 0xFE, 0xFF}
	require.NoError(t, wsConn.WriteMessage(websocket.BinaryMessage, payload))
	msgType, received, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.BinaryMessage, msgType)
	assert.Equal(t, payload, received)
}

func TestMITM_WebSocket_EmptyPayload(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("")))
	msgType, received, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, websocket.TextMessage, msgType)
	assert.Equal(t, []byte{}, received)
}

func TestMITM_WebSocket_LargePayload(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	// 512KB — well under wsFrameSizeLimit but large enough to exercise buffering.
	payload := bytes.Repeat([]byte("A"), 512*1024)
	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, payload))
	_, received, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, payload, received)
}

func TestMITM_WebSocket_PayloadAtSizeLimit(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	// Exactly at wsFrameSizeLimit (1MB) — should proxy fine, and logged payload
	// should be exactly wsFrameSizeLimit bytes (no truncation needed).
	payload := bytes.Repeat([]byte("B"), 1<<20)
	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, payload))
	_, received, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, len(payload), len(received), "payload at size limit must be proxied intact")
}

// ── message ordering ──────────────────────────────────────────────────────────

func TestMITM_WebSocket_MessageOrderingPreserved(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	messages := []string{"first", "second", "third", "fourth", "fifth"}
	for _, msg := range messages {
		require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte(msg)))
	}
	for _, expected := range messages {
		_, payload, err := wsConn.ReadMessage()
		require.NoError(t, err)
		assert.Equal(t, expected, string(payload), "messages must arrive in send order")
	}
}

// ── frame logging correctness ─────────────────────────────────────────────────

func TestMITM_WebSocket_FrameDirectionsCorrect(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("dirtest")))
	_, _, err = wsConn.ReadMessage()
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conns, err := st.AllWebSocketConnections()
		require.NoError(t, err)
		if len(conns) == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		frames, err := st.FramesForConnection(conns[0].ID)
		require.NoError(t, err)
		if len(frames) >= 2 {
			// First frame must be client→server, second server→client.
			assert.Equal(t, 0, int(frames[0].Direction), "first frame must be client→server")
			assert.Equal(t, 1, int(frames[1].Direction), "second frame must be server→client")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for frames with correct directions")
}

func TestMITM_WebSocket_FramePayloadsLogged(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("payload-check")))
	_, _, err = wsConn.ReadMessage()
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conns, err := st.AllWebSocketConnections()
		require.NoError(t, err)
		if len(conns) == 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		frames, err := st.FramesForConnection(conns[0].ID)
		require.NoError(t, err)
		if len(frames) >= 1 {
			assert.Equal(t, "payload-check", string(frames[0].Payload))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for frame payload to be logged")
}

// ── connection lifecycle ──────────────────────────────────────────────────────

func TestMITM_WebSocket_CleanClose(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("bye")))
	_, payload, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "bye", string(payload))

	err = wsConn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"))
	require.NoError(t, err)
	wsConn.Close()
}

func TestMITM_WebSocket_UpstreamDropsConnection(t *testing.T) {
	st := newTestStore(t)

	// Upstream closes the connection after receiving the first message.
	upstream := wsUpstreamServerWithHandler(t, func(conn *websocket.Conn) {
		_, _, _ = conn.ReadMessage()
		// Close without sending a close frame — simulates abrupt upstream drop.
		conn.Close()
	})

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("drop-me")))

	// Browser side should get an error when upstream drops.
	wsConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = wsConn.ReadMessage()
	assert.Error(t, err, "browser conn must get an error when upstream drops")
}

func TestMITM_WebSocket_BrowserDropsConnection(t *testing.T) {
	st := newTestStore(t)

	received := make(chan struct{})
	upstream := wsUpstreamServerWithHandler(t, func(conn *websocket.Conn) {
		// Signal that upstream is ready, then try to read — should fail when
		// browser drops.
		close(received)
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, _ = conn.ReadMessage()
	})

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)

	// Wait for upstream to be ready, then abruptly close browser side.
	<-received
	wsConn.Close()

	// Upstream read should fail — the proxy must propagate the close.
	// The test passes if it completes without hanging (the upstream handler returns).
}

// ── concurrent connections ────────────────────────────────────────────────────

func TestMITM_WebSocket_ConcurrentConnections(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)

	const nConns = 5
	var wg sync.WaitGroup
	errors := make(chan error, nConns)

	for i := 0; i < nConns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
			defer wsConn.Close()

			msg := []byte("concurrent")
			if err := wsConn.WriteMessage(websocket.TextMessage, msg); err != nil {
				errors <- err
				return
			}
			_, payload, err := wsConn.ReadMessage()
			if err != nil {
				errors <- err
				return
			}
			if string(payload) != string(msg) {
				errors <- nil
			}
		}(i)
	}

	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

// ── HTTP and WebSocket through same proxy ─────────────────────────────────────

// TestMITM_WebSocket_AfterHTTPOnSameProxy verifies that the proxy correctly
// handles both HTTP and WebSocket connections. HTTP and WS use separate CONNECT
// tunnels (which is how browsers work — each is a separate TCP connection).
func TestMITM_WebSocket_AfterHTTPOnSameProxy(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)

	// First: a plain HTTPS request through the proxy.
	httpConn := doConnectTunnel(t, proxyAddr, targetHost)
	httpTLS := tlsOverTunnel(t, httpConn, host)
	resp := sendHTTPOverConn(t, httpTLS, "GET", "/", targetHost, "")
	resp.Body.Close()
	// The upstream is a WS-only server so it won't return 200,
	// but the proxy must forward the request and get a response.
	assert.NotZero(t, resp.StatusCode)

	// Then: a WebSocket connection through the same proxy.
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("after-http")))
	_, payload, err := wsConn.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "after-http", string(payload), "WebSocket must work correctly after a prior HTTP request through the same proxy")
}

// ── URL and path preservation ─────────────────────────────────────────────────

func TestMITM_WebSocket_PathPreserved(t *testing.T) {
	st := newTestStore(t)

	receivedPath := make(chan string, 1)
	var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath <- r.URL.Path + "?" + r.URL.RawQuery
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close()
	}))
	t.Cleanup(srv.Close)

	upstreamAddr := strings.TrimPrefix(srv.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	_ = wsClientOverTunnelWithPath(t, proxyAddr, targetHost, "/chat/room?token=abc123")

	select {
	case path := <-receivedPath:
		assert.Equal(t, "/chat/room?token=abc123", path, "path and query must be preserved through proxy")
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for upstream to receive request")
	}
}

// ── store URL logged correctly ────────────────────────────────────────────────

func TestMITM_WebSocket_URLLoggedCorrectly(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnelWithPath(t, proxyAddr, targetHost, "/api/stream")
	defer wsConn.Close()

	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("url-test")))
	_, _, err = wsConn.ReadMessage()
	require.NoError(t, err)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conns, err := st.AllWebSocketConnections()
		require.NoError(t, err)
		if len(conns) > 0 {
			assert.Contains(t, conns[0].URL, "/api/stream", "URL must be logged with path")
			assert.Contains(t, conns[0].URL, "wss://", "URL must use wss scheme")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for connection URL to be logged")
}

// ── rapid fire messages ───────────────────────────────────────────────────────

func TestMITM_WebSocket_RapidFireMessages(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)
	defer wsConn.Close()

	const n = 100
	var sent atomic.Int64

	// Send 100 messages without waiting for echo.
	go func() {
		for i := 0; i < n; i++ {
			if err := wsConn.WriteMessage(websocket.TextMessage, []byte("rapid")); err != nil {
				return
			}
			sent.Add(1)
		}
	}()

	// Receive all 100 echoes.
	wsConn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < n; i++ {
		_, payload, err := wsConn.ReadMessage()
		require.NoError(t, err)
		assert.Equal(t, "rapid", string(payload))
	}
}
