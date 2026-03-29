package events

import (
	"net/http"
	"time"
)

// RequestEvent carries a fully-read HTTP request and its body, ready to be
// forwarded. The proxy emits this before forwarding. Consumers may inspect
// or modify the request. The result controls whether forwarding proceeds.
type RequestEvent struct {
	Request *http.Request
	Body    []byte
}

// ResponseEvent carries a completed HTTP transaction. The proxy emits this
// after the upstream response is fully received and the body is decompressed.
// All fields are populated; no further store knowledge is required.
type ResponseEvent struct {
	Timestamp   time.Time
	Host        string
	Proto       string // "HTTP/1.1" or "HTTP/2"
	Method      string
	URL         string
	ReqHeaders  http.Header
	ReqBody     []byte
	StatusCode  int
	RespHeaders http.Header
	RespBody    []byte // decompressed; nil if binary
	DurationMs  int64
	TLS         bool
}

// RequestResult is returned by RequestMiddleware. If Drop is true the proxy
// sends a 403 and does not forward. Request and Body are the (possibly
// modified) request to forward when Drop is false.
type RequestResult struct {
	Drop    bool
	Request *http.Request
	Body    []byte
}

// WebSocketDirection indicates which endpoint sent a frame.
type WebSocketDirection int

const (
	WebSocketClient WebSocketDirection = 0 // browser → server
	WebSocketServer WebSocketDirection = 1 // server → browser
)

// WebSocketOpcode mirrors gorilla/websocket message type constants.
type WebSocketOpcode int

const (
	WebSocketText   WebSocketOpcode = 1
	WebSocketBinary WebSocketOpcode = 2
	WebSocketPing   WebSocketOpcode = 9
	WebSocketPong   WebSocketOpcode = 10
	WebSocketClose  WebSocketOpcode = 8
)

// WebSocketConnectionEvent is emitted once when a WebSocket upgrade handshake
// completes successfully. The proxy emits this after the upstream dial and
// browser upgrade both succeed.
type WebSocketConnectionEvent struct {
	Host      string
	URL       string
	TLS       bool
	Timestamp time.Time
}

// WebSocketFrameResult is returned by WebSocketFrameObserver. Payload is the
// (possibly modified) payload to forward. If Payload is nil the original is used.
type WebSocketFrameResult struct {
	Payload []byte
}

// WebSocketFrameEvent is emitted for every proxied WebSocket frame in either
// direction. ConnectionID is the value returned by the
// WebSocketConnectionObserver that handled the corresponding
// WebSocketConnectionEvent.
type WebSocketFrameEvent struct {
	ConnectionID uint64
	Timestamp    time.Time
	Direction    WebSocketDirection
	Opcode       WebSocketOpcode
	Payload      []byte
}
