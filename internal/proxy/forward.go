package proxy

import (
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
	out, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		return nil, fmt.Errorf("forward: build request: %w", err)
	}
	for k, vv := range req.Header {
		if hopByHop[k] {
			continue
		}
		out.Header[k] = vv
	}
	resp, err := upstreamClient.Do(out)
	if err != nil {
		return nil, fmt.Errorf("forward: upstream request: %w", err)
	}
	return resp, nil
}

// decompressBody decompresses the body if Content-Encoding is set.
// Returns the original body if encoding is unknown or decompression fails.
func decompressBody(header http.Header, body []byte) []byte {
	ce := strings.ToLower(header.Get("Content-Encoding"))
	r := strings.NewReader(string(body))
	switch ce {
	case "gzip":
		gr, err := gzip.NewReader(r)
		if err != nil {
			return body
		}
		defer gr.Close()
		out, err := io.ReadAll(gr)
		if err != nil {
			return body
		}
		return out
	case "deflate":
		out, err := io.ReadAll(flate.NewReader(r))
		if err != nil {
			return body
		}
		return out
	case "br":
		out, err := io.ReadAll(brotli.NewReader(r))
		if err != nil {
			return body
		}
		return out
	case "zstd":
		// zstd not in stdlib — skip decompression, mark as binary
		return body
	}
	return body
}

// isBinary returns true if the response body should not be stored or displayed.
// Call this AFTER decompressBody — compressed text is not binary once decoded.
func isBinary(header http.Header) bool {
	// zstd we can't decompress — treat as binary
	ce := strings.ToLower(header.Get("Content-Encoding"))
	if ce == "zstd" {
		return true
	}

	ct := strings.ToLower(header.Get("Content-Type"))
	if ct == "" {
		return false
	}
	textTypes := []string{
		"text/", "application/json", "application/xml",
		"application/javascript", "application/x-www-form-urlencoded",
		"application/xhtml", "application/ld+json",
	}
	for _, t := range textTypes {
		if strings.Contains(ct, t) {
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
