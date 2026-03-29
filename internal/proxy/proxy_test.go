package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore opens a temp SQLite store and registers cleanup.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp("", "shiv-proxy-test-*.shiv")
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	projectStore, err := store.Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { projectStore.Close() })
	return projectStore
}

// newTestBus creates a bus wired to the given store — mirrors production wiring.
func newTestBus(t *testing.T, st *store.Store) *events.Bus {
	t.Helper()
	bus := events.NewBus()
	bus.Register(st.Intercept)
	bus.Register(st)
	return bus
}

// newTestProxy creates a Proxy wired to an events.Bus.
// The proxy is wired as an http.Handler so we can call it via httptest without
// binding a real port.
func newTestProxy(t *testing.T, bus *events.Bus) *proxy.Proxy {
	t.Helper()
	p, err := proxy.New("127.0.0.1:0", bus)
	require.NoError(t, err)
	return p
}

// upstreamServer starts a simple upstream that echoes status + body.
func upstreamServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── HTTP forwarding ───────────────────────────────────────────────────────────

func TestServeHTTP_ForwardsRequestAndReturnsResponse(t *testing.T) {
	st := newTestStore(t)
	upstream := upstreamServer(t, http.StatusOK, "hello proxy")
	p := newTestProxy(t, newTestBus(t, st))

	req := httptest.NewRequest(http.MethodGet, upstream.URL+"/test", nil)
	req.RequestURI = upstream.URL + "/test"
	w := httptest.NewRecorder()

	p.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello proxy", w.Body.String())
}

func TestServeHTTP_ForwardsPostWithBody(t *testing.T) {
	st := newTestStore(t)

	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(upstream.Close)

	p := newTestProxy(t, newTestBus(t, st))
	body := `{"name":"test"}`
	req := httptest.NewRequest(http.MethodPost, upstream.URL+"/api", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, body, receivedBody)
}

func TestServeHTTP_LogsTransactionToStore(t *testing.T) {
	st := newTestStore(t)
	upstream := upstreamServer(t, http.StatusOK, "logged")
	p := newTestProxy(t, newTestBus(t, st))

	req := httptest.NewRequest(http.MethodGet, upstream.URL+"/logged", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// Give the store a moment to write (it's async via channel)
	time.Sleep(50 * time.Millisecond)

	txs, err := st.TransactionsPage(0, store.TransactionFilter{ShowOutScope: true})
	require.NoError(t, err)
	require.Len(t, txs, 1)
	assert.Equal(t, "GET", txs[0].Method)
	assert.Equal(t, http.StatusOK, txs[0].StatusCode)
}

func TestServeHTTP_ResponseHeadersPassedThrough(t *testing.T) {
	st := newTestStore(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "custom-value")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	p := newTestProxy(t, newTestBus(t, st))
	req := httptest.NewRequest(http.MethodGet, upstream.URL, nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	assert.Equal(t, "custom-value", w.Header().Get("X-Custom-Header"))
}

func TestServeHTTP_CacheHeadersStripped(t *testing.T) {
	st := newTestStore(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Cache-Control", "max-age=3600")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	p := newTestProxy(t, newTestBus(t, st))
	req := httptest.NewRequest(http.MethodGet, upstream.URL, nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("ETag"))
	assert.Equal(t, "no-store, no-cache, must-revalidate", w.Header().Get("Cache-Control"))
}

func TestServeHTTP_UpstreamError_ReturnsBadGateway(t *testing.T) {
	st := newTestStore(t)
	p := newTestProxy(t, newTestBus(t, st))

	// Point at a port that is not listening
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// ── Intercept gate ────────────────────────────────────────────────────────────

func TestServeHTTP_InterceptDrop_ReturnsForbidden(t *testing.T) {
	st := newTestStore(t)
	upstream := upstreamServer(t, http.StatusOK, "should not reach")
	p := newTestProxy(t, newTestBus(t, st))

	st.Intercept.SetEnabled(true)

	// Drain the intercept queue and drop the request in a goroutine
	go func() {
		pending := <-st.Intercept.Queue()
		pending.Reply <- store.Decision{Forward: false}
	}()

	req := httptest.NewRequest(http.MethodGet, upstream.URL, nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestServeHTTP_InterceptForward_ModifiedRequest(t *testing.T) {
	st := newTestStore(t)

	var receivedHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeader = r.Header.Get("X-Injected")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	p := newTestProxy(t, newTestBus(t, st))
	st.Intercept.SetEnabled(true)

	go func() {
		pending := <-st.Intercept.Queue()
		pending.Request.Header.Set("X-Injected", "intercepted")
		pending.Reply <- store.Decision{
			Forward: true,
			Request: pending.Request,
			Body:    pending.Body,
		}
	}()

	req := httptest.NewRequest(http.MethodGet, upstream.URL, nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "intercepted", receivedHeader)
}
