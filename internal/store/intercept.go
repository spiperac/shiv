package store

import (
	"net/http"
	"sync"
)

// PendingRequest is a request held at the intercept gate waiting for a
// user decision. The proxy goroutine blocks on Reply until the UI responds.
type PendingRequest struct {
	Request *http.Request
	Body    []byte // buffered body since http.Request.Body is a stream
	Reply   chan Decision
}

// Decision is the user's response to a pending request.
type Decision struct {
	Forward bool          // false means drop
	Request *http.Request // possibly edited request to forward
	Body    []byte
}

// InterceptGate manages the intercept on/off state and the pending request queue.
type InterceptGate struct {
	mu      sync.RWMutex
	enabled bool
	queue   chan *PendingRequest
}

// NewInterceptGate creates a new gate. Intercept is off by default.
func NewInterceptGate() *InterceptGate {
	return &InterceptGate{
		queue: make(chan *PendingRequest, 64),
	}
}

// Enabled returns whether intercept is currently on.
func (g *InterceptGate) Enabled() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.enabled
}

// SetEnabled turns intercept on or off.
func (g *InterceptGate) SetEnabled(v bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.enabled = v
}

// Hold blocks the calling goroutine until the user makes a decision.
// Returns the (possibly edited) request and whether to forward it.
// If intercept is off, returns the original request immediately.
func (g *InterceptGate) Hold(req *http.Request, body []byte) (*http.Request, []byte, bool) {
	if !g.Enabled() {
		return req, body, true
	}

	pending := &PendingRequest{
		Request: req,
		Body:    body,
		Reply:   make(chan Decision, 1),
	}

	g.queue <- pending
	decision := <-pending.Reply
	return decision.Request, decision.Body, decision.Forward
}

// Queue returns the channel the UI reads pending requests from.
func (g *InterceptGate) Queue() <-chan *PendingRequest {
	return g.queue
}
