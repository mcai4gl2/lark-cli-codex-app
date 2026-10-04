package weixin

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	defaultBodyLogMaxLen = 200
	tokenPrefixLen       = 6
	botAgentMaxLen       = 256
)

// TruncateForLog shortens a string for logging, appending its true length when
// it was trimmed.
func TruncateForLog(s string, max int) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if max <= 0 || len(runes) <= max {
		return s
	}
	return fmt.Sprintf("%s…(len=%d)", string(runes[:max]), len(runes))
}

// RedactToken shows only a short prefix of a secret plus its length.
func RedactToken(token string) string {
	if token == "" {
		return "(none)"
	}
	runes := []rune(token)
	if len(runes) <= tokenPrefixLen {
		return fmt.Sprintf("****(len=%d)", len(runes))
	}
	return fmt.Sprintf("%s…(len=%d)", string(runes[:tokenPrefixLen]), len(runes))
}

var sensitiveFieldPattern = regexp.MustCompile(`"(context_token|bot_token|token|aes_key|aeskey|typing_ticket|get_updates_buf|authorization|Authorization)"\s*:\s*"[^"]*"`)

// RedactBody masks known secret-bearing JSON fields and truncates the result.
func RedactBody(body string, max int) string {
	if strings.TrimSpace(body) == "" {
		return "(empty)"
	}
	if max <= 0 {
		max = defaultBodyLogMaxLen
	}
	redacted := sensitiveFieldPattern.ReplaceAllString(body, `"$1":"<redacted>"`)
	return TruncateForLog(redacted, max)
}

// RedactURL strips the query string, which commonly carries signatures and
// encrypted download parameters.
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return TruncateForLog(raw, 80)
	}
	base := parsed.Scheme + "://" + parsed.Host + parsed.Path
	if parsed.RawQuery != "" {
		return base + "?<redacted>"
	}
	return base
}

var (
	botAgentProductPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,32}/[A-Za-z0-9_.+\-]{1,32}$`)
	botAgentCommentPattern = regexp.MustCompile(`^[\x20-\x27\x2A-\x7E]{1,64}$`)
)

// SanitizeBotAgent normalizes a configured bot_agent into a wire-safe UA-style
// string: `Name/Version` products, each optionally followed by `(comment)`.
// Tokens that do not parse are dropped; an empty result falls back to
// DefaultBotAgent.
func SanitizeBotAgent(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultBotAgent
	}

	// Split on whitespace, then re-glue a multi-word "(comment)" onto the token
	// that opened it.
	fields := strings.Fields(trimmed)
	tokens := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		token := fields[i]
		if strings.HasPrefix(token, "(") && !strings.HasSuffix(token, ")") {
			for i+1 < len(fields) && !strings.HasSuffix(token, ")") {
				i++
				token += " " + fields[i]
			}
		}
		tokens = append(tokens, token)
	}

	accepted := make([]string, 0, len(tokens))
	pendingProduct := ""
	for _, token := range tokens {
		if strings.HasPrefix(token, "(") && strings.HasSuffix(token, ")") {
			inner := token[1 : len(token)-1]
			if pendingProduct != "" && botAgentCommentPattern.MatchString(inner) {
				accepted = append(accepted, pendingProduct+" ("+inner+")")
			} else if pendingProduct != "" {
				accepted = append(accepted, pendingProduct)
			}
			pendingProduct = ""
			continue
		}
		if pendingProduct != "" {
			accepted = append(accepted, pendingProduct)
			pendingProduct = ""
		}
		if botAgentProductPattern.MatchString(token) {
			pendingProduct = token
		}
	}
	if pendingProduct != "" {
		accepted = append(accepted, pendingProduct)
	}
	if len(accepted) == 0 {
		return DefaultBotAgent
	}

	joined := strings.Join(accepted, " ")
	if len(joined) <= botAgentMaxLen {
		return joined
	}

	// Drop trailing tokens until the result fits.
	kept := make([]string, 0, len(accepted))
	size := 0
	for _, token := range accepted {
		add := len(token)
		if len(kept) > 0 {
			add++
		}
		if size+add > botAgentMaxLen {
			break
		}
		kept = append(kept, token)
		size += add
	}
	if len(kept) == 0 {
		return DefaultBotAgent
	}
	return strings.Join(kept, " ")
}
