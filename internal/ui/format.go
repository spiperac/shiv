package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/store"
)

const MaxDisplayBytes = 64 * 1024 // 64 KB

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

// FormatStoreRequest serialises a Transaction into a raw HTTP/1.1 request string
// suitable for display in the UI or sending via Repeater/Intruder.
func FormatStoreRequest(tx store.Transaction) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\n", tx.Method, PathOf(tx.URL))
	fmt.Fprintf(&b, "Host: %s\r\n", tx.Host)

	keys := sortedKeys(tx.ReqHeaders)
	for _, k := range keys {
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

// FormatStoreResponse serialises a Transaction into a raw HTTP/1.1 response string
// suitable for display in the UI.
func FormatStoreResponse(tx store.Transaction) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP/1.1 %d\r\n", tx.StatusCode)

	keys := sortedKeys(tx.RespHeaders)
	for _, k := range keys {
		for _, v := range tx.RespHeaders[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")

	if len(tx.RespBody) > 0 {
		body := internalhttp.Decompress(tx.RespHeaders, tx.RespBody)
		ct := tx.RespHeaders.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			b.Write(PrettyJSON(body))
		} else {
			b.Write(body)
		}
	}
	return b.String()
}

// TruncateBody returns body truncated to MaxDisplayBytes with a notice appended.
func TruncateBody(body []byte) []byte {
	if len(body) <= MaxDisplayBytes {
		return body
	}
	return append(body[:MaxDisplayBytes], []byte("\n... truncated")...)
}

func sortedKeys(h map[string][]string) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
