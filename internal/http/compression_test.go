package http

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Decompress ────────────────────────────────────────────────────────────────

func TestDecompress_NoEncoding(t *testing.T) {
	body := []byte("plain")
	assert.Equal(t, body, Decompress(http.Header{}, body))
}

func TestDecompress_UnknownEncoding(t *testing.T) {
	body := []byte("mystery")
	assert.Equal(t, body, Decompress(http.Header{"Content-Encoding": []string{"identity"}}, body))
}

func TestDecompress_Gzip(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write([]byte("gzip content"))
	require.NoError(t, err)
	w.Close()
	assert.Equal(t, "gzip content", string(Decompress(http.Header{"Content-Encoding": []string{"gzip"}}, buf.Bytes())))
}

func TestDecompress_Gzip_Corrupt(t *testing.T) {
	body := []byte("not gzip")
	assert.Equal(t, body, Decompress(http.Header{"Content-Encoding": []string{"gzip"}}, body))
}

func TestDecompress_Deflate(t *testing.T) {
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	require.NoError(t, err)
	w.Write([]byte("deflate content"))
	w.Close()
	assert.Equal(t, "deflate content", string(Decompress(http.Header{"Content-Encoding": []string{"deflate"}}, buf.Bytes())))
}

func TestDecompress_Deflate_Corrupt(t *testing.T) {
	body := []byte("not deflate")
	assert.Equal(t, body, Decompress(http.Header{"Content-Encoding": []string{"deflate"}}, body))
}

func TestDecompress_Brotli(t *testing.T) {
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	w.Write([]byte("brotli content"))
	w.Close()
	assert.Equal(t, "brotli content", string(Decompress(http.Header{"Content-Encoding": []string{"br"}}, buf.Bytes())))
}

func TestDecompress_Brotli_Corrupt(t *testing.T) {
	body := []byte("not brotli")
	assert.Equal(t, body, Decompress(http.Header{"Content-Encoding": []string{"br"}}, body))
}

func TestDecompress_Zstd(t *testing.T) {
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	compressed := enc.EncodeAll([]byte("zstd content"), nil)
	assert.Equal(t, "zstd content", string(Decompress(http.Header{"Content-Encoding": []string{"zstd"}}, compressed)))
}

func TestDecompress_Zstd_Corrupt(t *testing.T) {
	body := []byte("not zstd")
	assert.Equal(t, body, Decompress(http.Header{"Content-Encoding": []string{"zstd"}}, body))
}

func TestDecompress_CaseInsensitive(t *testing.T) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write([]byte("case"))
	w.Close()
	assert.Equal(t, "case", string(Decompress(http.Header{"Content-Encoding": []string{"GZIP"}}, buf.Bytes())))
}

func TestDecompress_EmptyBody(t *testing.T) {
	assert.Equal(t, []byte{}, Decompress(http.Header{"Content-Encoding": []string{"gzip"}}, []byte{}))
}

func TestDecompress_LargeBody(t *testing.T) {
	large := []byte(strings.Repeat("a", 100000))
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(large)
	w.Close()
	out := Decompress(http.Header{"Content-Encoding": []string{"gzip"}}, buf.Bytes())
	assert.Equal(t, large, out)
}

// ── IsBinary ──────────────────────────────────────────────────────────────────

func TestIsBinary_KnownEncodingsNotBinary(t *testing.T) {
	// Known encodings we can decompress fall through to the Content-Type check.
	// image/png is still binary even with a known encoding — the encoding tells
	// us we can decompress it, but the content type determines binary/text.
	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		h := http.Header{
			"Content-Encoding": []string{enc},
			"Content-Type":     []string{"text/plain"},
		}
		assert.False(t, IsBinary(h), "encoding %s with text/plain should not be binary", enc)
	}
	// image/png with a known encoding is still binary.
	for _, enc := range []string{"gzip", "deflate", "br", "zstd"} {
		h := http.Header{
			"Content-Encoding": []string{enc},
			"Content-Type":     []string{"image/png"},
		}
		assert.True(t, IsBinary(h), "encoding %s with image/png should be binary", enc)
	}
}

func TestIsBinary_BinaryContentTypes(t *testing.T) {
	for _, ct := range []string{"image/png", "image/jpeg", "application/octet-stream", "video/mp4", "audio/mpeg"} {
		h := http.Header{"Content-Type": []string{ct}}
		assert.True(t, IsBinary(h), "%s should be binary", ct)
	}
}

func TestIsBinary_TextContentTypes(t *testing.T) {
	for _, ct := range []string{
		"text/plain", "text/html", "text/css",
		"application/json", "application/xml",
		"application/javascript", "application/x-www-form-urlencoded",
		"application/xhtml+xml", "application/ld+json",
	} {
		h := http.Header{"Content-Type": []string{ct}}
		assert.False(t, IsBinary(h), "%s should not be binary", ct)
	}
}

func TestIsBinary_EmptyContentType(t *testing.T) {
	assert.False(t, IsBinary(http.Header{"Content-Type": []string{""}}))
}

func TestIsBinary_NoHeaders(t *testing.T) {
	assert.False(t, IsBinary(http.Header{}))
}
