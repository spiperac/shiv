package events_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shiv/internal/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	return httptest.NewRequest(method, url, nil)
}

func newRequestEvent(t *testing.T) events.RequestEvent {
	t.Helper()
	return events.RequestEvent{
		Request: newRequest(t, http.MethodGet, "http://example.com/path"),
		Body:    []byte("request body"),
	}
}

func newResponseEvent() events.ResponseEvent {
	return events.ResponseEvent{
		Timestamp:  time.Now(),
		Host:       "example.com",
		Proto:      "HTTP/1.1",
		Method:     "GET",
		URL:        "http://example.com/path",
		StatusCode: 200,
		RespBody:   []byte("response body"),
		DurationMs: 42,
		TLS:        false,
	}
}

func newWSConnectionEvent() events.WebSocketConnectionEvent {
	return events.WebSocketConnectionEvent{
		Host:      "example.com:443",
		URL:       "wss://example.com/ws",
		TLS:       true,
		Timestamp: time.Now(),
	}
}

func newWSFrameEvent(connID uint64) events.WebSocketFrameEvent {
	return events.WebSocketFrameEvent{
		ConnectionID: connID,
		Timestamp:    time.Now(),
		Direction:    events.WebSocketClient,
		Opcode:       events.WebSocketText,
		Payload:      []byte("hello"),
	}
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestRegister_PanicsOnUnknownType(t *testing.T) {
	b := events.NewBus()
	assert.Panics(t, func() {
		b.Register(struct{}{})
	})
}

func TestRegister_AcceptsRequestMiddleware(t *testing.T) {
	b := events.NewBus()
	require.NotPanics(t, func() {
		b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
			return events.RequestResult{Request: e.Request, Body: e.Body}
		}))
	})
}

func TestRegister_AcceptsResponseObserver(t *testing.T) {
	b := events.NewBus()
	require.NotPanics(t, func() {
		b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) {}))
	})
}

func TestRegister_AcceptsWebSocketConnectionObserver(t *testing.T) {
	b := events.NewBus()
	require.NotPanics(t, func() {
		b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 { return 1 }))
	})
}

func TestRegister_AcceptsWebSocketFrameObserver(t *testing.T) {
	b := events.NewBus()
	require.NotPanics(t, func() {
		b.Register(events.WebSocketFrameObserverFunc(func(e events.WebSocketFrameEvent) {}))
	})
}

func TestRegister_MultiInterfaceTypeRegisteredForAll(t *testing.T) {
	// A type implementing all four interfaces is registered for each one.
	b := events.NewBus()

	var requestCalled, responseCalled, wsConnCalled, wsFrameCalled bool

	all := &allHandler{
		onRequest:  func() { requestCalled = true },
		onResponse: func() { responseCalled = true },
		onWSConn:   func() { wsConnCalled = true },
		onWSFrame:  func() { wsFrameCalled = true },
	}

	b.Register(all)

	b.EmitRequest(newRequestEvent(t))
	b.EmitResponse(newResponseEvent())
	b.EmitWebSocketConnection(newWSConnectionEvent())
	b.EmitWebSocketFrame(newWSFrameEvent(1))

	assert.True(t, requestCalled, "HandleRequest should be called")
	assert.True(t, responseCalled, "ObserveResponse should be called")
	assert.True(t, wsConnCalled, "ObserveWebSocketConnection should be called")
	assert.True(t, wsFrameCalled, "ObserveWebSocketFrame should be called")
}

// ── EmitRequest ───────────────────────────────────────────────────────────────

func TestEmitRequest_NoHandlers_PassThrough(t *testing.T) {
	b := events.NewBus()
	ev := newRequestEvent(t)

	result := b.EmitRequest(ev)

	assert.False(t, result.Drop)
	assert.Equal(t, ev.Request, result.Request)
	assert.Equal(t, ev.Body, result.Body)
}

func TestEmitRequest_SingleMiddleware_PassThrough(t *testing.T) {
	b := events.NewBus()
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))

	ev := newRequestEvent(t)
	result := b.EmitRequest(ev)

	assert.False(t, result.Drop)
	assert.Equal(t, ev.Request, result.Request)
}

func TestEmitRequest_MiddlewareDrop_ReturnsDrop(t *testing.T) {
	b := events.NewBus()
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		return events.RequestResult{Drop: true, Request: e.Request, Body: e.Body}
	}))

	result := b.EmitRequest(newRequestEvent(t))

	assert.True(t, result.Drop)
}

