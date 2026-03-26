package http

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Decompress decompresses body according to the Content-Encoding header.
// Returns the original body unchanged if encoding is unknown or decompression fails.
func Decompress(header http.Header, body []byte) []byte {
	encoding := strings.ToLower(header.Get("Content-Encoding"))
	r := bytes.NewReader(body)

	switch encoding {
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
		dec, err := zstd.NewReader(r)
		if err != nil {
			return body
		}
		defer dec.Close()
		out, err := io.ReadAll(dec)
		if err != nil {
			return body
		}
		return out
	}

	return body
}

// IsBinary returns true if the response body should not be stored or displayed as text.
func IsBinary(header http.Header) bool {
	// If it has a content-encoding we can decompress, it's not binary.
	encoding := strings.ToLower(header.Get("Content-Encoding"))
	switch encoding {
	case "gzip", "deflate", "br", "zstd":
		return false
	}

	ct := strings.ToLower(header.Get("Content-Type"))
	if ct == "" {
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
	}
	for _, prefix := range textPrefixes {
		if strings.Contains(ct, prefix) {
			return false
		}
	}
	return true
}
