package weixin

import (
	"strings"
	"testing"
)

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"heading", "# Title\nbody", "Title\nbody"},
		{"deep heading", "### Sub\nbody", "Sub\nbody"},
		{"bold", "this is **bold** text", "this is bold text"},
		{"bold underscores", "this is __bold__ text", "this is bold text"},
		{"italic", "this is *italic* text", "this is italic text"},
		{"strikethrough", "this is ~~gone~~ text", "this is gone text"},
		{"inline code", "run `ls -la` now", "run ls -la now"},
		{"link", "see [docs](https://example.test)", "see docs (https://example.test)"},
		{"image", "![alt](https://example.test/a.png)", "alt (https://example.test/a.png)"},
		{"bullet normalization", "* one\n+ two\n- three", "- one\n- two\n- three"},
		{"blockquote", "> quoted", "quoted"},
		{"horizontal rule", "before\n\n---\n\nafter", "before\n\nafter"},
		{
			name:  "fenced code keeps its contents",
			input: "Result:\n```go\nfmt.Println(\"hi\")\n```\ndone",
			want:  "Result:\nfmt.Println(\"hi\")\ndone",
		},
		{
			name:  "markdown inside a fence is left alone",
			input: "```\n**not bold** and `code`\n```",
			want:  "**not bold** and `code`",
		},
		{"empty", "   ", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripMarkdown(tc.input); got != tc.want {
				t.Fatalf("StripMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestStripMarkdownCollapsesBlankRuns(t *testing.T) {
	got := StripMarkdown("a\n\n\n\nb")
	if got != "a\n\nb" {
		t.Fatalf("StripMarkdown() = %q", got)
	}
}

func TestSplitChunksKeepsShortTextIntact(t *testing.T) {
	chunks := SplitChunks("short", MaxChunkRunes)
	if len(chunks) != 1 || chunks[0] != "short" {
		t.Fatalf("SplitChunks() = %v", chunks)
	}
	if got := SplitChunks("  ", 10); got != nil {
		t.Fatalf("SplitChunks(blank) = %v", got)
	}
}

func TestSplitChunksPrefersNewlineBoundaries(t *testing.T) {
	text := strings.Repeat("line one\n", 3) + strings.Repeat("x", 30)
	chunks := SplitChunks(text, 20)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %v", chunks)
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 20 {
			t.Fatalf("chunk %q exceeds the limit", chunk)
		}
	}
	if strings.Join(strings.Fields(strings.Join(chunks, " ")), "") != strings.Join(strings.Fields(text), "") {
		t.Fatalf("chunking lost content: %v", chunks)
	}
}

func TestSplitChunksIsRuneSafe(t *testing.T) {
	// Multi-byte text must never be split mid-rune.
	text := strings.Repeat("中文字符", 50)
	chunks := SplitChunks(text, 17)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	rejoined := strings.Join(chunks, "")
	if rejoined != text {
		t.Fatalf("chunking altered the text (got %d runes, want %d)", len([]rune(rejoined)), len([]rune(text)))
	}
	for _, chunk := range chunks {
		if len([]rune(chunk)) > 17 {
			t.Fatalf("chunk exceeds the limit: %d runes", len([]rune(chunk)))
		}
		if !isValidUTF8(chunk) {
			t.Fatalf("chunk is not valid UTF-8: %q", chunk)
		}
	}
}

func TestSplitChunksFallsBackToHardSplit(t *testing.T) {
	// No newline and no space: the text must still be broken up.
	text := strings.Repeat("a", 45)
	chunks := SplitChunks(text, 20)
	if len(chunks) != 3 {
		t.Fatalf("SplitChunks() produced %d chunks, want 3", len(chunks))
	}
	if strings.Join(chunks, "") != text {
		t.Fatalf("chunking altered the text")
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
