package agent

import (
	"strings"
	"unicode"
)

// ParseBackendDirective recognizes a leading "/<backend>" token where <backend>
// resolves via Resolve (names + aliases). Unrecognized /word is left untouched.
func ParseBackendDirective(text string) (backend string, rest string, ok bool) {
	trimmedLeft := strings.TrimLeftFunc(text, unicode.IsSpace)
	if !strings.HasPrefix(trimmedLeft, "/") {
		return "", text, false
	}
	body := trimmedLeft[1:]
	if body == "" {
		return "", text, false
	}
	tokenEnd := strings.IndexFunc(body, unicode.IsSpace)
	token := body
	remainder := ""
	if tokenEnd >= 0 {
		token = body[:tokenEnd]
		remainder = strings.TrimSpace(body[tokenEnd+1:])
	}
	if token == "" {
		return "", text, false
	}
	b, found := Resolve(token)
	if !found {
		return "", text, false
	}
	return b.Name(), remainder, true
}
