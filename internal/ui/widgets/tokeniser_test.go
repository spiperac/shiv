package widgets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── detectContentType ────────────────────────────────────────────────────────

func TestDetectContentType_JSON(t *testing.T) {
	lines := []string{"POST /foo HTTP/1.1", "Content-Type: application/json", ""}
	assert.Equal(t, "json", detectContentType(lines))
}

func TestDetectContentType_HTML(t *testing.T) {
	lines := []string{"GET /foo HTTP/1.1", "Content-Type: text/html; charset=utf-8", ""}
	assert.Equal(t, "html", detectContentType(lines))
}

func TestDetectContentType_Plain(t *testing.T) {
	lines := []string{"GET /foo HTTP/1.1", "Content-Type: text/plain", ""}
	assert.Equal(t, "text", detectContentType(lines))
}

func TestDetectContentType_Missing(t *testing.T) {
	lines := []string{"GET /foo HTTP/1.1", "Accept: */*", ""}
	assert.Equal(t, "text", detectContentType(lines))
}

func TestDetectContentType_AfterBlankLineIgnored(t *testing.T) {
	lines := []string{"GET /foo HTTP/1.1", "", "Content-Type: application/json"}
	assert.Equal(t, "text", detectContentType(lines))
}

func TestDetectContentType_CaseInsensitive(t *testing.T) {
	lines := []string{"GET /foo HTTP/1.1", "content-type: APPLICATION/JSON", ""}
	assert.Equal(t, "json", detectContentType(lines))
}

// ── tokeniseFirstLine ────────────────────────────────────────────────────────

func TestTokeniseFirstLine_Request(t *testing.T) {
	tokens := tokeniseFirstLine("GET /search?q=foo HTTP/1.1")

	require.Len(t, tokens, 3)
	assert.Equal(t, tvKindMethod, tokens[0].Kind)
	assert.Equal(t, tvKindPath, tokens[1].Kind)
	assert.Equal(t, tvKindVersion, tokens[2].Kind)
}

func TestTokeniseFirstLine_ResponseStatusKinds(t *testing.T) {
	tests := []struct {
		line     string
		wantKind tvTokenKind
	}{
		{"HTTP/1.1 200 OK", tvKindStatus2xx},
		{"HTTP/1.1 201 Created", tvKindStatus2xx},
		{"HTTP/1.1 301 Moved Permanently", tvKindStatus3xx},
		{"HTTP/1.1 404 Not Found", tvKindStatus4xx},
		{"HTTP/1.1 500 Internal Server Error", tvKindStatus5xx},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			tokens := tokeniseFirstLine(tt.line)
			require.GreaterOrEqual(t, len(tokens), 2)
			assert.Equal(t, tt.wantKind, tokens[1].Kind)
		})
	}
}

// ── tokeniseHTTPMeta ─────────────────────────────────────────────────────────

func TestTokeniseHTTPMeta_Header(t *testing.T) {
	tokens := tokeniseHTTPMeta("Content-Type: application/json", false)

	require.Len(t, tokens, 3)
	assert.Equal(t, tvKindHdrName, tokens[0].Kind)
	assert.Equal(t, "Content-Type", tokens[0].Text)
	assert.Equal(t, tvKindHdrColon, tokens[1].Kind)
	assert.Equal(t, tvKindHdrValue, tokens[2].Kind)
}

func TestTokeniseHTTPMeta_EmptyLine(t *testing.T) {
	tokens := tokeniseHTTPMeta("", false)

	require.Len(t, tokens, 1)
	assert.Equal(t, tvKindPlain, tokens[0].Kind)
	assert.Equal(t, "", tokens[0].Text)
}

func TestTokeniseHTTPMeta_NoColon(t *testing.T) {
	tokens := tokeniseHTTPMeta("plaintext", false)

	require.Len(t, tokens, 1)
	assert.Equal(t, tvKindPlain, tokens[0].Kind)
}

// ── tokeniseJSON ─────────────────────────────────────────────────────────────

