package http

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RawRequestOptions controls how SendRaw dials and sends.
type RawRequestOptions struct {
	Host      string
	Port      int
	TLS       bool
	RawReq    string
	CookieJar map[string]string // optional; merged into Cookie header
}

// RawResponse is returned by SendRaw.
type RawResponse struct {
	Raw        string // full response as text (status line + headers + body)
	StatusCode int
	Headers    http.Header
	Body       []byte
	Cookies    []*http.Cookie
}

// SendRaw dials host:port, sends rawReq over a plain or TLS connection, and
// returns the decompressed, decoded response. It is intentionally low-level
// so the caller (Repeater, Intruder) controls the exact bytes on the wire.
//
// The request line is always rewritten to HTTP/1.1 — H2 frames cannot be
// sent as raw text, and a raw socket is always HTTP/1.1.
func SendRaw(opts RawRequestOptions) (*RawResponse, error) {
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))

	var conn net.Conn
	var err error
	if opts.TLS {
		// Use DialTimeout via net.Dialer so TLS handshake is also bounded.
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		tlsCfg := &tls.Config{
			ServerName:         opts.Host,
			InsecureSkipVerify: true, //nolint:gosec
		}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	// Deadline covers the full round-trip after the connection is established.
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	raw := normalizeLineEndings(opts.RawReq)
	raw = rewriteRequestLine(raw)
	raw = rewriteHeaders(raw, opts.CookieJar)

	if _, err := fmt.Fprint(conn, raw); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	httpResp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	defer httpResp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 2<<20))
	body = Decompress(httpResp.Header, body)
	if len(body) > 64*1024 {
		notice := []byte("\n... truncated")
		truncated := make([]byte, 64*1024+len(notice))
		copy(truncated, body[:64*1024])
		copy(truncated[64*1024:], notice)
		body = truncated
	}

	rawResp := buildRawResponse(httpResp, body)

	return &RawResponse{
		Raw:        rawResp,
		StatusCode: httpResp.StatusCode,
		Headers:    httpResp.Header,
		Body:       body,
		Cookies:    httpResp.Cookies(),
	}, nil
}

// rewriteRequestLine rewrites the first line of a raw HTTP request to use
// HTTP/1.1, regardless of what was captured. HTTP/2 cannot be sent as raw
// text over a socket.
func rewriteRequestLine(raw string) string {
	idx := strings.Index(raw, "\r\n")
	if idx < 0 {
		return raw
	}
	firstLine := raw[:idx]
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return raw
	}
	// Rewrite: METHOD PATH HTTP/1.1
	newFirstLine := parts[0] + " " + parts[1] + " HTTP/1.1"
	return newFirstLine + raw[idx:]
}

// ParseHostFromRaw extracts host, port, and TLS flag from the Host header
// in a raw HTTP request string. The caller should pass the known TLS state
// from the captured transaction when available, as the Host header alone
// cannot distinguish HTTP from HTTPS.
func ParseHostFromRaw(raw string) (host string, port int, useTLS bool) {
	// Check the scheme in the request line first (e.g. absolute-form URLs).
	firstLine := strings.SplitN(raw, "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) >= 2 {
		url := parts[1]
		if strings.HasPrefix(url, "https://") {
			useTLS = true
		} else if strings.HasPrefix(url, "http://") {
			useTLS = false
		}
	}

	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "host:") {
			continue
		}
		hostVal := strings.TrimSpace(line[5:])
		if h, portStr, err := net.SplitHostPort(hostVal); err == nil {
			host = h
			port, _ = strconv.Atoi(portStr)
			// Port in Host header is the definitive signal.
			useTLS = port == 443
		} else {
			host = hostVal
			if net.ParseIP(hostVal) != nil {
				// Bare IP without port — can't tell, default to plaintext.
				port = 80
				useTLS = false
			} else {
				// Domain without port — use whatever we detected from the
				// request line scheme, or fall back to HTTPS.
				if port == 0 {
					if useTLS {
						port = 443
					} else {
						port = 80
					}
				}
				// If we got here without a scheme signal, default to HTTPS
				// since most modern interception is TLS.
				if port == 443 {
					useTLS = true
				}
			}
		}
		return
	}
	return "", 0, false
}

// ParseRawHeaders parses the headers section of a raw HTTP response string
// into an http.Header map.
func ParseRawHeaders(raw string) http.Header {
	headers := http.Header{}
	lines := strings.Split(raw, "\r\n")
	if len(lines) == 0 {
		return headers
	}
	// Skip the status/request line.
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			headers.Add(parts[0], parts[1])
		}
	}
	return headers
}

