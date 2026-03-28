package proxy_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		// Echo all messages back.
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

// wsClientOverTunnel dials a WebSocket through the proxy CONNECT tunnel.
func wsClientOverTunnel(t *testing.T, proxyAddr, targetHost string) *websocket.Conn {
	t.Helper()
	conn := doConnectTunnel(t, proxyAddr, targetHost)

	// Wrap the raw conn in TLS negotiating http/1.1 (no h2 for WS).
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         strings.Split(targetHost, ":")[0],
		InsecureSkipVerify: true, //nolint:gosec — test only
		NextProtos:         []string{"http/1.1"},
	})
	require.NoError(t, tlsConn.Handshake())
	t.Cleanup(func() { tlsConn.Close() })

	// NetDialTLSContext tells gorilla the connection is already TLS —
	// it will not attempt another handshake.
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

// ── tests ─────────────────────────────────────────────────────────────────────

func TestMITM_WebSocket_EchoesMessages(t *testing.T) {
	st := newTestStore(t)
	upstream := wsUpstreamServer(t)

	upstreamAddr := strings.TrimPrefix(upstream.URL, "https://")
	host, port, err := net.SplitHostPort(upstreamAddr)
	require.NoError(t, err)
	targetHost := net.JoinHostPort(host, port)

	proxyAddr := startProxyServer(t, st)
	wsConn := wsClientOverTunnel(t, proxyAddr, targetHost)

	err = wsConn.WriteMessage(websocket.TextMessage, []byte("hello"))
	require.NoError(t, err)

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

	// Send a message to ensure the connection is fully established.
	require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte("ping")))
	_, _, err = wsConn.ReadMessage()
	require.NoError(t, err)

	// Give the store a moment to write.
	var conns []interface{}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := st.AllWebSocketConnections()
		require.NoError(t, err)
		if len(loaded) > 0 {
			conns = make([]interface{}, len(loaded))
			assert.Equal(t, targetHost, loaded[0].Host)
			assert.True(t, loaded[0].TLS)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = conns
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

	// Send two messages — each round-trip produces a client frame and a server frame.
	for _, msg := range []string{"frame1", "frame2"} {
		require.NoError(t, wsConn.WriteMessage(websocket.TextMessage, []byte(msg)))
		_, _, err := wsConn.ReadMessage()
		require.NoError(t, err)
	}

	// Wait for connection to appear then check frames.
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
			// 2 client frames + 2 server echo frames.
			assert.Equal(t, 4, len(frames))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for WebSocket frames to be logged")
}