func TestEmitRequest_FirstDropShortCircuits(t *testing.T) {
	// The second middleware must never be called when the first drops.
	b := events.NewBus()
	secondCalled := false

	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		return events.RequestResult{Drop: true, Request: e.Request, Body: e.Body}
	}))
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		secondCalled = true
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))

	b.EmitRequest(newRequestEvent(t))

	assert.False(t, secondCalled, "second middleware must not be called after a drop")
}

func TestEmitRequest_MiddlewareModifiesBody(t *testing.T) {
	b := events.NewBus()
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		return events.RequestResult{Request: e.Request, Body: []byte("modified body")}
	}))

	result := b.EmitRequest(newRequestEvent(t))

	assert.False(t, result.Drop)
	assert.Equal(t, []byte("modified body"), result.Body)
}

func TestEmitRequest_MiddlewareModifiesRequest(t *testing.T) {
	b := events.NewBus()
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		e.Request.Header.Set("X-Injected", "yes")
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))

	ev := newRequestEvent(t)
	result := b.EmitRequest(ev)

	assert.Equal(t, "yes", result.Request.Header.Get("X-Injected"))
}

func TestEmitRequest_ChainedMiddlewares_ModificationsAccumulate(t *testing.T) {
	// Each middleware in the chain sees the output of the previous one.
	b := events.NewBus()

	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		return events.RequestResult{Request: e.Request, Body: []byte("step1")}
	}))
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		body := append(e.Body, []byte("-step2")...)
		return events.RequestResult{Request: e.Request, Body: body}
	}))

	result := b.EmitRequest(newRequestEvent(t))

	assert.Equal(t, []byte("step1-step2"), result.Body)
}

func TestEmitRequest_MiddlewareCalledInRegistrationOrder(t *testing.T) {
	b := events.NewBus()
	var order []int

	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		order = append(order, 1)
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		order = append(order, 2)
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))
	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		order = append(order, 3)
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))

	b.EmitRequest(newRequestEvent(t))

	assert.Equal(t, []int{1, 2, 3}, order)
}

// ── EmitResponse ──────────────────────────────────────────────────────────────

func TestEmitResponse_NoHandlers_NoPanic(t *testing.T) {
	b := events.NewBus()
	assert.NotPanics(t, func() {
		b.EmitResponse(newResponseEvent())
	})
}

func TestEmitResponse_ObserverReceivesEvent(t *testing.T) {
	b := events.NewBus()
	var received events.ResponseEvent

	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) {
		received = e
	}))

	ev := newResponseEvent()
	b.EmitResponse(ev)

	assert.Equal(t, ev.Host, received.Host)
	assert.Equal(t, ev.Method, received.Method)
	assert.Equal(t, ev.StatusCode, received.StatusCode)
	assert.Equal(t, ev.DurationMs, received.DurationMs)
}

func TestEmitResponse_MultipleObserversAllCalled(t *testing.T) {
	b := events.NewBus()
	var calls int

	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) { calls++ }))
	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) { calls++ }))
	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) { calls++ }))

	b.EmitResponse(newResponseEvent())

	assert.Equal(t, 3, calls)
}

func TestEmitResponse_ObserversCalledInRegistrationOrder(t *testing.T) {
	b := events.NewBus()
	var order []int

	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) { order = append(order, 1) }))
	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) { order = append(order, 2) }))
	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) { order = append(order, 3) }))

	b.EmitResponse(newResponseEvent())

	assert.Equal(t, []int{1, 2, 3}, order)
}

func TestEmitResponse_IsSynchronous(t *testing.T) {
	// EmitResponse must block until all observers return. We verify this by
	// having the observer set a flag and asserting it is set immediately after
	// EmitResponse returns — no sleep required.
	b := events.NewBus()
	done := false

	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) {
		done = true
	}))

	b.EmitResponse(newResponseEvent())

	assert.True(t, done, "observer must have run before EmitResponse returned")
}

// ── EmitWebSocketConnection ───────────────────────────────────────────────────

func TestEmitWebSocketConnection_NoHandlers_ReturnsZero(t *testing.T) {
	b := events.NewBus()
	id := b.EmitWebSocketConnection(newWSConnectionEvent())
	assert.Equal(t, uint64(0), id)
}

