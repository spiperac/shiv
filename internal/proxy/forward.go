package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
)

var upstreamClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func forward(req *http.Request) (*http.Response, error) {
	upstreamRequest, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		return nil, fmt.Errorf("forward: build request: %w", err)
	}
	for headerKey, headerValues := range req.Header {
		if hopByHop[headerKey] {
			continue
		}
		upstreamRequest.Header[headerKey] = headerValues
	}
	stripRequestCacheHeaders(upstreamRequest.Header)
	upstreamResponse, err := upstreamClient.Do(upstreamRequest)
	if err != nil {
		return nil, fmt.Errorf("forward: upstream request: %w", err)
	}
	stripResponseCacheHeaders(upstreamResponse.Header)
	return upstreamResponse, nil
}

func stripRequestCacheHeaders(h http.Header) {
	h.Del("If-None-Match")
	h.Del("If-Modified-Since")
	h.Del("If-Range")
	h.Del("If-Match")
	h.Del("If-Unmodified-Since")
}

func stripResponseCacheHeaders(h http.Header) {
	h.Del("Cache-Control")
	h.Del("Expires")
	h.Del("ETag")
	h.Del("Last-Modified")
	h.Del("Pragma")
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
}

func decompressBody(header http.Header, body []byte) []byte {
	contentEncoding := strings.ToLower(header.Get("Content-Encoding"))
	bodyReader := bytes.NewReader(body)
	switch contentEncoding {
	case "gzip":
		gzipReader, err := gzip.NewReader(bodyReader)
		if err != nil {
			return body
		}
		defer gzipReader.Close()
		out, err := io.ReadAll(gzipReader)
		if err != nil {
			return body
		}
		return out
	case "deflate":
		out, err := io.ReadAll(flate.NewReader(bodyReader))
		if err != nil {
			return body
		}
		return out
	case "br":
		out, err := io.ReadAll(brotli.NewReader(bodyReader))
		if err != nil {
			return body
		}
		return out
	case "zstd":
		return body
	}
	return body
}

func isBinary(header http.Header) bool {
	contentEncoding := strings.ToLower(header.Get("Content-Encoding"))
	if contentEncoding == "zstd" {
		return true
	}
	contentType := strings.ToLower(header.Get("Content-Type"))
	if contentType == "" {
		return false
	}
	textTypes := []string{
		"text/", "application/json", "application/xml",
		"application/javascript", "application/x-www-form-urlencoded",
		"application/xhtml", "application/ld+json",
	}
	for _, textType := range textTypes {
		if strings.Contains(contentType, textType) {
			return false
		}
	}
	return true
}

var hopByHop = map[string]bool{
	"Connection":          true,
	"Proxy-Connection":    true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}
