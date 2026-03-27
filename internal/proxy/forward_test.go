package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"net/http"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	internalhttp "github.com/shiv/internal/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decompressBody(h http.Header, body []byte) []byte { return internalhttp.Decompress(h, body) }
func isBinary(h http.Header) bool                      { return internalhttp.IsBinary(h) }
func stripRequestCacheHeaders(h http.Header)           { internalhttp.StripRequestCacheHeaders(h) }
func stripResponseCacheHeaders(h http.Header)          { internalhttp.StripResponseCacheHeaders(h) }

// ── decompressBody ────────────────────────────────────────────────────────────

func TestDecompressBody_NoEncoding(t *testing.T) {
	body := []byte("plain text")
	out := decompressBody(http.Header{}, body)
	assert.Equal(t, body, out)
}

func TestDecompressBody_Gzip(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write([]byte("gzip content"))
	require.NoError(t, err)
	w.Close()

	h := http.Header{"Content-Encoding": []string{"gzip"}}
	out := decompressBody(h, buf.Bytes())
	assert.Equal(t, "gzip content", string(out))
}

func TestDecompressBody_Gzip_CorruptData(t *testing.T) {
	body := []byte("not gzip")
	h := http.Header{"Content-Encoding": []string{"gzip"}}
	out := decompressBody(h, body)
	assert.Equal(t, body, out, "corrupt gzip should return original bytes")
}

func TestDecompressBody_Deflate(t *testing.T) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	require.NoError(t, err)
	_, err = w.Write([]byte("deflate content"))
	require.NoError(t, err)
	w.Close()

	h := http.Header{"Content-Encoding": []string{"deflate"}}
	out := decompressBody(h, buf.Bytes())
	assert.Equal(t, "deflate content", string(out))
}

func TestDecompressBody_Brotli(t *testing.T) {
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	_, err := w.Write([]byte("brotli content"))
	require.NoError(t, err)
	w.Close()

	h := http.Header{"Content-Encoding": []string{"br"}}
	out := decompressBody(h, buf.Bytes())
	assert.Equal(t, "brotli content", string(out))
}

func TestDecompressBody_Zstd(t *testing.T) {
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	compressed := enc.EncodeAll([]byte("zstd content"), nil)

	h := http.Header{"Content-Encoding": []string{"zstd"}}
	out := decompressBody(h, compressed)
	assert.Equal(t, "zstd content", string(out))
}

func TestDecompressBody_UnknownEncoding(t *testing.T) {
	body := []byte("mystery")
	h := http.Header{"Content-Encoding": []string{"identity"}}
	out := decompressBody(h, body)
	assert.Equal(t, body, out)
}

// ── isBinary ─────────────────────────────────────────────────────────────────

func TestIsBinary_PlainText(t *testing.T) {
	h := http.Header{"Content-Type": []string{"text/plain"}}
	assert.False(t, isBinary(h))
}

func TestIsBinary_HTML(t *testing.T) {
	h := http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}
	assert.False(t, isBinary(h))
}

func TestIsBinary_JSON(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/json"}}
	assert.False(t, isBinary(h))
}

func TestIsBinary_XML(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/xml"}}
	assert.False(t, isBinary(h))
}

func TestIsBinary_JavaScript(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/javascript"}}
	assert.False(t, isBinary(h))
}

func TestIsBinary_FormEncoded(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	assert.False(t, isBinary(h))
}

func TestIsBinary_Image(t *testing.T) {
	h := http.Header{"Content-Type": []string{"image/png"}}
	assert.True(t, isBinary(h))
}

func TestIsBinary_OctetStream(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/octet-stream"}}
	assert.True(t, isBinary(h))
}

func TestIsBinary_NoContentType(t *testing.T) {
	assert.False(t, isBinary(http.Header{}))
}

func TestIsBinary_ZstdEncoding(t *testing.T) {
	h := http.Header{
		"Content-Encoding": []string{"zstd"},
		"Content-Type":     []string{"text/plain"},
	}
	assert.False(t, isBinary(h))
}

// ── stripRequestCacheHeaders ──────────────────────────────────────────────────

func TestStripRequestCacheHeaders(t *testing.T) {
	h := http.Header{
		"If-None-Match":       []string{`"etag123"`},
		"If-Modified-Since":   []string{"Mon, 01 Jan 2024 00:00:00 GMT"},
		"If-Range":            []string{`"etag123"`},
		"If-Match":            []string{`"etag123"`},
		"If-Unmodified-Since": []string{"Mon, 01 Jan 2024 00:00:00 GMT"},
		"Authorization":       []string{"Bearer token"},
	}
	stripRequestCacheHeaders(h)

	assert.Empty(t, h.Get("If-None-Match"))
	assert.Empty(t, h.Get("If-Modified-Since"))
	assert.Empty(t, h.Get("If-Range"))
	assert.Empty(t, h.Get("If-Match"))
	assert.Empty(t, h.Get("If-Unmodified-Since"))
	assert.Equal(t, "Bearer token", h.Get("Authorization"))
}

func TestStripRequestCacheHeaders_AlreadyAbsent(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/json"}}
	assert.NotPanics(t, func() { stripRequestCacheHeaders(h) })
	assert.Equal(t, "application/json", h.Get("Content-Type"))
}

// ── stripResponseCacheHeaders ─────────────────────────────────────────────────

func TestStripResponseCacheHeaders(t *testing.T) {
	h := http.Header{
		"Cache-Control": []string{"max-age=3600"},
		"Expires":       []string{"Mon, 01 Jan 2025 00:00:00 GMT"},
		"ETag":          []string{`"etag123"`},
		"Last-Modified": []string{"Mon, 01 Jan 2024 00:00:00 GMT"},
		"Pragma":        []string{"no-cache"},
		"Content-Type":  []string{"text/html"},
	}
	stripResponseCacheHeaders(h)

	assert.Equal(t, "no-store, no-cache, must-revalidate", h.Get("Cache-Control"))
	assert.Empty(t, h.Get("Expires"))
	assert.Empty(t, h.Get("ETag"))
	assert.Empty(t, h.Get("Last-Modified"))
	assert.Empty(t, h.Get("Pragma"))
	assert.Equal(t, "text/html", h.Get("Content-Type"))
}

func TestStripResponseCacheHeaders_SetsNoCacheDirective(t *testing.T) {
	h := http.Header{}
	stripResponseCacheHeaders(h)
	assert.Equal(t, "no-store, no-cache, must-revalidate", h.Get("Cache-Control"))
}
