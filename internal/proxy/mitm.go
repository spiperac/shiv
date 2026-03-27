package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	defer recoverPanic("ServeHTTP " + r.URL.String())
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		logger.Error("mitm: server does not support hijacking")
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		logger.Error("mitm: hijack: %v", err)
		return
	}
	defer clientConn.Close()

	bareHost, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		bareHost = r.Host
	}

	tlsCert, err := p.certAuth.TLSCertForHost(bareHost)
	if err != nil {
		logger.Error("mitm: cert for %s: %v", bareHost, err)
		return
	}

	browserTLS := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err := browserTLS.Handshake(); err != nil {
		logger.Debug("mitm: browser TLS handshake for %s: %v", bareHost, err)
		return
	}
	defer browserTLS.Close()
	logger.Debug("mitm: negotiated protocol for %s: %q", bareHost, browserTLS.ConnectionState().NegotiatedProtocol)

	if browserTLS.ConnectionState().NegotiatedProtocol == "h2" {
		p.handleConnectH2(browserTLS, r, bareHost)
		return
	}

	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("mitm: dial %s: %w", addr, err)
			}
			tlsConn := tls.Client(conn, &tls.Config{
				ServerName:         bareHost,
				InsecureSkipVerify: true, //nolint:gosec
			})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return nil, fmt.Errorf("mitm: upstream TLS handshake for %s: %w", bareHost, err)
			}
			return tlsConn, nil
		},
		ResponseHeaderTimeout: 30 * time.Second,
	}
	defer transport.CloseIdleConnections()

	upstreamClient := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	browserReader := bufio.NewReader(browserTLS)
	for {
		req, err := http.ReadRequest(browserReader)
		if err != nil {
			logger.Debug("mitm: connection closed for %s: %v", bareHost, err)
			return
		}

		start := time.Now()

		reqBody, err := io.ReadAll(io.LimitReader(req.Body, maxBodySize))
		if err != nil {
			logger.Error("mitm: read request body for %s: %v", bareHost, err)
			return
		}
		req.Body.Close()

		req.URL.Scheme = "https"
		req.URL.Host = r.Host

		interceptedReq, reqBody, shouldForward := p.store.Intercept.Hold(req, reqBody)
		if !shouldForward {
			resp := &http.Response{
				Status:        "403 Forbidden",
				StatusCode:    403,
				Proto:         "HTTP/1.1",
				ProtoMajor:    1,
				ProtoMinor:    1,
				Body:          http.NoBody,
				Header:        make(http.Header),
				ContentLength: 0,
			}
			if err := resp.Write(browserTLS); err != nil {
				logger.Error("mitm: write 403 to browser for %s: %v", bareHost, err)
			}
			continue
		}

		outReq, err := http.NewRequest(interceptedReq.Method, interceptedReq.URL.String(), bytes.NewReader(reqBody))
		if err != nil {
			logger.Error("mitm: build upstream request for %s: %v", bareHost, err)
			return
		}
		outReq.Header = interceptedReq.Header.Clone()
		outReq.Host = internalhttp.NormalizeHost(bareHost, true)
		internalhttp.StripRequestCacheHeaders(outReq.Header)

		resp, err := upstreamClient.Do(outReq)
		if err != nil {
			logger.Error("mitm: upstream request for %s: %v", bareHost, err)
			return
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
		resp.Body.Close()
		if err != nil {
			logger.Error("mitm: read response body for %s: %v", bareHost, err)
			return
		}

		elapsed := time.Since(start).Milliseconds()
		logger.Info("%s %s %d %db %dms", interceptedReq.Method, interceptedReq.URL, resp.StatusCode, len(respBody), elapsed)

		internalhttp.StripResponseCacheHeaders(resp.Header)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		if err := resp.Write(browserTLS); err != nil {
			logger.Debug("mitm: write response to browser for %s: %v", bareHost, err)
			return
		}

		logBody := internalhttp.Decompress(resp.Header, respBody)
		if internalhttp.IsBinary(resp.Header) {
			logBody = nil
		}

		if err := p.store.Log(store.Transaction{
			Timestamp:   start,
			Host:        r.Host,
			Proto:       "HTTP/1.1",
			Method:      interceptedReq.Method,
			URL:         interceptedReq.URL.String(),
			ReqHeaders:  interceptedReq.Header,
			ReqBody:     reqBody,
			StatusCode:  resp.StatusCode,
			RespHeaders: resp.Header,
			RespBody:    logBody,
			DurationMs:  elapsed,
			TLS:         true,
			InScope:     p.store.InScope(r.Host),
		}); err != nil {
			logger.Error("mitm: store transaction for %s: %v", bareHost, err)
		}
	}
}
