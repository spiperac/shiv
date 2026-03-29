package events

import "sync"

// ── Handler interfaces ────────────────────────────────────────────────────────

// RequestMiddleware is called synchronously for every HTTP request before
// forwarding. Returning Drop=true causes the proxy to send a 403 and stop.
// The implementation may modify Request and Body (e.g. intercept gate).
type RequestMiddleware interface {
	HandleRequest(RequestEvent) RequestResult
}

// ResponseObserver is called synchronously after every completed HTTP
// transaction. Ordering is preserved: observers see responses in the exact
// order the proxy received them.
type ResponseObserver interface {
	ObserveResponse(ResponseEvent)
}

// WebSocketConnectionObserver is called synchronously when a WebSocket
// connection is established. It returns a uint64 connection ID that the
// bus passes back to WebSocketFrameObservers as ConnectionID. Return 0
// to indicate the connection should not be tracked.
type WebSocketConnectionObserver interface {
	ObserveWebSocketConnection(WebSocketConnectionEvent) uint64
}

// WebSocketFrameObserver is called synchronously for every WebSocket frame.
// Ordering is preserved within a connection.
type WebSocketFrameObserver interface {
	ObserveWebSocketFrame(WebSocketFrameEvent)
}

// ── Func adapters ─────────────────────────────────────────────────────────────
// Allow main.go to register plain closures without declaring named types.

type RequestMiddlewareFunc func(RequestEvent) RequestResult

func (f RequestMiddlewareFunc) HandleRequest(e RequestEvent) RequestResult {
	return f(e)
}

type ResponseObserverFunc func(ResponseEvent)

func (f ResponseObserverFunc) ObserveResponse(e ResponseEvent) {
	f(e)
}

type WebSocketConnectionObserverFunc func(WebSocketConnectionEvent) uint64

func (f WebSocketConnectionObserverFunc) ObserveWebSocketConnection(e WebSocketConnectionEvent) uint64 {
	return f(e)
}

type WebSocketFrameObserverFunc func(WebSocketFrameEvent)

func (f WebSocketFrameObserverFunc) ObserveWebSocketFrame(e WebSocketFrameEvent) {
	f(e)
}

// ── Bus ───────────────────────────────────────────────────────────────────────

// Bus holds registered handlers and dispatches events to them.
// All methods are safe for concurrent use. All emit methods run
// synchronously to preserve the ordering guarantee that existed before
// the refactor: store.Log emits to the Updates channel which the UI reads
// in arrival order. Making observers async would break this.
type Bus struct {
	mu                 sync.RWMutex
	requestMiddlewares []RequestMiddleware
	responseObservers  []ResponseObserver
	wsConnObservers    []WebSocketConnectionObserver
	wsFrameObservers   []WebSocketFrameObserver
}

// NewBus returns a ready-to-use Bus with no handlers registered.
func NewBus() *Bus {
	return &Bus{}
}

// Register adds a handler to the bus. The handler is matched against all
// four handler interfaces — a single type may implement multiple interfaces
// and will be registered for each one it satisfies. Panics if h implements
// none of the four interfaces (programming error).
func (b *Bus) Register(h any) {
	b.mu.Lock()
	defer b.mu.Unlock()

	matched := false
	if v, ok := h.(RequestMiddleware); ok {
		b.requestMiddlewares = append(b.requestMiddlewares, v)
		matched = true
	}
	if v, ok := h.(ResponseObserver); ok {
		b.responseObservers = append(b.responseObservers, v)
		matched = true
	}
	if v, ok := h.(WebSocketConnectionObserver); ok {
		b.wsConnObservers = append(b.wsConnObservers, v)
		matched = true
	}
	if v, ok := h.(WebSocketFrameObserver); ok {
		b.wsFrameObservers = append(b.wsFrameObservers, v)
		matched = true
	}
	if !matched {
		panic("events: Register called with a type that implements no handler interface")
	}
}

// EmitRequest runs all RequestMiddlewares in registration order.
// The first middleware to return Drop=true short-circuits — subsequent
// middlewares are not called. If no middleware drops the request, the
// final Request and Body are taken from the last middleware result.
// If no middlewares are registered the original event values are returned
// as a pass-through result.
func (b *Bus) EmitRequest(e RequestEvent) RequestResult {
	b.mu.RLock()
	mws := b.requestMiddlewares
	b.mu.RUnlock()

	result := RequestResult{Request: e.Request, Body: e.Body}
	for _, mw := range mws {
		result = mw.HandleRequest(RequestEvent{Request: result.Request, Body: result.Body})
		if result.Drop {
			return result
		}
	}
	return result
}

// EmitResponse calls all ResponseObservers in registration order.
// Runs synchronously; returns when all observers have returned.
func (b *Bus) EmitResponse(e ResponseEvent) {
	b.mu.RLock()
	obs := b.responseObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObserveResponse(e)
	}
}

// EmitWebSocketConnection calls all WebSocketConnectionObservers in
// registration order. The first non-zero uint64 returned is used as the
// connection ID for subsequent frame events. Runs synchronously.
func (b *Bus) EmitWebSocketConnection(e WebSocketConnectionEvent) uint64 {
	b.mu.RLock()
	obs := b.wsConnObservers
	b.mu.RUnlock()

	var connID uint64
	for _, o := range obs {
		if id := o.ObserveWebSocketConnection(e); id != 0 && connID == 0 {
			connID = id
		}
	}
	return connID
}

// EmitWebSocketFrame calls all WebSocketFrameObservers in registration order.
// Runs synchronously to preserve per-connection frame ordering.
func (b *Bus) EmitWebSocketFrame(e WebSocketFrameEvent) {
	b.mu.RLock()
	obs := b.wsFrameObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObserveWebSocketFrame(e)
	}
}
