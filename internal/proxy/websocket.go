package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

const wsFrameSizeLimit = 1 << 20 // 1 MB per frame

// isWebSocketUpgrade returns true if the request is a WebSocket upgrade.
func isWebSocketUpgrade(req *http.Request) bool {
	return websocket.IsWebSocketUpgrade(req)
}

// singleConnListener is a net.Listener that serves exactly one connection.
// Used to feed an already-established net.Conn into http.Serve so we get
// a proper http.ResponseWriter for gorilla's Upgrader.
type singleConnListener struct {
	conn   net.Conn
	ch     chan net.Conn
	closed chan struct{}
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	l := &singleConnListener{
		conn:   conn,
		ch:     make(chan net.Conn, 1),
		closed: make(chan struct{}),
	}
	l.ch <- conn
	return l
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.ch:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *singleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

// handleWebSocketTLS handles a WebSocket upgrade over an already-established
// TLS connection. It spins up a single-connection HTTP/1.1 server so that
// gorilla's Upgrader receives a proper http.ResponseWriter, then proxies
// frames bidirectionally between browser and upstream.
func (p *Proxy) handleWebSocketTLS(
	tlsConn *tls.Conn,
	req *http.Request,
	bareHost string,
	hostWithPort string,
) {
	defer recoverPanic("websocket " + bareHost)

	// We need to replay the already-read request back through http.Serve.
	// The cleanest way: create a single-connection listener and serve it.
	// The handler will see the request via the normal ServeHTTP path.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.serveWebSocket(w, r, bareHost, hostWithPort, true)
	})

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  0, // no timeout — WebSocket connections are long-lived
		WriteTimeout: 0,
	}

	// Wrap the TLS conn so it replays the buffered request bytes first.
	// Since the request was already read from the bufio.Reader in the loop,
	// we need to put it back. We do this by using a replayConn that prepends
	// the serialised request bytes before the live connection data.
	var reqBuf []byte
	reqBuf = append(reqBuf, req.Method+" "+req.URL.RequestURI()+" HTTP/1.1\r\n"...)
	reqBuf = append(reqBuf, "Host: "+req.Host+"\r\n"...)
	for k, vals := range req.Header {
		for _, v := range vals {
			reqBuf = append(reqBuf, k+": "+v+"\r\n"...)
		}
	}
	reqBuf = append(reqBuf, "\r\n"...)

	replayConn := &replayConn{Conn: tlsConn, buf: reqBuf}
	ln := newSingleConnListener(replayConn)

	// Serve blocks until the single connection is done.
	_ = srv.Serve(ln)
}

// serveWebSocket is the http.HandlerFunc called by the single-conn server.
// It upgrades the browser connection, dials the upstream, and proxies frames.
func (p *Proxy) serveWebSocket(
	w http.ResponseWriter,
	req *http.Request,
	bareHost string,
	hostWithPort string,
	useTLS bool,
) {
	scheme := "ws"
	if useTLS {
		scheme = "wss"
	}
	upstreamURL := &url.URL{
		Scheme:   scheme,
		Host:     hostWithPort,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}

	// Dial the upstream WebSocket.
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			ServerName:         bareHost,
			InsecureSkipVerify: true, //nolint:gosec
		},
		HandshakeTimeout: 10 * time.Second,
	}

	upstreamHeaders := http.Header{}
	for k, vals := range req.Header {
		switch k {
		case "Upgrade", "Connection", "Sec-Websocket-Key",
			"Sec-Websocket-Version", "Sec-Websocket-Extensions",
			"Sec-Websocket-Protocol":
			// gorilla handles these
		default:
			upstreamHeaders[k] = vals
		}
	}

	upstreamConn, upstreamResp, err := dialer.Dial(upstreamURL.String(), upstreamHeaders)
	if err != nil {
		logger.Error("ws: dial upstream %s: %v", upstreamURL, err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer upstreamConn.Close()

	responseHeader := http.Header{}
	if proto := upstreamResp.Header.Get("Sec-Websocket-Protocol"); proto != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", proto)
	}

	upgrader := websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  wsFrameSizeLimit,
		WriteBufferSize: wsFrameSizeLimit,
	}
	browserConn, err := upgrader.Upgrade(w, req, responseHeader)
	if err != nil {
		logger.Error("ws: upgrade browser for %s: %v", bareHost, err)
		return
	}
	defer browserConn.Close()

	connID, err := p.store.LogWebSocketConnection(store.WebSocketConnection{
		Host:      hostWithPort,
		URL:       upstreamURL.String(),
		TLS:       useTLS,
		InScope:   p.store.InScope(hostWithPort),
		Timestamp: time.Now(),
	})
	if err != nil {
		logger.Error("ws: log connection for %s: %v", bareHost, err)
	}

	logger.Info("ws: connected %s%s", bareHost, req.URL.Path)

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			msgType, payload, err := browserConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					logger.Debug("ws: browser→upstream read for %s: %v", bareHost, err)
				}
				return
			}
			p.logWSFrame(connID, store.WebSocketClient, msgType, payload)
			if err := upstreamConn.WriteMessage(msgType, payload); err != nil {
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
				return
			}
			p.logWSFrame(connID, store.WebSocketServer, msgType, payload)
			if err := browserConn.WriteMessage(msgType, payload); err != nil {
				logger.Debug("ws: upstream→browser write for %s: %v", bareHost, err)
				return
			}
		}
	}()

	<-done
	logger.Info("ws: closed %s%s", bareHost, req.URL.Path)
}

// logWSFrame logs a single WebSocket frame, truncating oversized payloads.
func (p *Proxy) logWSFrame(connID uint64, dir store.WebSocketDirection, msgType int, payload []byte) {
	if connID == 0 {
		return
	}
	if len(payload) > wsFrameSizeLimit {
		truncated := make([]byte, wsFrameSizeLimit)
		copy(truncated, payload[:wsFrameSizeLimit])
		payload = truncated
	}
	if err := p.store.LogWebSocketFrame(store.WebSocketFrame{
		ConnectionID: connID,
		Timestamp:    time.Now(),
		Direction:    dir,
		Opcode:       store.WebSocketOpcode(msgType),
		Payload:      payload,
	}); err != nil {
		logger.Error("ws: log frame: %v", err)
	}
}

// replayConn wraps a net.Conn and prepends buffered bytes before live reads.
// Used to replay a request that was already read into a buffer back into
// an http.Server that expects to read it from the connection.
type replayConn struct {
	net.Conn
	buf []byte
}

func (c *replayConn) Read(b []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(b, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}
