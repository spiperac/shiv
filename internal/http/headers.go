package http

import (
	"net"
	"net/http"
)

// NormalizeHost strips the port from a Host header value when it is the
// default for the scheme (443 for HTTPS, 80 for HTTP), so that we never
// send "Host: example.com:443" to an upstream server.
func NormalizeHost(host string, tls bool) string {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	if (tls && port == "443") || (!tls && port == "80") {
		return h
	}
	return host
}

// StripRequestCacheHeaders removes conditional-request headers so the
// upstream always returns a fresh response.
func StripRequestCacheHeaders(h http.Header) {
	h.Del("If-None-Match")
	h.Del("If-Modified-Since")
	h.Del("If-Range")
	h.Del("If-Match")
	h.Del("If-Unmodified-Since")
}

// StripResponseCacheHeaders removes caching directives from a response and
// sets no-store so the browser never serves a stale cached copy.
func StripResponseCacheHeaders(h http.Header) {
	h.Del("Cache-Control")
	h.Del("Expires")
	h.Del("ETag")
	h.Del("Last-Modified")
	h.Del("Pragma")
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate")
}

// HopByHop is the set of headers that must not be forwarded to the upstream.
var HopByHop = map[string]bool{
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
