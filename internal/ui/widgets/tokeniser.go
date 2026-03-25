package widgets

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// tvTokenKind identifies the syntax role of a token for colour mapping.
type tvTokenKind uint8

const (
	tvKindPlain     tvTokenKind = iota
	tvKindMethod                // HTTP request method
	tvKindPath                  // HTTP request path
	tvKindVersion               // HTTP version string
	tvKindStatus2xx             // 2xx response status code
	tvKindStatus3xx             // 3xx response status code
	tvKindStatus4xx             // 4xx response status code
	tvKindStatus5xx             // 5xx response status code
	tvKindHdrName               // header field name
	tvKindHdrColon              // colon separator after header name
	tvKindHdrValue              // header field value
	tvKindJSONKey               // JSON object key
	tvKindJSONStr               // JSON string value
	tvKindJSONNum               // JSON number
	tvKindJSONBool              // JSON true, false, null
	tvKindJSONPunct             // JSON structural punctuation: { } [ ] , :
	tvKindLow                   // dimmed / de-emphasised text
)

// tvColor returns the theme colour for a given token kind.
func tvColor(kind tvTokenKind) color.Color {
	switch kind {
	case tvKindMethod:
		return theme.Color(theme.ColorNameError)
	case tvKindPath:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindVersion, tvKindLow:
		return theme.Color(theme.ColorNamePlaceHolder)
	case tvKindStatus2xx:
		return theme.Color(theme.ColorNameSuccess)
	case tvKindStatus3xx:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindStatus4xx, tvKindStatus5xx:
		return theme.Color(theme.ColorNameError)
	case tvKindHdrName:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindHdrColon, tvKindJSONPunct:
		return theme.Color(theme.ColorNamePlaceHolder)
	case tvKindHdrValue:
		return theme.Color(theme.ColorNameForeground)
	case tvKindJSONKey:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindJSONStr:
		return theme.Color(theme.ColorNameSuccess)
	case tvKindJSONNum:
		return theme.Color(theme.ColorNameWarning)
	case tvKindJSONBool:
		return theme.Color(theme.ColorNameError)
	default:
		return theme.Color(theme.ColorNameForeground)
	}
}

// tvToken is a run of text sharing a single syntax colour.
type tvToken struct {
	Text string
	Kind tvTokenKind
}

// tvLine is one visual line (after word-wrap) composed of tokens.
// Raw holds the plain text for selection and clipboard operations.
type tvLine struct {
	Tokens []tvToken
	Raw    string
}

// parseAndWrap tokenises raw HTTP text and wraps each logical line to fit
// within wrapWidth pixels. Returns the complete slice of visual lines.
func parseAndWrap(s string, wrapWidth float32) []tvLine {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	logicalLines := strings.Split(s, "\n")

	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if charWidth <= 0 {
		charWidth = theme.TextSize() * 0.6
	}
	charsPerLine := max(int(wrapWidth/charWidth), 10)

	bodyContentType := detectContentType(logicalLines)

	var result []tvLine
	inBody := false

	for i, rawLine := range logicalLines {
		if !inBody && strings.TrimSpace(rawLine) == "" && i > 0 {
			inBody = true
			result = append(result, tvLine{Tokens: []tvToken{{Text: "", Kind: tvKindPlain}}, Raw: ""})
			continue
		}

		var tokens []tvToken
		if !inBody {
			tokens = tokeniseHTTPMeta(rawLine, i == 0)
		} else {
			tokens = tokeniseBody(rawLine, bodyContentType)
		}

		result = append(result, wrapTokens(tokens, rawLine, charsPerLine)...)
	}
	return result
}

// detectContentType scans the header section to determine the body content type.
func detectContentType(lines []string) string {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-type:") {
			switch {
			case strings.Contains(lower, "json"):
				return "json"
			case strings.Contains(lower, "html"):
				return "html"
			default:
				return "text"
			}
		}
		if line == "" {
			break
		}
	}
	return "text"
}

// tokeniseHTTPMeta tokenises one line from the HTTP header section.
// isFirstLine indicates the request or response line.
func tokeniseHTTPMeta(line string, isFirstLine bool) []tvToken {
	if line == "" {
		return []tvToken{{Text: "", Kind: tvKindPlain}}
	}
	if isFirstLine {
		return tokeniseFirstLine(line)
	}
	colonIdx := strings.Index(line, ":")
	if colonIdx < 0 {
		return []tvToken{{Text: line, Kind: tvKindPlain}}
	}
	return []tvToken{
		{Text: line[:colonIdx], Kind: tvKindHdrName},
		{Text: ":", Kind: tvKindHdrColon},
		{Text: line[colonIdx+1:], Kind: tvKindHdrValue},
	}
}

