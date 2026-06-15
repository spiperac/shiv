package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
)

// isWebSocketUpgrade returns true if the request is a WebSocket upgrade.
func isWebSocketUpgrade(req *http.Request) bool {
	return websocket.IsWebSocketUpgrade(req)
}

// handleWebSocketTLS handles a WebSocket upgrade over an already-established
// TLS connection.
//
// browserReader is the *bufio.Reader that was used to parse the upgrade
// request in the H1 loop. It must be passed here — not discarded — because
// bufio.Reader reads ahead and may have buffered bytes beyond the end of the
// HTTP request. Discarding it and creating a new bufio.Reader over the raw
// conn would silently lose those bytes, corrupting the WebSocket stream.
//
// Design:
//  1. Dial the upstream as a WebSocket client.
//  2. Give gorilla's Upgrader a hijackWriter that wraps (tlsConn, browserReader)
//     so gorilla writes the 101 and takes back the conn via Hijack() —
//     the read side of Hijack uses the existing browserReader, preserving
//     any buffered bytes. Zero replay. Zero data loss.
//  3. Proxy frames bidirectionally with gorilla on both sides.
func (p *Proxy) handleWebSocketTLS(
	tlsConn *tls.Conn,
	browserReader *bufio.Reader,
	req *http.Request,
	bareHost string,
	hostWithPort string,
) {
	defer recoverPanic("websocket " + bareHost)

	upstreamURL := &url.URL{
		Scheme:   "wss",
		Host:     hostWithPort,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}

	// ── 1. Dial upstream ──────────────────────────────────────────────────────

	upstreamHeaders := http.Header{}
	for k, vals := range req.Header {
		switch k {
		case "Upgrade", "Connection", "Sec-Websocket-Key",
			"Sec-Websocket-Version", "Sec-Websocket-Extensions",
			"Sec-Websocket-Protocol":
			// Let gorilla's dialer handle these.
		default:
			upstreamHeaders[k] = vals
		}
	}

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			ServerName:         bareHost,
			InsecureSkipVerify: true, //nolint:gosec
		},
		HandshakeTimeout: 10 * time.Second,
	}

	upstreamConn, upstreamResp, err := dialer.Dial(upstreamURL.String(), upstreamHeaders)
	if err != nil {
		logger.Error("ws: dial upstream %s: %v", upstreamURL, err)
		writeWSError(tlsConn, http.StatusBadGateway)
		return
	}
	defer upstreamConn.Close()

	// ── 2. Upgrade browser conn ───────────────────────────────────────────────
	//
	// hijackWriter implements http.ResponseWriter + http.Hijacker over
	// (tlsConn, browserReader). When gorilla's Upgrader calls Hijack(), it
	// gets back the existing bufio.ReadWriter — the read side IS browserReader,
	// so any bytes already buffered by the H1 loop are not lost.

	responseHeader := http.Header{}
	if proto := upstreamResp.Header.Get("Sec-Websocket-Protocol"); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}

	hw := newHijackWriter(tlsConn, browserReader)
	upgrader := websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  1 << 20,
		WriteBufferSize: 1 << 20,
	}
	browserConn, err := upgrader.Upgrade(hw, req, responseHeader)
	if err != nil {
		logger.Error("ws: upgrade browser for %s: %v", bareHost, err)
		return
	}
	defer browserConn.Close()
	const wsMaxMessageSize = 16 << 20 // 16 MB
	browserConn.SetReadLimit(wsMaxMessageSize)
	upstreamConn.SetReadLimit(wsMaxMessageSize)

	// ── 3. Emit connection event ──────────────────────────────────────────────

	connID := p.bus.EmitWebSocketConnection(events.WebSocketConnectionEvent{
		Host:      hostWithPort,
		URL:       upstreamURL.String(),
		TLS:       true,
		Timestamp: time.Now(),
	})

	logger.Info("ws: connected %s%s", bareHost, req.URL.Path)

	// ── 4. Bidirectional frame proxy ──────────────────────────────────────────

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, payload, err := browserConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.Debug("ws: browser→upstream read for %s: %v", bareHost, err)
				}
				upstreamConn.Close()
				return
			}
			result := p.bus.EmitWebSocketFrame(events.WebSocketFrameEvent{
				ConnectionID: connID,
				Timestamp:    time.Now(),
				Direction:    events.WebSocketClient,
				Opcode:       events.WebSocketOpcode(msgType),
				Payload:      payload,
			})
			if err := upstreamConn.WriteMessage(msgType, result.Payload); err != nil {
				logger.Debug("ws: browser→upstream write for %s: %v", bareHost, err)
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, payload, err := upstreamConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.Debug("ws: upstream→browser read for %s: %v", bareHost, err)
				}
				browserConn.Close()
				return
			}
			result := p.bus.EmitWebSocketFrame(events.WebSocketFrameEvent{
				ConnectionID: connID,
				Timestamp:    time.Now(),
				Direction:    events.WebSocketServer,
				Opcode:       events.WebSocketOpcode(msgType),
				Payload:      payload,
			})
			if err := browserConn.WriteMessage(msgType, result.Payload); err != nil {
				logger.Debug("ws: upstream→browser write for %s: %v", bareHost, err)
				return
			}
		}
	}()

	<-done
	<-done
	logger.Info("ws: closed %s%s", bareHost, req.URL.Path)
}

// writeWSError writes a minimal HTTP error response directly to a raw conn.
// Used when the upstream dial fails before the 101 is sent.
func writeWSError(conn net.Conn, code int) {
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		code, http.StatusText(code))
}

// hijackWriter implements http.ResponseWriter and http.Hijacker over a raw
// net.Conn + an existing *bufio.Reader.
//
// The critical invariant: the bufio.Reader passed here MUST be the same one
// used to parse the HTTP upgrade request. gorilla's Upgrader calls Hijack()
// to take ownership of the connection — the read side of the returned
// bufio.ReadWriter is this same reader, so any bytes already buffered in it
// (read-ahead past the end of the HTTP request) are not lost.
//
// The write side is a fresh bufio.Writer over the raw conn — gorilla flushes
// it after writing the 101 response.
type hijackWriter struct {
	conn   net.Conn
	reader *bufio.Reader
	header http.Header
}

func newHijackWriter(conn net.Conn, reader *bufio.Reader) *hijackWriter {
	return &hijackWriter{
		conn:   conn,
		reader: reader,
		header: make(http.Header),
	}
}

func (hw *hijackWriter) Header() http.Header         { return hw.header }
func (hw *hijackWriter) WriteHeader(_ int)           {}
func (hw *hijackWriter) Write(b []byte) (int, error) { return hw.conn.Write(b) }

// Hijack returns the raw conn and a bufio.ReadWriter whose read side is the
// existing browserReader — preserving any buffered bytes — and whose write
// side is a fresh writer over the conn.
func (hw *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	brw := bufio.NewReadWriter(hw.reader, bufio.NewWriter(hw.conn))
	return hw.conn, brw, nil
}