func TestEmitWebSocketConnection_ObserverReceivesEvent(t *testing.T) {
	b := events.NewBus()
	var received events.WebSocketConnectionEvent

	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		received = e
		return 1
	}))

	ev := newWSConnectionEvent()
	b.EmitWebSocketConnection(ev)

	assert.Equal(t, ev.Host, received.Host)
	assert.Equal(t, ev.URL, received.URL)
	assert.Equal(t, ev.TLS, received.TLS)
}

func TestEmitWebSocketConnection_ReturnsFirstNonZeroID(t *testing.T) {
	b := events.NewBus()

	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		return 0 // returns zero — should be skipped
	}))
	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		return 42 // first non-zero — should win
	}))
	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		return 99 // subsequent non-zero — should be ignored
	}))

	id := b.EmitWebSocketConnection(newWSConnectionEvent())

	assert.Equal(t, uint64(42), id)
}

func TestEmitWebSocketConnection_AllObserversCalledEvenAfterNonZero(t *testing.T) {
	// All observers are called regardless of what earlier ones returned.
	b := events.NewBus()
	var calls int

	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		calls++
		return 1
	}))
	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		calls++
		return 2
	}))

	b.EmitWebSocketConnection(newWSConnectionEvent())

	assert.Equal(t, 2, calls, "all observers must be called")
}

func TestEmitWebSocketConnection_ZeroReturnNotUsedAsID(t *testing.T) {
	b := events.NewBus()

	b.Register(events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		return 0
	}))

	id := b.EmitWebSocketConnection(newWSConnectionEvent())
	assert.Equal(t, uint64(0), id)
}

// ── EmitWebSocketFrame ────────────────────────────────────────────────────────

func TestEmitWebSocketFrame_NoHandlers_NoPanic(t *testing.T) {
	b := events.NewBus()
	assert.NotPanics(t, func() {
		b.EmitWebSocketFrame(newWSFrameEvent(1))
	})
}

func TestEmitWebSocketFrame_ObserverReceivesEvent(t *testing.T) {
	b := events.NewBus()
	var received events.WebSocketFrameEvent

	b.Register(events.WebSocketFrameObserverFunc(func(e events.WebSocketFrameEvent) {
		received = e
	}))

	ev := newWSFrameEvent(7)
	b.EmitWebSocketFrame(ev)

	assert.Equal(t, uint64(7), received.ConnectionID)
	assert.Equal(t, events.WebSocketClient, received.Direction)
	assert.Equal(t, events.WebSocketText, received.Opcode)
	assert.Equal(t, []byte("hello"), received.Payload)
}

func TestEmitWebSocketFrame_MultipleObserversAllCalled(t *testing.T) {
	b := events.NewBus()
	var calls int

	b.Register(events.WebSocketFrameObserverFunc(func(e events.WebSocketFrameEvent) { calls++ }))
	b.Register(events.WebSocketFrameObserverFunc(func(e events.WebSocketFrameEvent) { calls++ }))

	b.EmitWebSocketFrame(newWSFrameEvent(1))

	assert.Equal(t, 2, calls)
}

func TestEmitWebSocketFrame_OrderingPreserved(t *testing.T) {
	// Frames emitted in order must be received in order by the observer.
	b := events.NewBus()
	var received []uint64

	b.Register(events.WebSocketFrameObserverFunc(func(e events.WebSocketFrameEvent) {
		received = append(received, e.ConnectionID)
	}))

	b.EmitWebSocketFrame(events.WebSocketFrameEvent{ConnectionID: 1})
	b.EmitWebSocketFrame(events.WebSocketFrameEvent{ConnectionID: 2})
	b.EmitWebSocketFrame(events.WebSocketFrameEvent{ConnectionID: 3})

	assert.Equal(t, []uint64{1, 2, 3}, received)
}

// ── Func adapters ─────────────────────────────────────────────────────────────

func TestRequestMiddlewareFunc_ImplementsInterface(t *testing.T) {
	var _ events.RequestMiddleware = events.RequestMiddlewareFunc(nil)
}

func TestResponseObserverFunc_ImplementsInterface(t *testing.T) {
	var _ events.ResponseObserver = events.ResponseObserverFunc(nil)
}

func TestWebSocketConnectionObserverFunc_ImplementsInterface(t *testing.T) {
	var _ events.WebSocketConnectionObserver = events.WebSocketConnectionObserverFunc(nil)
}

