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
	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/store"
)

type Proxy struct {
	addr     string
	certAuth *cert.CA
	store    *store.Store

	mu  sync.Mutex
	srv *http.Server
}

const maxBodySize = 10 << 20

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

	return srv.ListenAndServe()
}

func (p *Proxy) Restart(newAddr string) error {
	p.mu.Lock()
	srv := p.srv
	p.mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)

		p.mu.Lock()
		p.srv = nil
		p.mu.Unlock()
	}

	p.mu.Lock()
	p.addr = newAddr
	p.mu.Unlock()

	go func() {
		_ = p.Start()
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
		_ = srv.Shutdown(ctx)

		p.mu.Lock()
		p.srv = nil
		p.mu.Unlock()
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
		return
	}
	defer resp.Body.Close()

	var respBuf bytes.Buffer
	reader := io.TeeReader(io.LimitReader(resp.Body, maxBodySize), &respBuf)

	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	_, _ = io.Copy(w, reader)

	respBody := respBuf.Bytes()
	elapsed := time.Since(start).Milliseconds()

	logBody := internalhttp.Decompress(resp.Header, respBody)
	if internalhttp.IsBinary(resp.Header) {
		logBody = nil
	}

	_ = p.store.Log(store.Transaction{
		Timestamp:   start,
		Host:        interceptedReq.Host,
		Proto:       "HTTP/1.1",
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
	})
}
