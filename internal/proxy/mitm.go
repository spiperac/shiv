package proxy

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		logger.Error("server does not support hijacking")
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		logger.Error("hijack: %v", err)
		return
	}
	defer clientConn.Close()

	upstreamConn, err := net.Dial("tcp", r.Host)
	if err != nil {
		logger.Error("dial upstream %s: %v", r.Host, err)
		return
	}
	defer upstreamConn.Close()

	tlsCert, err := p.ca.TLSCertForHost(r.Host)
	if err != nil {
		logger.Error("cert for %s: %v", r.Host, err)
		return
	}

	browserTLS := tls.Server(clientConn, &tls.Config{
		Certificates: []tls.Certificate{*tlsCert},
		NextProtos:   []string{"http/1.1"},
	})
	if err := browserTLS.Handshake(); err != nil {
		logger.Error("browser TLS handshake for %s: %v", r.Host, err)
		return
	}
	defer browserTLS.Close()

	host, _, _ := net.SplitHostPort(r.Host)
	upstreamTLS := tls.Client(upstreamConn, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, //nolint:gosec
	})
	if err := upstreamTLS.Handshake(); err != nil {
		logger.Error("upstream TLS handshake for %s: %v", r.Host, err)
		return
	}
	defer upstreamTLS.Close()

	dialUpstream := func() (net.Conn, error) {
		conn, err := net.Dial("tcp", r.Host)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: true, //nolint:gosec
		})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return nil, err
		}
		return tlsConn, nil
	}

	browserReader := bufio.NewReader(browserTLS)
	for {
		req, err := http.ReadRequest(browserReader)
		if err != nil {
			logger.Debug("connection closed for %s: %v", r.Host, err)
			return
		}

		start := time.Now()

		reqBody, err := io.ReadAll(req.Body)
		if err != nil {
			logger.Error("read request body for %s: %v", r.Host, err)
			return
		}
		req.Body.Close()

		req.URL.Scheme = "https"
		req.URL.Host = r.Host

		interceptedReq, reqBody, shouldForward := p.store.Intercept.Hold(req, reqBody)
		if !shouldForward {
			resp := &http.Response{
				Status:     "403 Forbidden",
				StatusCode: 403,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Header:     make(http.Header),
			}
			resp.Write(browserTLS)
			continue
		}

		outReq, err := http.NewRequest(interceptedReq.Method, interceptedReq.URL.String(), bytes.NewReader(reqBody))
		if err != nil {
			logger.Error("build upstream request for %s: %v", r.Host, err)
			return
		}
		outReq.Header = interceptedReq.Header.Clone()

		upstreamHTTP := &http.Client{
			Transport: &http.Transport{
				DialTLS: func(network, addr string) (net.Conn, error) {
					return dialUpstream()
				},
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		resp, err := upstreamHTTP.Do(outReq)
		if err != nil {
			logger.Error("upstream request for %s: %v", r.Host, err)
			return
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			logger.Error("read response body for %s: %v", r.Host, err)
			return
		}

		elapsed := time.Since(start).Milliseconds()
		logger.Info("%s %s %d %db %dms", req.Method, req.URL, resp.StatusCode, len(respBody), elapsed)

		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		if err := resp.Write(browserTLS); err != nil {
			logger.Error("write response to browser for %s: %v", r.Host, err)
			return
		}

		logBody := decompressBody(resp.Header, respBody)
		if isBinary(resp.Header) {
			logBody = nil
		}

		if err := p.store.Log(store.Transaction{
			Timestamp:   start,
			Host:        r.Host,
			Method:      interceptedReq.Method,
			URL:         interceptedReq.URL.String(),
			ReqHeaders:  interceptedReq.Header,
			ReqBody:     reqBody,
			StatusCode:  resp.StatusCode,
			RespHeaders: resp.Header,
			RespBody:    logBody,
			DurationMs:  elapsed,
			TLS:         true,
			InScope:     true,
		}); err != nil {
			logger.Error("store transaction for %s: %v", r.Host, err)
		}
	}
}
