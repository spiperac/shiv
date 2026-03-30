package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shiv/internal/store"
)

// PathOf extracts the path+query portion from a full URL string.
func PathOf(rawURL string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(rawURL, prefix) {
			rest := rawURL[len(prefix):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				return rest[slash:]
			}
			return "/"
		}
	}
	return rawURL
}

// PrettyJSON re-indents JSON, returning the original bytes on any error.
func PrettyJSON(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, body, "", "  "); err != nil {
		return body
	}
	return buf.Bytes()
}

// hostHeader returns the value to use for the Host header in a reconstructed
// raw request. Default ports (80 for HTTP, 443 for HTTPS) are omitted per
// RFC 7230. Non-standard ports are always included.
func hostHeader(tx store.Transaction) string {
	if tx.Port == 0 {
		return tx.Host
	}
	return tx.Host + ":" + strconv.Itoa(tx.Port)
}

// FormatStoreRequest serialises a Transaction into a raw HTTP/1.1 request string
// suitable for display in the UI or sending via Repeater/Intruder.
//
// The protocol is always written as HTTP/1.1 regardless of what was captured —
func FormatStoreRequest(tx store.Transaction) string {
	proto := tx.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s\r\n", tx.Method, PathOf(tx.URL), proto)
	fmt.Fprintf(&b, "Host: %s\r\n", hostHeader(tx))

	keys := sortedKeys(tx.ReqHeaders)
	for _, k := range keys {
		// Skip Host — we already wrote a canonical one above.
		if strings.EqualFold(k, "host") {
			continue
		}
		for _, v := range tx.ReqHeaders[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	if len(tx.ReqBody) > 0 {
		b.Write(PrettyJSON(tx.ReqBody))
	}
	return b.String()
}

// FormatStoreResponse serialises a Transaction into a raw HTTP response string
// suitable for display in the UI.
//
// The store already holds decompressed body bytes — we must NOT decompress again.
func FormatStoreResponse(tx store.Transaction) string {
	var b strings.Builder
	// Always render as HTTP/1.1; H2 pseudo-headers are not meaningful in raw text.
	const proto = "HTTP/1.1"
	fmt.Fprintf(&b, "%s %d\r\n", proto, tx.StatusCode)

	keys := sortedKeys(tx.RespHeaders)
	for _, k := range keys {
		for _, v := range tx.RespHeaders[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")

	if len(tx.RespBody) > 0 {
		// tx.RespBody is already decompressed by the proxy before storage.
		// Do NOT call Decompress here — the body is plain bytes.
		body := tx.RespBody
		ct := tx.RespHeaders.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			b.Write(PrettyJSON(body))
		} else {
			b.Write(body)
		}
	}
	return b.String()
}

func sortedKeys(h map[string][]string) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
