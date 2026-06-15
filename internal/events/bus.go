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
// Ordering is preserved within a connection. The returned WebSocketFrameResult
// carries the (possibly modified) payload to forward.
type WebSocketFrameObserver interface {
	ObserveWebSocketFrame(WebSocketFrameEvent) WebSocketFrameResult
}

// PluginLogObserver is called synchronously when a plugin emits a log line.
// The store implements this to buffer lines in memory and notify the UI.
type PluginLogObserver interface {
	ObservePluginLog(PluginLogEvent)
}

// PluginEnabledObserver is called synchronously when the UI toggles a plugin.
// Both the engine (to skip hooks) and the store (to persist state) implement this.
type PluginEnabledObserver interface {
	ObservePluginEnabled(SetPluginEnabledEvent)
}

// LoadPluginObserver is called synchronously when the user imports a new
// plugin. The engine observes it to copy, load, and register the plugin.
type LoadPluginObserver interface {
	ObserveLoadPlugin(LoadPluginEvent)
}

// ProxyCommandObserver is implemented by the proxy to react to UI-initiated
// control events. Restart starts or restarts the proxy on the given address.
// Stop shuts the proxy down.
type ProxyCommandObserver interface {
	ObserveProxyRestart(ProxyRestartEvent)
	ObserveProxyStop(ProxyStopEvent)
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

type WebSocketFrameObserverFunc func(WebSocketFrameEvent) WebSocketFrameResult

func (f WebSocketFrameObserverFunc) ObserveWebSocketFrame(e WebSocketFrameEvent) WebSocketFrameResult {
	return f(e)
}

type PluginLogObserverFunc func(PluginLogEvent)

func (f PluginLogObserverFunc) ObservePluginLog(e PluginLogEvent) {
	f(e)
}

type PluginEnabledObserverFunc func(SetPluginEnabledEvent)

func (f PluginEnabledObserverFunc) ObservePluginEnabled(e SetPluginEnabledEvent) {
	f(e)
}

type LoadPluginObserverFunc func(LoadPluginEvent)

func (f LoadPluginObserverFunc) ObserveLoadPlugin(e LoadPluginEvent) {
	f(e)
}

// ── Bus ───────────────────────────────────────────────────────────────────────

// Bus holds registered handlers and dispatches events to them.
// All methods are safe for concurrent use. All emit methods run
// synchronously to preserve the ordering guarantee that existed before
// the refactor: store.Log emits to the Updates channel which the UI reads
// in arrival order. Making observers async would break this.
type Bus struct {
	mu                     sync.RWMutex
	requestMiddlewares     []RequestMiddleware
	responseObservers      []ResponseObserver
	wsConnObservers        []WebSocketConnectionObserver
	wsFrameObservers       []WebSocketFrameObserver
	pluginLogObservers     []PluginLogObserver
	pluginEnabledObservers []PluginEnabledObserver
	pluginLoadObservers    []LoadPluginObserver
	proxyCommandObservers  []ProxyCommandObserver
}

// NewBus returns a ready-to-use Bus with no handlers registered.
func NewBus() *Bus {
	return &Bus{}
}

// Register adds a handler to the bus. The handler is matched against all
// handler interfaces — a single type may implement multiple interfaces
// and will be registered for each one it satisfies. Panics if h implements
// none of the interfaces (programming error).
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
	if v, ok := h.(PluginLogObserver); ok {
		b.pluginLogObservers = append(b.pluginLogObservers, v)
		matched = true
	}
	if v, ok := h.(PluginEnabledObserver); ok {
		b.pluginEnabledObservers = append(b.pluginEnabledObservers, v)
		matched = true
	}
	if v, ok := h.(LoadPluginObserver); ok {
		b.pluginLoadObservers = append(b.pluginLoadObservers, v)
		matched = true
	}
	if v, ok := h.(ProxyCommandObserver); ok {
		b.proxyCommandObservers = append(b.proxyCommandObservers, v)
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
// Runs synchronously to preserve per-connection frame ordering. Each observer
// may return a modified payload — the last non-nil payload wins. The final
// WebSocketFrameResult carries the payload to forward.
func (b *Bus) EmitWebSocketFrame(e WebSocketFrameEvent) WebSocketFrameResult {
	b.mu.RLock()
	obs := b.wsFrameObservers
	b.mu.RUnlock()

	result := WebSocketFrameResult{Payload: e.Payload}
	for _, o := range obs {
		r := o.ObserveWebSocketFrame(e)
		if r.Payload != nil {
			result.Payload = r.Payload
			e.Payload = r.Payload
		}
	}
	return result
}

// EmitPluginLog calls all PluginLogObservers in registration order.
// Runs synchronously.
func (b *Bus) EmitPluginLog(e PluginLogEvent) {
	b.mu.RLock()
	obs := b.pluginLogObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObservePluginLog(e)
	}
}

// EmitSetPluginEnabled calls all PluginEnabledObservers in registration order.
// Runs synchronously.
func (b *Bus) EmitSetPluginEnabled(e SetPluginEnabledEvent) {
	b.mu.RLock()
	obs := b.pluginEnabledObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObservePluginEnabled(e)
	}
}

// EmitLoadPlugin calls all LoadPluginObservers in registration order.
// Runs synchronously.
func (b *Bus) EmitLoadPlugin(e LoadPluginEvent) {
	b.mu.RLock()
	obs := b.pluginLoadObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObserveLoadPlugin(e)
	}
}

// EmitProxyRestart calls all ProxyCommandObservers to restart on the given address.
// Runs synchronously.
func (b *Bus) EmitProxyRestart(e ProxyRestartEvent) {
	b.mu.RLock()
	obs := b.proxyCommandObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObserveProxyRestart(e)
	}
}

// EmitProxyStop calls all ProxyCommandObservers to stop the proxy.
// Runs synchronously.
func (b *Bus) EmitProxyStop(e ProxyStopEvent) {
	b.mu.RLock()
	obs := b.proxyCommandObservers
	b.mu.RUnlock()

	for _, o := range obs {
		o.ObserveProxyStop(e)
	}
}
