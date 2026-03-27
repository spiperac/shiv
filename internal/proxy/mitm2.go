package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// handleConnectH2 serves an HTTP/2 MITM session on an already-established
// TLS connection that negotiated "h2". It blocks until the connection closes.
func (p *Proxy) handleConnectH2(browserTLS *tls.Conn, connectReq *http.Request, bareHost string) {
	upstreamTransport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("mitm/h2: dial %s: %w", addr, err)
			}
			tlsConn := tls.Client(conn, &tls.Config{
				ServerName:         bareHost,
				InsecureSkipVerify: true, //nolint:gosec
				NextProtos:         []string{"h2", "http/1.1"},
			})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return nil, fmt.Errorf("mitm/h2: upstream TLS handshake for %s: %w", addr, err)
			}
			return tlsConn, nil
		},
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if err := http2.ConfigureTransport(upstreamTransport); err != nil {
		logger.Error("mitm/h2: configure upstream transport: %v", err)
	}
	defer upstreamTransport.CloseIdleConnections()

	upstreamClient := &http.Client{
		Transport: upstreamTransport,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	streamHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.URL.Scheme = "https"
		req.URL.Host = bareHost

		reqBody, err := io.ReadAll(io.LimitReader(req.Body, maxBodySize))
		if err != nil {
			logger.Error("mitm/h2: read request body for %s: %v", bareHost, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		req.Body.Close()

		start := time.Now()

		interceptedReq, reqBody, shouldForward := p.store.Intercept.Hold(req, reqBody)
		if !shouldForward {
			http.Error(w, "request dropped", http.StatusForbidden)
			return
		}

		outReq, err := http.NewRequestWithContext(req.Context(), interceptedReq.Method, interceptedReq.URL.String(), bytes.NewReader(reqBody))
		if err != nil {
			logger.Error("mitm/h2: build upstream request for %s: %v", bareHost, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		outReq.Header = interceptedReq.Header.Clone()
		outReq.Host = internalhttp.NormalizeHost(bareHost, true)
		internalhttp.StripRequestCacheHeaders(outReq.Header)

		resp, err := upstreamClient.Do(outReq)
		if err != nil {
			logger.Error("mitm/h2: upstream request for %s: %v", bareHost, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
		resp.Body.Close()
		if err != nil {
			logger.Error("mitm/h2: read response body for %s: %v", bareHost, err)
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}

		elapsed := time.Since(start).Milliseconds()
		logger.Info("%s %s %d %db %dms (h2)", interceptedReq.Method, interceptedReq.URL, resp.StatusCode, len(respBody), elapsed)

		internalhttp.StripResponseCacheHeaders(resp.Header)
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(respBody); err != nil {
			logger.Debug("mitm/h2: write response to browser for %s: %v", bareHost, err)
		}

		logBody := internalhttp.Decompress(resp.Header, respBody)
		if internalhttp.IsBinary(resp.Header) {
			logBody = nil
		}

		if err := p.store.Log(store.Transaction{
			Timestamp:   start,
			Host:        connectReq.Host,
			Proto:       "HTTP/2",
			Method:      interceptedReq.Method,
			URL:         interceptedReq.URL.String(),
			ReqHeaders:  interceptedReq.Header,
			ReqBody:     reqBody,
			StatusCode:  resp.StatusCode,
			RespHeaders: resp.Header,
			RespBody:    logBody,
			DurationMs:  elapsed,
			TLS:         true,
			InScope:     p.store.InScope(connectReq.Host),
		}); err != nil {
			logger.Error("mitm/h2: store transaction for %s: %v", bareHost, err)
		}
	})

	h2srv := &http2.Server{}
	h2srv.ServeConn(browserTLS, &http2.ServeConnOpts{
		Handler: streamHandler,
	})
}
