package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"

	"github.com/shiv/internal/events"
	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
)

const (
	h2UpstreamDialTimeout   = 10 * time.Second
	h2UpstreamHeaderTimeout = 30 * time.Second
	// h2StreamRequestTimeout is a per-stream deadline, not a connection-level
	// timeout. Using a context timeout per stream instead of a client-level
	// Timeout means a single slow stream cannot kill the entire H2 connection.
	h2StreamRequestTimeout = 60 * time.Second
	// h2MaxConcurrentStreams matches the HTTP/2 spec default (RFC 7540 §6.5.2).
	h2MaxConcurrentStreams = 250
)

func (p *Proxy) handleConnectH2(browserTLS *tls.Conn, connectReq *http.Request, bareHost string) {
	defer recoverPanic("handleConnectH2 " + bareHost)

	// Use the port-aware host from the original CONNECT request for upstream
	// dialling so non-443 HTTPS ports are preserved.
	upstreamHost := connectReq.Host

	upstreamTransport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: h2UpstreamDialTimeout}).DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, fmt.Errorf("mitm/h2: dial %s: %w", addr, err)
			}
			tlsConn := tls.Client(conn, &tls.Config{
				ServerName:         bareHost,
				InsecureSkipVerify: true, //nolint:gosec
				NextProtos:         []string{"h2", "http/1.1"},
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return nil
					}
					cert, err := x509.ParseCertificate(rawCerts[0])
					if err != nil {
						return nil
					}
					now := time.Now()
					if now.After(cert.NotAfter) {
						logger.Info("mitm/h2: EXPIRED cert for %s (expired %s, issuer: %s)",
							bareHost, cert.NotAfter.Format("2006-01-02"), cert.Issuer.CommonName)
					} else if now.Before(cert.NotBefore) {
						logger.Info("mitm/h2: NOT YET VALID cert for %s (valid from %s, issuer: %s)",
							bareHost, cert.NotBefore.Format("2006-01-02"), cert.Issuer.CommonName)
					}
					if err := cert.VerifyHostname(bareHost); err != nil {
						logger.Info("mitm/h2: hostname mismatch for %s: %v (CN: %s, SANs: %v)",
							bareHost, err, cert.Subject.CommonName, cert.DNSNames)
					}
					return nil
				},
			})
			if err := tlsConn.Handshake(); err != nil {
				conn.Close()
				return nil, fmt.Errorf("mitm/h2: upstream TLS handshake for %s: %w", addr, err)
			}
			return tlsConn, nil
		},
		ResponseHeaderTimeout: h2UpstreamHeaderTimeout,
	}
	if err := http2.ConfigureTransport(upstreamTransport); err != nil {
		logger.Error("mitm/h2: configure upstream transport: %v", err)
	}
	defer upstreamTransport.CloseIdleConnections()

	// No client-level Timeout — that would kill the entire H2 connection on
	// any slow stream. Per-stream timeouts are applied via request contexts.
	upstreamClient := &http.Client{
		Transport: upstreamTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	streamHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer recoverPanic("h2 stream " + bareHost)

		req.URL.Scheme = "https"
		req.URL.Host = upstreamHost

		reqBody, err := io.ReadAll(io.LimitReader(req.Body, maxBodySize))
		if err != nil {
			logger.Error("mitm/h2: read request body for %s: %v", bareHost, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		req.Body.Close()

		start := time.Now()

		result := p.bus.EmitRequest(events.RequestEvent{Request: req, Body: reqBody})
		if result.Drop {
			// Stream is dropped by intercept rules; the H2 connection stays alive.
			http.Error(w, "request dropped", http.StatusForbidden)
			return
		}
		interceptedReq, reqBody := result.Request, result.Body

		streamCtx, cancel := context.WithTimeout(req.Context(), h2StreamRequestTimeout)
		defer cancel()

		outReq, err := http.NewRequestWithContext(streamCtx, interceptedReq.Method, interceptedReq.URL.String(), bytes.NewReader(reqBody))
		if err != nil {
			logger.Error("mitm/h2: build upstream request for %s: %v", bareHost, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		outReq.Header = interceptedReq.Header.Clone()
		outReq.Host = internalhttp.NormalizeHost(bareHost, true)
		// Strip H2-illegal headers before forwarding.
		outReq.Header.Del("Transfer-Encoding")
		internalhttp.StripRequestCacheHeaders(outReq.Header)

		resp, err := upstreamClient.Do(outReq)
		if err != nil {
			// context.Canceled means the browser cancelled the stream (RST_STREAM)
			// or we hit our per-stream deadline. This is normal browser behaviour
			// (prefetch, autocomplete, navigation away) — log at Debug, not Error.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logger.Debug("mitm/h2: stream cancelled/timed out for %s: %v", bareHost, err)
			} else {
				logger.Error("mitm/h2: upstream request for %s: %v", bareHost, err)
			}
			http.Error(w, "bad gateway", http.StatusBadGateway)
			return
		}

		var respBuf bytes.Buffer
		reader := io.TeeReader(io.LimitReader(resp.Body, maxBodySize), &respBuf)

		// Strip Transfer-Encoding — illegal in HTTP/2 responses (RFC 7540 §8.1.2.2).
		// Strip caching headers so the browser always fetches fresh content.
		resp.Header.Del("Transfer-Encoding")
		internalhttp.StripResponseCacheHeaders(resp.Header)

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		if _, err := io.Copy(w, reader); err != nil {
			logger.Debug("mitm/h2: write response to browser for %s: %v", bareHost, err)
			// Drain whatever remains into respBuf so the log body is as complete
			// as possible even if the browser disconnected mid-stream.
			_, _ = io.Copy(&respBuf, resp.Body)
		}

		respBody := respBuf.Bytes()
		resp.Body.Close()

		if len(respBody) >= int(maxBodySize) {
			logger.Debug("mitm/h2: response body truncated at %d bytes for %s %s",
				maxBodySize, interceptedReq.Method, interceptedReq.URL)
		}

		elapsed := time.Since(start).Milliseconds()
		logger.Info("%s %s %d %db %dms (h2)", interceptedReq.Method, interceptedReq.URL, resp.StatusCode, len(respBody), elapsed)

		logBody := internalhttp.Decompress(resp.Header, respBody)
		if internalhttp.IsBinary(resp.Header) {
			logBody = nil
		}

		p.bus.EmitResponse(events.ResponseEvent{
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
		})
	})

	h2srv := &http2.Server{
		MaxConcurrentStreams: h2MaxConcurrentStreams,
	}
	// ServeConn blocks until the browser closes the underlying TLS connection.
	// It handles all stream multiplexing internally. No idle timer is needed —
	// the connection lifetime is naturally tied to browserTLS.
	h2srv.ServeConn(browserTLS, &http2.ServeConnOpts{
		Handler: streamHandler,
	})
}
