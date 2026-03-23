package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/shiv/internal/cert"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

type Proxy struct {
	addr     string
	certAuth *cert.CA
	store    *store.Store

	mu  sync.Mutex
	srv *http.Server
}

const maxBodySize = 10 << 20 // 10 MB

func New(addr string, projectStore *store.Store) (*Proxy, error) {
	ca, err := cert.Load()
	if err != nil {
		return nil, fmt.Errorf("proxy: load CA: %w", err)
	}
	return &Proxy{addr: addr, certAuth: ca, store: projectStore}, nil
}

func (p *Proxy) CA() *cert.CA {
	return p.certAuth
}

func (p *Proxy) Start() error {
	p.mu.Lock()
	srv := &http.Server{
		Addr:         p.addr,
		Handler:      p,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	p.srv = srv
	p.mu.Unlock()

	logger.Always("proxy listening on %s", p.addr)
	return srv.ListenAndServe()
}

func (p *Proxy) Restart(newAddr string) error {
	p.mu.Lock()
	srv := p.srv
	p.mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("proxy: shutdown: %v", err)
		}
	}

	p.mu.Lock()
	p.addr = newAddr
	p.mu.Unlock()

	go func() {
		if err := p.Start(); err != nil && err != http.ErrServerClosed {
			logger.Error("proxy: restart: %v", err)
		}
	}()
	return nil
}

func (p *Proxy) Stop() {
	p.mu.Lock()
	srv := p.srv
	p.mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("proxy: shutdown: %v", err)
		}
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}

	reqBody, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		http.Error(w, "failed to read response body", http.StatusBadGateway)
		logger.Error("read response body %s %s: %v", r.Method, r.URL, err)
		return
	}

	elapsed := time.Since(start).Milliseconds()

	stripResponseCacheHeaders(resp.Header)
	for headerKey, headerValues := range resp.Header {
		for _, headerValue := range headerValues {
			w.Header().Add(headerKey, headerValue)
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
		InScope:     p.store.InScope(interceptedReq.Host),
	}); err != nil {
		logger.Error("store transaction: %v", err)
	}
}
