package store

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// HAR 1.2 — https://w3c.github.io/web-performance/specs/HAR/Overview.html

type HAR struct {
	Log HARLog `json:"log"`
}

type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type HAREntry struct {
	StartedDateTime string      `json:"startedDateTime"`
	Time            int64       `json:"time"`
	Request         HARRequest  `json:"request"`
	Response        HARResponse `json:"response"`
	Timings         HARTimings  `json:"timings"`
}

type HARRequest struct {
	Method      string         `json:"method"`
	URL         string         `json:"url"`
	HTTPVersion string         `json:"httpVersion"`
	Headers     []HARNameValue `json:"headers"`
	QueryString []HARNameValue `json:"queryString"`
	PostData    *HARPostData   `json:"postData,omitempty"`
	HeadersSize int            `json:"headersSize"`
	BodySize    int            `json:"bodySize"`
}

type HARResponse struct {
	Status      int            `json:"status"`
	StatusText  string         `json:"statusText"`
	HTTPVersion string         `json:"httpVersion"`
	Headers     []HARNameValue `json:"headers"`
	Content     HARContent     `json:"content"`
	RedirectURL string         `json:"redirectURL"`
	HeadersSize int            `json:"headersSize"`
	BodySize    int            `json:"bodySize"`
}

type HARNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HARPostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
}

type HARContent struct {
	Size     int    `json:"size"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
}

type HARTimings struct {
	Send    int64 `json:"send"`
	Wait    int64 `json:"wait"`
	Receive int64 `json:"receive"`
}

// ExportHAR serialises the given transactions into a HAR 1.2 JSON byte slice.
// Pass only the transactions you want exported — scope and out-of-scope
// filtering is the caller's responsibility.
func ExportHAR(txs []Transaction) ([]byte, error) {
	har := HAR{
		Log: HARLog{
			Version: "1.2",
			Creator: HARCreator{
				Name:    "Shiv",
				Version: "1.0",
			},
			Entries: make([]HAREntry, 0, len(txs)),
		},
	}

	for _, tx := range txs {
		entry := HAREntry{
			StartedDateTime: tx.Timestamp.UTC().Format(time.RFC3339),
			Time:            tx.DurationMs,
			Request:         buildHARRequest(tx),
			Response:        buildHARResponse(tx),
			Timings: HARTimings{
				Send:    0,
				Wait:    tx.DurationMs,
				Receive: 0,
			},
		}
		har.Log.Entries = append(har.Log.Entries, entry)
	}

	data, err := json.MarshalIndent(har, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("har: marshal: %w", err)
	}
	return data, nil
}

func stripDefaultPort(rawURL string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if !strings.HasPrefix(rawURL, prefix) {
			continue
		}
		rest := rawURL[len(prefix):]
		slash := strings.Index(rest, "/")
		if slash < 0 {
			slash = len(rest)
		}
		host := rest[:slash]
		path := rest[slash:]
		h, port, err := net.SplitHostPort(host)
		if err != nil {
			return rawURL
		}
		if (prefix == "https://" && port == "443") || (prefix == "http://" && port == "80") {
			return prefix + h + path
		}
		return rawURL
	}
	return rawURL
}

func buildHARRequest(tx Transaction) HARRequest {
	headers := make([]HARNameValue, 0, len(tx.ReqHeaders))
	for k, vals := range tx.ReqHeaders {
		for _, v := range vals {
			headers = append(headers, HARNameValue{Name: k, Value: v})
		}
	}

	httpVersion := tx.Proto
	if httpVersion == "" {
		httpVersion = "HTTP/1.1"
	}

	req := HARRequest{
		Method:      tx.Method,
		URL:         stripDefaultPort(tx.URL),
		HTTPVersion: httpVersion,
		Headers:     headers,
		QueryString: extractQueryString(tx.URL),
		HeadersSize: -1,
		BodySize:    len(tx.ReqBody),
	}

	if len(tx.ReqBody) > 0 {
		mimeType := tx.ReqHeaders.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		req.PostData = &HARPostData{
			MimeType: mimeType,
			Text:     string(tx.ReqBody),
		}
	}

	return req
}

func buildHARResponse(tx Transaction) HARResponse {
	headers := make([]HARNameValue, 0, len(tx.RespHeaders))
	for k, vals := range tx.RespHeaders {
		for _, v := range vals {
			headers = append(headers, HARNameValue{Name: k, Value: v})
		}
	}

	httpVersion := tx.Proto
	if httpVersion == "" {
		httpVersion = "HTTP/1.1"
	}

	mimeType := tx.RespHeaders.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	redirectURL := tx.RespHeaders.Get("Location")

	return HARResponse{
		Status:      tx.StatusCode,
		StatusText:  statusText(tx.StatusCode),
		HTTPVersion: httpVersion,
		Headers:     headers,
		Content: HARContent{
			Size:     len(tx.RespBody),
			MimeType: mimeType,
			Text:     string(tx.RespBody),
		},
		RedirectURL: redirectURL,
		HeadersSize: -1,
		BodySize:    len(tx.RespBody),
	}
}

func extractQueryString(rawURL string) []HARNameValue {
	idx := -1
	for i, c := range rawURL {
		if c == '?' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return []HARNameValue{}
	}
	query := rawURL[idx+1:]
	var pairs []HARNameValue
	for _, part := range splitQuery(query) {
		if part == "" {
			continue
		}
		i := -1
		for j, c := range part {
			if c == '=' {
				i = j
				break
			}
		}
		if i < 0 {
			pairs = append(pairs, HARNameValue{Name: part, Value: ""})
		} else {
			pairs = append(pairs, HARNameValue{Name: part[:i], Value: part[i+1:]})
		}
	}
	if pairs == nil {
		return []HARNameValue{}
	}
	return pairs
}

func splitQuery(q string) []string {
	var parts []string
	start := 0
	for i, c := range q {
		if c == '&' {
			parts = append(parts, q[start:i])
			start = i + 1
		}
	}
	parts = append(parts, q[start:])
	return parts
}

func statusText(code int) string {
	texts := map[int]string{
		100: "Continue", 101: "Switching Protocols",
		200: "OK", 201: "Created", 202: "Accepted", 204: "No Content",
		206: "Partial Content",
		301: "Moved Permanently", 302: "Found", 303: "See Other",
		304: "Not Modified", 307: "Temporary Redirect", 308: "Permanent Redirect",
		400: "Bad Request", 401: "Unauthorized", 403: "Forbidden",
		404: "Not Found", 405: "Method Not Allowed", 408: "Request Timeout",
		409: "Conflict", 410: "Gone", 413: "Payload Too Large",
		415: "Unsupported Media Type", 422: "Unprocessable Entity",
		429: "Too Many Requests",
		500: "Internal Server Error", 501: "Not Implemented",
		502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout",
	}
	if t, ok := texts[code]; ok {
		return t
	}
	return fmt.Sprintf("%d", code)
}