// normalizeLineEndings ensures CRLF throughout the header section only.
// It does NOT touch the body to avoid corrupting binary or multi-byte content.
func normalizeLineEndings(raw string) string {
	// Split into header section and body at the first blank line.
	// We look for both \r\n\r\n and \n\n to handle inputs from different sources.
	var headerSection, body string
	if idx := strings.Index(raw, "\r\n\r\n"); idx >= 0 {
		headerSection = raw[:idx]
		body = raw[idx+4:]
	} else if idx := strings.Index(raw, "\n\n"); idx >= 0 {
		headerSection = raw[:idx]
		body = raw[idx+2:]
	} else {
		// No blank line found — treat the whole thing as headers.
		headerSection = raw
		body = ""
	}

	// Normalize to CRLF in the header section only.
	// First collapse any existing \r\n to \n, then expand all \n to \r\n.
	headerSection = strings.ReplaceAll(headerSection, "\r\n", "\n")
	headerSection = strings.ReplaceAll(headerSection, "\n", "\r\n")

	return headerSection + "\r\n\r\n" + body
}

// rewriteHeaders rewrites the header section of a raw request:
//   - Injects Connection: close (required for single-use raw TCP connections)
//   - Strips Accept-Encoding (we handle decompression ourselves)
//   - Strips Content-Length (recalculated below)
//   - Strips Transfer-Encoding (not valid in HTTP/1.1 raw send context)
//   - Merges Cookie header from jar (replaces existing if jar is non-empty)
//   - Strips default port from Host when it matches the scheme
//   - Appends correct Content-Length based on byte length of body
func rewriteHeaders(raw string, jar map[string]string) string {
	// Split on the canonical blank line separator (normalizeLineEndings runs first).
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	headerSection := parts[0]
	body := ""
	if len(parts) == 2 {
		body = parts[1]
		// Trim trailing CRLF from body only if it was added by normalisation,
		// not if the user intentionally added trailing newlines.
		// We use the raw byte length for Content-Length regardless.
	}

	lines := strings.Split(headerSection, "\r\n")
	out := make([]string, 0, len(lines)+3)
	connectionSeen := false

	for _, line := range lines {
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "cookie:"):
			// Drop original Cookie header only if jar has entries to replace it.
			if len(jar) == 0 {
				out = append(out, line)
			}
		case strings.HasPrefix(lower, "accept-encoding:"):
			// Drop — we decompress ourselves so we want uncompressed bytes.
			continue
		case strings.HasPrefix(lower, "content-length:"):
			// Drop — we recalculate below from actual body byte length.
			continue
		case strings.HasPrefix(lower, "transfer-encoding:"):
			// Not valid to send in this raw H1 context.
			continue
		case strings.HasPrefix(lower, "connection:"):
			// Replace whatever was there with Connection: close.
			out = append(out, "Connection: close")
			connectionSeen = true
		case strings.HasPrefix(lower, "host:"):
			hostVal := strings.TrimSpace(line[5:])
			// Strip default ports from Host header.
			if h, port, err := net.SplitHostPort(hostVal); err == nil {
				if port == "443" || port == "80" {
					line = "Host: " + h
				}
			}
			out = append(out, line)
		default:
			out = append(out, line)
		}
	}

	// Ensure Connection: close is present even if the original had no Connection header.
	if !connectionSeen {
		out = append(out, "Connection: close")
	}

	// Append merged Cookie header from jar.
	if len(jar) > 0 {
		cookies := make([]string, 0, len(jar))
		for k, v := range jar {
			cookies = append(cookies, k+"="+v)
		}
		out = append(out, "Cookie: "+strings.Join(cookies, "; "))
	}

	// Append Content-Length based on actual byte length of the body.
	if len(body) > 0 {
		out = append(out, fmt.Sprintf("Content-Length: %d", len([]byte(body))))
	}

	return strings.Join(out, "\r\n") + "\r\n\r\n" + body
}

func buildRawResponse(resp *http.Response, body []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	for k, vals := range resp.Header {
		for _, v := range vals {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	b.Write(body)
	return b.String()
}

func FormatRequest(req *http.Request, body []byte) string {
	var builder bytes.Buffer
	path := req.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(&builder, "%s %s HTTP/1.1\r\n", req.Method, path)
	fmt.Fprintf(&builder, "Host: %s\r\n", req.Host)
	for headerKey, headerValues := range req.Header {
		for _, headerValue := range headerValues {
			fmt.Fprintf(&builder, "%s: %s\r\n", headerKey, headerValue)
		}
	}
	builder.WriteString("\r\n")
	if len(body) > 0 {
		builder.Write(body)
	}
	return builder.String()
}

// ExtractURL extracts the path from the first line of a raw HTTP request.
func ExtractURL(rawRequest string) string {
	parts := strings.Fields(strings.SplitN(rawRequest, "\n", 2)[0])
	if len(parts) >= 2 {
		return parts[1]
	}
	return "/"
}

// ExtractMethod extracts the HTTP method from the first line of a raw HTTP request.
func ExtractMethod(rawRequest string) string {
	parts := strings.Fields(strings.SplitN(rawRequest, "\n", 2)[0])
	if len(parts) >= 1 {
		return parts[0]
	}
	return "GET"
}
