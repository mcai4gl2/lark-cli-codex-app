package slackmemory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjwong/lark-cli/internal/platform"
)

func TestBuildPromptContextIncludesChannelThreadMemoryAndSummary(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{TeamID: "T123", ChannelID: "C123", ThreadID: "171.1", MessageID: "171.1"}

	mustWriteMarkdown(t, store.ChannelMemoryPath(event), "# Channel Memory\nUser prefers concise plans.")
	mustWriteMarkdown(t, store.ThreadMemoryPath(event), "# Thread Memory\nThis thread is about Slack setup.")
	mustWriteMarkdown(t, store.ThreadSummaryPath(event), "# Summary\nWe chose two Slack apps.")

	ctx, err := BuildPromptContext(store, event, ContextOptions{MaxSectionChars: 500})
	if err != nil {
		t.Fatalf("BuildPromptContext() error = %v", err)
	}

	want := strings.Join([]string{
		"## Slack channel memory\n# Channel Memory\nUser prefers concise plans.",
		"## Slack thread memory\n# Thread Memory\nThis thread is about Slack setup.",
		"## Slack thread summary\n# Summary\nWe chose two Slack apps.",
	}, "\n\n")
	if ctx != want {
		t.Fatalf("context = %q, want %q", ctx, want)
	}
}

func TestBuildPromptContextReturnsEmptyForMissingFiles(t *testing.T) {
	store := NewStore(Config{Root: t.TempDir()})
	event := platform.MessageEvent{TeamID: "T123", ChannelID: "C123", ThreadID: "171.1", MessageID: "171.1"}

	ctx, err := BuildPromptContext(store, event, ContextOptions{MaxSectionChars: 500})
	if err != nil {
		t.Fatalf("BuildPromptContext() error = %v", err)
	}
	if ctx != "" {
		t.Fatalf("context = %q", ctx)
	}
}

func TestBuildPromptContextReturnsEmptyForNilOrDisabledStore(t *testing.T) {
	event := platform.MessageEvent{TeamID: "T123", ChannelID: "C123", ThreadID: "171.1", MessageID: "171.1"}

	ctx, err := BuildPromptContext(nil, event, ContextOptions{MaxSectionChars: 500})
	if err != nil {
		t.Fatalf("BuildPromptContext(nil) error = %v", err)
	}
	if ctx != "" {
		t.Fatalf("BuildPromptContext(nil) = %q", ctx)
	}

	ctx, err = BuildPromptContext(NewStore(Config{}), event, ContextOptions{MaxSectionChars: 500})
	if err != nil {
		t.Fatalf("BuildPromptContext(disabled) error = %v", err)
	}
	if ctx != "" {
		t.Fatalf("BuildPromptContext(disabled) = %q", ctx)
	}
}

func TestBuildPromptContextUsesDefaultSectionLimit(t *testing.T) {
	store := NewStore(Config{Root: t.TempDir()})
	event := platform.MessageEvent{TeamID: "T123", ChannelID: "C123", ThreadID: "171.1", MessageID: "171.1"}
	mustWriteMarkdown(t, store.ChannelMemoryPath(event), strings.Repeat("a", 2001))

	ctx, err := BuildPromptContext(store, event, ContextOptions{})
	if err != nil {
		t.Fatalf("BuildPromptContext() error = %v", err)
	}

	want := "## Slack channel memory\n" + strings.Repeat("a", 2000)
	if ctx != want {
		t.Fatalf("context length = %d, want %d; context = %q", len([]rune(ctx)), len([]rune(want)), ctx)
	}
}

func TestBuildPromptContextReturnsContextualReadError(t *testing.T) {
	store := NewStore(Config{Root: t.TempDir()})
	event := platform.MessageEvent{TeamID: "T123", ChannelID: "C123", ThreadID: "171.1", MessageID: "171.1"}
	mustWriteMarkdown(t, store.ChannelMemoryPath(event), "ok")
	if err := os.MkdirAll(store.ThreadMemoryPath(event), 0o700); err != nil {
		t.Fatalf("MkdirAll(thread memory path): %v", err)
	}

	_, err := BuildPromptContext(store, event, ContextOptions{MaxSectionChars: 500})
	if err == nil {
		t.Fatal("BuildPromptContext() error = nil")
	}
	if !strings.Contains(err.Error(), "read Slack thread memory") {
		t.Fatalf("error = %v, want contextual section title", err)
	}
}

func mustWriteMarkdown(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
