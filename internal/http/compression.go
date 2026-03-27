package http

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Decompress decompresses body according to the Content-Encoding header.
// Returns the original body unchanged if encoding is unknown or decompression fails.
//
// Deflate note: servers claiming "deflate" Content-Encoding almost universally
// send zlib-wrapped data (RFC 1950), not raw DEFLATE (RFC 1951). We try zlib
// first and fall back to raw deflate so both are handled correctly.
func Decompress(header http.Header, body []byte) []byte {
	encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Encoding")))

	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(bytes.NewReader(body))
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
		// Try zlib (RFC 1950) first — the de-facto standard despite the name.
		if out, err := decompressZlib(body); err == nil {
			return out
		}
		// Fall back to raw DEFLATE (RFC 1951).
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
		if err != nil {
			return body
		}
		return out

	case "br":
		out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
		if err != nil {
			return body
		}
		return out

	case "zstd":
		dec, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			return body
		}
		defer dec.Close()
		out, err := io.ReadAll(dec)
		if err != nil {
			return body
		}
		return out

	case "", "identity":
		// No encoding or explicit identity — return as-is.
		return body
	}

	// Unknown encoding — return original bytes unchanged.
	return body
}

// decompressZlib attempts to decompress body as zlib (RFC 1950).
func decompressZlib(body []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// IsBinary returns true if the response body should not be stored or displayed as text.
//
// If Content-Encoding is a known compression we support, the body will be
// decompressed to text before this is called, so those are not binary.
// Unknown or future Content-Encoding values are treated as binary since we
// cannot safely decode them.
func IsBinary(header http.Header) bool {
	encoding := strings.ToLower(strings.TrimSpace(header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity", "gzip", "deflate", "br", "zstd":
		// Known encodings we can decompress — fall through to Content-Type check.
	default:
		// Unknown encoding — we can't decode it, treat as binary.
		return true
	}

	ct := strings.ToLower(header.Get("Content-Type"))
	if ct == "" {
		// No Content-Type — assume text so we don't silently discard bodies.
		return false
	}

	textPrefixes := []string{
		"text/",
		"application/json",
		"application/xml",
		"application/javascript",
		"application/x-www-form-urlencoded",
		"application/xhtml",
		"application/ld+json",
		"application/graphql",
		"application/x-yaml",
		"application/yaml",
	}
	for _, prefix := range textPrefixes {
		if strings.Contains(ct, prefix) {
			return false
		}
	}
	return true
}
