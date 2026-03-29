package store

import (
	"net/http"
	"sync"

	"github.com/shiv/internal/events"
)

// PendingRequest is a request held at the intercept gate waiting for a
// user decision. The proxy goroutine blocks on Reply until the UI responds.
// done is closed when the request is cancelled via bypass so watchQueue
// can skip stale entries without deadlocking.
type PendingRequest struct {
	Request *http.Request
	Body    []byte
	Reply   chan Decision
	done    chan struct{}
}

func newPendingRequest(req *http.Request, body []byte) *PendingRequest {
	return &PendingRequest{
		Request: req,
		Body:    body,
		Reply:   make(chan Decision, 1),
		done:    make(chan struct{}),
	}
}

// Cancel marks this pending request as cancelled. Safe to call multiple times.
func (p *PendingRequest) Cancel() {
	select {
	case <-p.done:
		// already cancelled
	default:
		close(p.done)
	}
}

// IsDone returns true if this request was cancelled before the UI processed it.
func (p *PendingRequest) IsDone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Decision is the user's response to a pending request.
type Decision struct {
	Forward bool
	Request *http.Request
	Body    []byte
}

// InterceptGate manages intercept state. When enabled, every request enters
// a serialising semaphore — one request is shown to the UI at a time, others
// wait in order. ForwardAll releases all currently waiting goroutines by
// closing the bypass channel and immediately replacing it so future requests
// are intercepted normally again.
type InterceptGate struct {
	mu      sync.RWMutex
	enabled bool

	queue chan *PendingRequest // UI reads from here
	sem   chan struct{}        // capacity 1 — one request shown at a time

	bypassMu     sync.Mutex
	bypass       chan struct{} // closed to release all waiting goroutines
	bypassClosed bool         // tracks whether current bypass is already closed
}

// NewInterceptGate creates a new gate. Intercept is off by default.
func NewInterceptGate() *InterceptGate {
	return &InterceptGate{
		queue:  make(chan *PendingRequest, 1024),
		sem:    make(chan struct{}, 1),
		bypass: make(chan struct{}),
	}
}

// Enabled returns whether intercept is currently on.
func (g *InterceptGate) Enabled() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// SetEnabled turns intercept on or off.
// Turning off triggers bypass so all goroutines currently blocked in Hold
// are released immediately without deadlocking.
// Turning on resets the bypass channel so future requests are intercepted.
// g.mu and bypassMu are never held simultaneously to avoid deadlock with Hold.
func (g *InterceptGate) SetEnabled(v bool) {
	g.mu.Lock()
	g.enabled = v
	g.mu.Unlock()

	if !v {
		g.bypassMu.Lock()
		if !g.bypassClosed {
			close(g.bypass)
			g.bypassClosed = true
		}
		g.bypassMu.Unlock()
	} else {
		g.bypassMu.Lock()
		g.bypass = make(chan struct{})
		g.bypassClosed = false
		g.bypassMu.Unlock()
	}
}

// Hold blocks until the user makes a decision on this request.
// If intercept is off, returns immediately.
// Requests acquire the semaphore in arrival order so the UI sees exactly
// one request at a time. If bypass fires at any point during Hold, the
// goroutine exits immediately and the request is passed through unmodified.
// Any pending already in the queue is marked cancelled so watchQueue skips it.
func (g *InterceptGate) Hold(req *http.Request, body []byte) (*http.Request, []byte, bool) {
	if !g.Enabled() {
		return req, body, true
	}

	// Capture the current bypass reference once. This goroutine watches this
	// exact channel throughout — if it gets closed (ForwardAll or SetEnabled(false)),
	// we see it no matter where in Hold we are.
	g.bypassMu.Lock()
	bypass := g.bypass
	g.bypassMu.Unlock()

	// Wait for semaphore (one slot) or bypass signal.
	select {
	case g.sem <- struct{}{}:
	case <-bypass:
		return req, body, true
	}

	// Re-check enabled — may have changed while we waited for the semaphore.
	if !g.Enabled() {
		<-g.sem
		return req, body, true
	}

	// Re-check bypass — may have been closed while we raced for the semaphore.
	select {
	case <-bypass:
		<-g.sem
		return req, body, true
	default:
	}

	// We hold the semaphore and bypass is open. Put the request in the queue.
	pending := newPendingRequest(req, body)
	g.queue <- pending

	// Wait for the user's decision or a bypass signal.
	select {
	case decision := <-pending.Reply:
		<-g.sem
		return decision.Request, decision.Body, decision.Forward
	case <-bypass:
		// Bypass fired while we were waiting. Mark the pending as cancelled
		// so watchQueue skips this stale queue entry, then release the semaphore.
		pending.Cancel()
		<-g.sem
		return req, body, true
	}
}

// ForwardAll releases all goroutines currently blocked in Hold, passing their
// requests through unmodified. Future requests are intercepted normally —
// the bypass channel is replaced immediately after closing the old one.
func (g *InterceptGate) ForwardAll() {
	g.bypassMu.Lock()
	if !g.bypassClosed {
		close(g.bypass)
		g.bypassClosed = true
	}
	// Replace immediately so future requests are intercepted normally.
	g.bypass = make(chan struct{})
	g.bypassClosed = false
	g.bypassMu.Unlock()
}

// Queue returns the channel the UI reads pending requests from.
func (g *InterceptGate) Queue() <-chan *PendingRequest {
	return g.queue
}

// HandleRequest implements events.RequestMiddleware.
// This is a thin adapter: it calls Hold and maps the result to RequestResult.
func (g *InterceptGate) HandleRequest(e events.RequestEvent) events.RequestResult {
	req, body, forward := g.Hold(e.Request, e.Body)
	return events.RequestResult{
		Drop:    !forward,
		Request: req,
		Body:    body,
	}
}
