package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shiv/internal/cert"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// Proxy is an HTTP/HTTPS proxy server.
type Proxy struct {
	addr  string
	ca    *cert.CA
	store *store.Store
}

// New creates a new Proxy. Loads (or generates) the CA on startup.
func New(addr string, st *store.Store) (*Proxy, error) {
	ca, err := cert.Load()
	if err != nil {
		return nil, fmt.Errorf("proxy: load CA: %w", err)
	}
	return &Proxy{addr: addr, ca: ca, store: st}, nil
}

// Start starts the proxy and blocks until it fails.
func (p *Proxy) Start() error {
	logger.Always("proxy listening on %s", p.addr)
	return http.ListenAndServe(p.addr, p)
}

// ServeHTTP handles all incoming proxy requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		logger.Error("read request body: %v", err)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(reqBody))

	interceptedReq, reqBody, shouldForward := p.store.Intercept.Hold(r, reqBody)
	if !shouldForward {
		http.Error(w, "request dropped", http.StatusForbidden)
		return
	}
	interceptedReq.Body = io.NopCloser(bytes.NewReader(reqBody))

	start := time.Now()

	resp, err := forward(interceptedReq)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		logger.Error("%s %s: %v", r.Method, r.URL, err)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read response body", http.StatusBadGateway)
		logger.Error("read response body %s %s: %v", r.Method, r.URL, err)
		return
	}

	elapsed := time.Since(start).Milliseconds()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(respBody); err != nil {
		logger.Error("write response to client: %v", err)
	}

	logger.Info("%s %s %d %db %dms", r.Method, r.URL, resp.StatusCode, len(respBody), elapsed)

	logBody := decompressBody(resp.Header, respBody)
	if isBinary(resp.Header) {
		logBody = nil
	}

	if err := p.store.Log(store.Transaction{
		Timestamp:   start,
		Host:        interceptedReq.Host,
		Method:      interceptedReq.Method,
		URL:         interceptedReq.URL.String(),
		ReqHeaders:  interceptedReq.Header,
		ReqBody:     reqBody,
		StatusCode:  resp.StatusCode,
		RespHeaders: resp.Header,
		RespBody:    logBody,
		DurationMs:  elapsed,
		TLS:         false,
		InScope:     true,
	}); err != nil {
		logger.Error("store transaction: %v", err)
	}
}