func TestTokeniseJSON_KeyAndString(t *testing.T) {
	tokens := tokeniseJSON(`"name": "alice"`)

	kinds := map[tvTokenKind]bool{}
	for _, tok := range tokens {
		kinds[tok.Kind] = true
	}
	assert.True(t, kinds[tvKindJSONKey], "expected tvKindJSONKey token")
	assert.True(t, kinds[tvKindJSONStr], "expected tvKindJSONStr token")
}

func TestTokeniseJSON_Number(t *testing.T) {
	tokens := tokeniseJSON(`"count": 42`)

	found := false
	for _, tok := range tokens {
		if tok.Kind == tvKindJSONNum && tok.Text == "42" {
			found = true
		}
	}
	assert.True(t, found, "expected number token '42'")
}

func TestTokeniseJSON_BoolAndNull(t *testing.T) {
	for _, kw := range []string{"true", "false", "null"} {
		t.Run(kw, func(t *testing.T) {
			tokens := tokeniseJSON(`"flag": ` + kw)
			found := false
			for _, tok := range tokens {
				if tok.Kind == tvKindJSONBool && tok.Text == kw {
					found = true
				}
			}
			assert.True(t, found, "expected tvKindJSONBool token %q", kw)
		})
	}
}

func TestTokeniseJSON_Punctuation(t *testing.T) {
	for _, ch := range []string{"{", "}", "[", "]", ","} {
		t.Run(ch, func(t *testing.T) {
			tokens := tokeniseJSON(ch)
			require.Len(t, tokens, 1)
			assert.Equal(t, tvKindJSONPunct, tokens[0].Kind)
		})
	}
}

func TestTokeniseJSON_EscapedQuoteInsideString(t *testing.T) {
	tokens := tokeniseJSON(`"say \"hi\""`)

	strCount := 0
	for _, tok := range tokens {
		if tok.Kind == tvKindJSONStr || tok.Kind == tvKindJSONKey {
			strCount++
		}
	}
	assert.Equal(t, 1, strCount, "escaped quote should produce exactly 1 string token")
}

func TestTokeniseJSON_DoubleBackslashBeforeQuote(t *testing.T) {
	tokens := tokeniseJSON(`"\\"`)

	strCount := 0
	for _, tok := range tokens {
		if tok.Kind == tvKindJSONStr || tok.Kind == tvKindJSONKey {
			strCount++
		}
	}
	assert.Equal(t, 1, strCount, "double backslash before closing quote should produce exactly 1 string token")
}

func TestTokeniseJSON_Empty(t *testing.T) {
	tokens := tokeniseJSON("")

	require.Len(t, tokens, 1)
	assert.Equal(t, tvKindPlain, tokens[0].Kind)
}

// ── wrapTokens ───────────────────────────────────────────────────────────────

func TestWrapTokens_ShortLineNoWrap(t *testing.T) {
	tokens := []tvToken{{Text: "hello", Kind: tvKindPlain}}
	lines := wrapTokens(tokens, "hello", 80)

	require.Len(t, lines, 1)
	assert.Equal(t, "hello", lines[0].Raw)
}

func TestWrapTokens_LongLineWraps(t *testing.T) {
	long := strings.Repeat("x", 100)
	tokens := []tvToken{{Text: long, Kind: tvKindPlain}}
	lines := wrapTokens(tokens, long, 40)

	assert.Greater(t, len(lines), 1, "expected wrapping into multiple lines")

	total := 0
	for _, l := range lines {
		total += len([]rune(l.Raw))
	}
	assert.Equal(t, 100, total, "all characters must be preserved across wrapped lines")
}

func TestWrapTokens_JSONStringNotSplit(t *testing.T) {
	str := `"` + strings.Repeat("a", 50) + `"`
	tokens := []tvToken{{Text: str, Kind: tvKindJSONStr}}
	lines := wrapTokens(tokens, str, 20)

	assert.Len(t, lines, 1, "JSON string must not be split across lines")
}
