package http

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── NormalizeHost ─────────────────────────────────────────────────────────────

func TestNormalizeHost_TLS_443_Stripped(t *testing.T) {
	assert.Equal(t, "example.com", NormalizeHost("example.com:443", true))
}

func TestNormalizeHost_Plain_80_Stripped(t *testing.T) {
	assert.Equal(t, "example.com", NormalizeHost("example.com:80", false))
}

func TestNormalizeHost_TLS_NonDefault_Kept(t *testing.T) {
	assert.Equal(t, "example.com:8443", NormalizeHost("example.com:8443", true))
}

func TestNormalizeHost_Plain_NonDefault_Kept(t *testing.T) {
	assert.Equal(t, "example.com:8080", NormalizeHost("example.com:8080", false))
}

func TestNormalizeHost_Plain_443_Kept(t *testing.T) {
	// port 443 on plain (non-TLS) should not be stripped
	assert.Equal(t, "example.com:443", NormalizeHost("example.com:443", false))
}

func TestNormalizeHost_TLS_80_Kept(t *testing.T) {
	// port 80 on TLS should not be stripped
	assert.Equal(t, "example.com:80", NormalizeHost("example.com:80", true))
}

func TestNormalizeHost_NoPort_ReturnedAsIs(t *testing.T) {
	assert.Equal(t, "example.com", NormalizeHost("example.com", true))
}

func TestNormalizeHost_IP_WithPort443(t *testing.T) {
	assert.Equal(t, "127.0.0.1", NormalizeHost("127.0.0.1:443", true))
}

// ── StripRequestCacheHeaders ──────────────────────────────────────────────────

func TestStripRequestCacheHeaders_AllStripped(t *testing.T) {
	h := http.Header{
		"If-None-Match":       []string{`"etag"`},
		"If-Modified-Since":   []string{"Mon, 01 Jan 2024 00:00:00 GMT"},
		"If-Range":            []string{`"etag"`},
		"If-Match":            []string{`"etag"`},
		"If-Unmodified-Since": []string{"Mon, 01 Jan 2024 00:00:00 GMT"},
		"Authorization":       []string{"Bearer token"},
	}
	StripRequestCacheHeaders(h)

	assert.Empty(t, h.Get("If-None-Match"))
	assert.Empty(t, h.Get("If-Modified-Since"))
	assert.Empty(t, h.Get("If-Range"))
	assert.Empty(t, h.Get("If-Match"))
	assert.Empty(t, h.Get("If-Unmodified-Since"))
	assert.Equal(t, "Bearer token", h.Get("Authorization"))
}

func TestStripRequestCacheHeaders_AlreadyAbsent(t *testing.T) {
	h := http.Header{"Content-Type": []string{"application/json"}}
	assert.NotPanics(t, func() { StripRequestCacheHeaders(h) })
	assert.Equal(t, "application/json", h.Get("Content-Type"))
}

// ── StripResponseCacheHeaders ─────────────────────────────────────────────────

func TestStripResponseCacheHeaders_AllStripped(t *testing.T) {
	h := http.Header{
		"Cache-Control": []string{"max-age=3600"},
		"Expires":       []string{"Mon, 01 Jan 2025 00:00:00 GMT"},
		"ETag":          []string{`"etag"`},
		"Last-Modified": []string{"Mon, 01 Jan 2024 00:00:00 GMT"},
		"Pragma":        []string{"no-cache"},
		"Content-Type":  []string{"text/html"},
	}
	StripResponseCacheHeaders(h)

	assert.Equal(t, "no-store, no-cache, must-revalidate", h.Get("Cache-Control"))
	assert.Empty(t, h.Get("Expires"))
	assert.Empty(t, h.Get("ETag"))
	assert.Empty(t, h.Get("Last-Modified"))
	assert.Empty(t, h.Get("Pragma"))
	assert.Equal(t, "text/html", h.Get("Content-Type"))
}

func TestStripResponseCacheHeaders_SetEvenWhenAbsent(t *testing.T) {
	h := http.Header{}
	StripResponseCacheHeaders(h)
	assert.Equal(t, "no-store, no-cache, must-revalidate", h.Get("Cache-Control"))
}

// ── HopByHop ──────────────────────────────────────────────────────────────────

func TestHopByHop_ContainsExpectedHeaders(t *testing.T) {
	expected := []string{
		"Connection", "Proxy-Connection", "Keep-Alive",
		"Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
	}
	for _, h := range expected {
		assert.True(t, HopByHop[h], "expected %s in HopByHop", h)
	}
}

func TestHopByHop_DoesNotContainRegularHeaders(t *testing.T) {
	assert.False(t, HopByHop["Content-Type"])
	assert.False(t, HopByHop["Authorization"])
	assert.False(t, HopByHop["Host"])
}
