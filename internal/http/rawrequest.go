package http

import (
	"bufio"
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
	TimeoutMs int               // 0 means use defaults (10s dial, 30s total)
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

	timeout := 30 * time.Second
	if opts.TimeoutMs > 0 {
		timeout = time.Duration(opts.TimeoutMs) * time.Millisecond
	}

	var conn net.Conn
	var err error
	if opts.TLS {
		dialer := &net.Dialer{Timeout: timeout}
		tlsCfg := &tls.Config{
			ServerName:         opts.Host,
			InsecureSkipVerify: true, //nolint:gosec
		}
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = net.DialTimeout("tcp", addr, timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	raw := NormalizeLineEndings(opts.RawReq)
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

	body, err := io.ReadAll(io.LimitReader(httpResp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	body = Decompress(httpResp.Header, body)
	body = TruncateBody(body)
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

// SplitRaw splits a raw HTTP message into its header section and body at the
// first blank line. Handles both CRLF and LF delimiters. The returned header
// section does not include the blank-line separator.
func SplitRaw(raw string) (header string, body []byte) {
	if idx := strings.Index(raw, "\r\n\r\n"); idx >= 0 {
		return raw[:idx], []byte(raw[idx+4:])
	}
	if idx := strings.Index(raw, "\n\n"); idx >= 0 {
		return raw[:idx], []byte(raw[idx+2:])
	}
	return raw, nil
}

// NormalizeLineEndings ensures CRLF throughout the header section only.
// It does NOT touch the body to avoid corrupting binary or multi-byte content.
func NormalizeLineEndings(raw string) string {
	header, body := SplitRaw(raw)
	header = strings.ReplaceAll(header, "\r\n", "\n")
	header = strings.ReplaceAll(header, "\n", "\r\n")
	return header + "\r\n\r\n" + string(body)
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
		out = append(out, fmt.Sprintf("Content-Length: %d", len(body)))
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
