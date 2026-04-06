package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/shiv/internal/cert"
	"github.com/shiv/internal/events"
	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
)

type Proxy struct {
	addr     string
	certAuth *cert.CA
	bus      *events.Bus

	mu    sync.Mutex
	srv   *http.Server
	conns sync.Map // active hijacked net.Conn
}

const maxBodySize = 10 << 20

func New(addr string, bus *events.Bus) (*Proxy, error) {
	ca, err := cert.Load()
	if err != nil {
		return nil, fmt.Errorf("proxy: load CA: %w", err)
	}
	p := &Proxy{addr: addr, certAuth: ca, bus: bus}
	bus.Register(p)
	return p, nil
}

func (p *Proxy) CA() *cert.CA {
	return p.certAuth
}

func (p *Proxy) trackConn(c net.Conn) {
	p.conns.Store(c, struct{}{})
}

func (p *Proxy) untrackConn(c net.Conn) {
	p.conns.Delete(c)
}

func (p *Proxy) closeAllConns() {
	p.conns.Range(func(k, _ any) bool {
		k.(net.Conn).SetDeadline(time.Now())
		return true
	})
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
	ln, err := net.Listen("tcp", newAddr)
	if err != nil {
		return fmt.Errorf("proxy: listen on %s: %w", newAddr, err)
	}

	newSrv := &http.Server{
		Handler:      p,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	p.mu.Lock()
	oldSrv := p.srv
	p.addr = newAddr
	p.srv = newSrv
	p.mu.Unlock()

	if oldSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = oldSrv.Shutdown(ctx)
	}
	p.closeAllConns()

	go func() {
		if err := newSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("proxy: Serve: %v", err)
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
		_ = srv.Shutdown(ctx)

		p.mu.Lock()
		p.srv = nil
		p.mu.Unlock()
	}

	p.closeAllConns()
}

// ObserveProxyRestart implements events.ProxyCommandObserver.
func (p *Proxy) ObserveProxyRestart(e events.ProxyRestartEvent) {
	_ = p.Restart(e.Addr)
}

// ObserveProxyStop implements events.ProxyCommandObserver.
func (p *Proxy) ObserveProxyStop(_ events.ProxyStopEvent) {
	p.Stop()
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

	result := p.bus.EmitRequest(events.RequestEvent{Request: r, Body: reqBody})
	if result.Drop {
		http.Error(w, "request dropped", http.StatusForbidden)
		return
	}
	interceptedReq, reqBody := result.Request, result.Body
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

	p.bus.EmitResponse(events.ResponseEvent{
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
	})
}