func TestWebSocketFrameObserverFunc_ImplementsInterface(t *testing.T) {
	var _ events.WebSocketFrameObserver = events.WebSocketFrameObserverFunc(nil)
}

func TestRequestMiddlewareFunc_DelegatesCall(t *testing.T) {
	called := false
	f := events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		called = true
		return events.RequestResult{Request: e.Request, Body: e.Body}
	})

	req := newRequest(t, http.MethodGet, "http://example.com")
	f.HandleRequest(events.RequestEvent{Request: req, Body: nil})

	assert.True(t, called)
}

func TestResponseObserverFunc_DelegatesCall(t *testing.T) {
	called := false
	f := events.ResponseObserverFunc(func(e events.ResponseEvent) {
		called = true
	})

	f.ObserveResponse(newResponseEvent())

	assert.True(t, called)
}

func TestWebSocketConnectionObserverFunc_DelegatesCall(t *testing.T) {
	f := events.WebSocketConnectionObserverFunc(func(e events.WebSocketConnectionEvent) uint64 {
		return 55
	})

	id := f.ObserveWebSocketConnection(newWSConnectionEvent())

	assert.Equal(t, uint64(55), id)
}

func TestWebSocketFrameObserverFunc_DelegatesCall(t *testing.T) {
	called := false
	f := events.WebSocketFrameObserverFunc(func(e events.WebSocketFrameEvent) {
		called = true
	})

	f.ObserveWebSocketFrame(newWSFrameEvent(1))

	assert.True(t, called)
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestBus_ConcurrentEmitRequest_Safe(t *testing.T) {
	// Multiple goroutines emitting requests concurrently must not race.
	// Run with -race to verify.
	b := events.NewBus()
	var count atomic.Int64

	b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
		count.Add(1)
		return events.RequestResult{Request: e.Request, Body: e.Body}
	}))

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			b.EmitRequest(events.RequestEvent{Request: req})
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines), count.Load())
}

func TestBus_ConcurrentEmitResponse_Safe(t *testing.T) {
	b := events.NewBus()
	var count atomic.Int64

	b.Register(events.ResponseObserverFunc(func(e events.ResponseEvent) {
		count.Add(1)
	}))

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			b.EmitResponse(newResponseEvent())
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines), count.Load())
}

func TestBus_ConcurrentRegisterAndEmit_Safe(t *testing.T) {
	// Registering handlers while emitting must not race.
	b := events.NewBus()
	var wg sync.WaitGroup

	// Emitters
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			b.EmitRequest(events.RequestEvent{Request: req})
		}()
	}

	// Concurrent registrations
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Register(events.RequestMiddlewareFunc(func(e events.RequestEvent) events.RequestResult {
				return events.RequestResult{Request: e.Request, Body: e.Body}
			}))
		}()
	}

	wg.Wait()
}

// ── WebSocket direction and opcode constants ──────────────────────────────────

func TestWebSocketDirection_Values(t *testing.T) {
	assert.Equal(t, events.WebSocketDirection(0), events.WebSocketClient)
	assert.Equal(t, events.WebSocketDirection(1), events.WebSocketServer)
}

func TestWebSocketOpcode_Values(t *testing.T) {
	assert.Equal(t, events.WebSocketOpcode(1), events.WebSocketText)
	assert.Equal(t, events.WebSocketOpcode(2), events.WebSocketBinary)
	assert.Equal(t, events.WebSocketOpcode(8), events.WebSocketClose)
	assert.Equal(t, events.WebSocketOpcode(9), events.WebSocketPing)
	assert.Equal(t, events.WebSocketOpcode(10), events.WebSocketPong)
}

// ── allHandler — test double implementing all four interfaces ─────────────────

type allHandler struct {
	onRequest  func()
	onResponse func()
	onWSConn   func()
	onWSFrame  func()
}

func (h *allHandler) HandleRequest(e events.RequestEvent) events.RequestResult {
	h.onRequest()
	return events.RequestResult{Request: e.Request, Body: e.Body}
}

func (h *allHandler) ObserveResponse(e events.ResponseEvent) {
	h.onResponse()
}

func (h *allHandler) ObserveWebSocketConnection(e events.WebSocketConnectionEvent) uint64 {
	h.onWSConn()
	return 1
}

func (h *allHandler) ObserveWebSocketFrame(e events.WebSocketFrameEvent) {
	h.onWSFrame()
}
