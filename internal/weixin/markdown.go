package weixin

import (
	"regexp"
	"strings"
)

// WeChat renders plain text only, and agent output is markdown-heavy, so
// replies are flattened before they are sent. This is a reduced version of the
// reference plugin's streaming filter: it removes markup that would otherwise
// show up as literal punctuation, and keeps the text itself intact.

var (
	markdownImagePattern   = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	markdownLinkPattern    = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	markdownHeadingPattern = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	// RE2 has no backreferences, so each rule character gets its own branch.
	markdownRulePattern       = regexp.MustCompile(`^\s{0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	markdownBlockquotePattern = regexp.MustCompile(`^\s{0,3}>\s?`)
	markdownBulletPattern     = regexp.MustCompile(`^(\s*)[*+]\s+`)
	markdownBoldPattern       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	markdownBoldAltPattern    = regexp.MustCompile(`__([^_]+)__`)
	markdownItalicPattern     = regexp.MustCompile(`(^|[^*\w])\*([^*\n]+)\*`)
	markdownStrikePattern     = regexp.MustCompile(`~~([^~]+)~~`)
	markdownInlineCodePattern = regexp.MustCompile("`+([^`\n]+)`+")
	markdownFencePattern      = regexp.MustCompile("^\\s{0,3}```|^\\s{0,3}~~~")
)

// StripMarkdown converts markdown into plain text suitable for WeChat.
//
// Fenced code blocks keep their contents (fence lines are dropped) so command
// output and snippets survive; inline emphasis, headings, links, and rules lose
// their markup.
func StripMarkdown(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inFence := false

	for _, line := range lines {
		if markdownFencePattern.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			// Inside a fence the text is literal: emit it untouched.
			out = append(out, line)
			continue
		}
		out = append(out, stripInlineMarkdown(line))
	}

	return collapseBlankRuns(out)
}

func stripInlineMarkdown(line string) string {
	if markdownRulePattern.MatchString(line) {
		return ""
	}
	line = markdownHeadingPattern.ReplaceAllString(line, "")
	line = markdownBlockquotePattern.ReplaceAllString(line, "")
	line = markdownBulletPattern.ReplaceAllString(line, "$1- ")
	line = markdownImagePattern.ReplaceAllString(line, "$1 ($2)")
	line = markdownLinkPattern.ReplaceAllString(line, "$1 ($2)")
	line = markdownInlineCodePattern.ReplaceAllString(line, "$1")
	line = markdownBoldPattern.ReplaceAllString(line, "$1")
	line = markdownBoldAltPattern.ReplaceAllString(line, "$1")
	line = markdownStrikePattern.ReplaceAllString(line, "$1")
	line = markdownItalicPattern.ReplaceAllString(line, "$1$2")
	return strings.TrimRight(line, " \t")
}

// collapseBlankRuns joins lines, squashing runs of blank lines left behind by
// removed markup down to a single separator.
func collapseBlankRuns(lines []string) string {
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blank = true
			continue
		}
		if blank && len(out) > 0 {
			out = append(out, "")
		}
		blank = false
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// MaxChunkRunes is the per-message text limit accepted by sendmessage.
const MaxChunkRunes = 4000

// SplitChunks breaks text into rune-safe pieces of at most maxRunes, preferring
// to break at a newline and then at a space so words and lines stay intact.
func SplitChunks(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = MaxChunkRunes
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	runes := []rune(text)
	if len(runes) <= maxRunes {
		return []string{text}
	}

	chunks := make([]string, 0, len(runes)/maxRunes+1)
	for len(runes) > 0 {
		if len(runes) <= maxRunes {
			if chunk := strings.TrimSpace(string(runes)); chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}

		window := runes[:maxRunes]
		split := lastIndexRune(window, '\n')
		if split <= 0 {
			split = lastIndexRune(window, ' ')
		}
		if split <= 0 {
			split = maxRunes
		}

		if chunk := strings.TrimSpace(string(runes[:split])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[split:]
	}
	return chunks
}

func lastIndexRune(runes []rune, target rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == target {
			return i
		}
	}
	return -1
}
