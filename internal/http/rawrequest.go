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
func SendRaw(opts RawRequestOptions) (*RawResponse, error) {
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))

	var conn net.Conn
	var err error
	if opts.TLS {
		conn, err = tls.Dial("tcp", addr, &tls.Config{
			ServerName:         opts.Host,
			InsecureSkipVerify: true, //nolint:gosec
		})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck

	raw := normalizeLineEndings(opts.RawReq)
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
		body = append(body[:64*1024], []byte("\n... truncated")...)
	}

	raw = buildRawResponse(httpResp, body)

	return &RawResponse{
		Raw:        raw,
		StatusCode: httpResp.StatusCode,
		Headers:    httpResp.Header,
		Body:       body,
		Cookies:    httpResp.Cookies(),
	}, nil
}

// ParseHostFromRaw extracts host, port, and TLS flag from the Host header
// in a raw HTTP request string.
func ParseHostFromRaw(raw string) (host string, port int, useTLS bool) {
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "host:") {
			continue
		}
		hostVal := strings.TrimSpace(line[5:])
		if h, portStr, err := net.SplitHostPort(hostVal); err == nil {
			host = h
			port, _ = strconv.Atoi(portStr)
			useTLS = port == 443
		} else {
			host = hostVal
			if net.ParseIP(hostVal) != nil {
				port = 80
				useTLS = false
			} else {
				port = 443
				useTLS = true
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

// normalizeLineEndings ensures CRLF throughout and folds any continuation lines.
func normalizeLineEndings(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	raw = strings.ReplaceAll(raw, "\r\n ", " ")
	raw = strings.ReplaceAll(raw, "\r\n\t", " ")
	return raw
}

// rewriteHeaders rewrites the header section of a raw request:
//   - strips Accept-Encoding (we handle decompression ourselves)
//   - strips Content-Length (we recalculate)
//   - strips Cookie (replaced by jar if provided)
//   - strips the port from Host when it is the default for the scheme
//   - appends Cookie header from jar
//   - appends correct Content-Length
func rewriteHeaders(raw string, jar map[string]string) string {
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	headerSection := parts[0]
	body := ""
	if len(parts) == 2 {
		body = strings.TrimRight(parts[1], "\r\n")
	}

	lines := strings.Split(headerSection, "\r\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "cookie:"):
			// Only drop the original Cookie header if we have jar entries to
			// replace it with. Otherwise preserve the cookies from the raw request.
			if len(jar) == 0 {
				out = append(out, line)
			}
			// Either way do not fall through to default — jar entries are appended below.
		case strings.HasPrefix(lower, "accept-encoding:"):
			continue
		case strings.HasPrefix(lower, "content-length:"):
			continue
		case strings.HasPrefix(lower, "host:"):
			hostVal := strings.TrimSpace(line[5:])
			// Strip default port from Host header.
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

	if len(jar) > 0 {
		cookies := make([]string, 0, len(jar))
		for k, v := range jar {
			cookies = append(cookies, k+"="+v)
		}
		out = append(out, "Cookie: "+strings.Join(cookies, "; "))
	}

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