// tokeniseFirstLine tokenises the HTTP request or response line.
func tokeniseFirstLine(line string) []tvToken {
	if strings.TrimSpace(line) == "" {
		return []tvToken{{Text: line, Kind: tvKindPlain}}
	}

	parts := strings.Fields(line)
	if len(parts) == 0 {
		return []tvToken{{Text: line, Kind: tvKindPlain}}
	}

	// Response line: HTTP/1.1 200 OK
	if strings.HasPrefix(parts[0], "HTTP/") {
		if len(parts) < 2 {
			return []tvToken{{Text: line, Kind: tvKindPlain}}
		}

		statusKind := tvKindStatus2xx
		switch parts[1][0] {
		case '3':
			statusKind = tvKindStatus3xx
		case '4':
			statusKind = tvKindStatus4xx
		case '5':
			statusKind = tvKindStatus5xx
		}

		tokens := []tvToken{
			{Text: parts[0] + " ", Kind: tvKindVersion},
			{Text: parts[1], Kind: statusKind},
		}

		if len(parts) > 2 {
			tokens = append(tokens, tvToken{
				Text: " " + strings.Join(parts[2:], " "),
				Kind: tvKindLow,
			})
		}

		return tokens
	}

	// Request line: GET /path HTTP/1.1
	if len(parts) >= 3 {
		return []tvToken{
			{Text: parts[0] + " ", Kind: tvKindMethod},
			{Text: parts[1], Kind: tvKindPath},
			{Text: " " + strings.Join(parts[2:], " "), Kind: tvKindVersion},
		}
	}

	return []tvToken{{Text: line, Kind: tvKindPlain}}
}

// tokeniseBody tokenises a body line according to its content type.
func tokeniseBody(line string, contentType string) []tvToken {
	if contentType == "json" {
		return tokeniseJSON(line)
	}
	return []tvToken{{Text: line, Kind: tvKindPlain}}
}

// tokeniseJSON performs a single-pass tokenisation of a JSON line.
func tokeniseJSON(line string) []tvToken {
	if line == "" {
		return []tvToken{{Text: "", Kind: tvKindPlain}}
	}
	var tokens []tvToken
	runes := []rune(line)
	pos := 0
	for pos < len(runes) {
		ch := runes[pos]
		switch {
		case ch == '"':
			end := pos + 1
			for end < len(runes) {
				if runes[end] == '"' {
					// Count preceding backslashes — even count means quote is unescaped.
					numBackslashes := 0
					for i := end - 1; i >= pos && runes[i] == '\\'; i-- {
						numBackslashes++
					}
					if numBackslashes%2 == 0 {
						break
					}
				}
				end++
			}
			if end < len(runes) {
				end++
			}
			str := string(runes[pos:end])
			peek := end
			for peek < len(runes) && runes[peek] == ' ' {
				peek++
			}
			if peek < len(runes) && runes[peek] == ':' {
				tokens = append(tokens, tvToken{Text: str, Kind: tvKindJSONKey})
			} else {
				tokens = append(tokens, tvToken{Text: str, Kind: tvKindJSONStr})
			}
			pos = end
		case ch == '{' || ch == '}' || ch == '[' || ch == ']' || ch == ',' || ch == ':':
			tokens = append(tokens, tvToken{Text: string(ch), Kind: tvKindJSONPunct})
			pos++
		case ch == '-' || (ch >= '0' && ch <= '9'):
			end := pos + 1
			for end < len(runes) && (runes[end] >= '0' && runes[end] <= '9' || runes[end] == '.' || runes[end] == 'e' || runes[end] == 'E' || runes[end] == '+' || runes[end] == '-') {
				end++
			}
			tokens = append(tokens, tvToken{Text: string(runes[pos:end]), Kind: tvKindJSONNum})
			pos = end
		case ch == 't' || ch == 'f' || ch == 'n':
			matched := false
			for _, keyword := range []string{"true", "false", "null"} {
				if strings.HasPrefix(string(runes[pos:]), keyword) {
					tokens = append(tokens, tvToken{Text: keyword, Kind: tvKindJSONBool})
					pos += len([]rune(keyword))
					matched = true
					break
				}
			}
			if !matched {
				tokens = append(tokens, tvToken{Text: string(ch), Kind: tvKindPlain})
				pos++
			}
		case ch == ' ' || ch == '\t':
			end := pos + 1
			for end < len(runes) && (runes[end] == ' ' || runes[end] == '\t') {
				end++
			}
			tokens = append(tokens, tvToken{Text: string(runes[pos:end]), Kind: tvKindPlain})
			pos = end
		default:
			tokens = append(tokens, tvToken{Text: string(ch), Kind: tvKindPlain})
			pos++
		}
	}
	return tokens
}

// wrapTokens splits a logical line into visual lines of at most charsPerLine runes.
func wrapTokens(tokens []tvToken, rawLine string, charsPerLine int) []tvLine {
	if charsPerLine <= 0 {
		charsPerLine = 80
	}

	var result []tvLine
	var current []tvToken
	currentLen := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		var raw strings.Builder
		for _, t := range current {
			raw.WriteString(t.Text)
		}
		result = append(result, tvLine{Tokens: current, Raw: raw.String()})
		current = nil
		currentLen = 0
	}

	for _, token := range tokens {
		tokenLen := len([]rune(token.Text))

		// never split JSON strings
		if token.Kind == tvKindJSONStr || token.Kind == tvKindJSONKey {
			if currentLen+tokenLen > charsPerLine {
				flush()
			}
			current = append(current, token)
			currentLen += tokenLen
			continue
		}

		// split other tokens if needed
		runes := []rune(token.Text)
		for len(runes) > 0 {
			spaceLeft := charsPerLine - currentLen
			if spaceLeft <= 0 {
				flush()
				spaceLeft = charsPerLine
			}

			n := min(len(runes), spaceLeft)

			current = append(current, tvToken{
				Text: string(runes[:n]),
				Kind: token.Kind,
			})
			currentLen += n
			runes = runes[n:]
		}
	}

	flush()
	return result
}
