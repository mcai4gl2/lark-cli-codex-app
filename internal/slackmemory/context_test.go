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

func TestBuildPromptContextIncludesRecentThreadTranscript(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C123",
		ThreadID:    "171.1",
		MessageID:   "171.3",
		UserID:      "U123",
		MessageText: "current message",
		ReceivedAt:  "2026-06-14T10:03:00Z",
	}
	previous := event
	previous.MessageID = "171.1"
	previous.MessageText = "previous request"
	previous.ReceivedAt = "2026-06-14T10:01:00Z"
	current := event

	if err := store.RecordInbound(previous); err != nil {
		t.Fatalf("RecordInbound(previous) error = %v", err)
	}
	if err := store.RecordOutbound(previous, "previous reply"); err != nil {
		t.Fatalf("RecordOutbound() error = %v", err)
	}
	if err := store.RecordInbound(current); err != nil {
		t.Fatalf("RecordInbound(current) error = %v", err)
	}

	ctx, err := BuildPromptContext(store, event, ContextOptions{
		MaxSectionChars:         500,
		IncludeThreadTranscript: true,
		MaxTranscriptChars:      1000,
		MaxTranscriptRecords:    10,
	})
	if err != nil {
		t.Fatalf("BuildPromptContext() error = %v", err)
	}

	if !strings.Contains(ctx, "## Slack recent thread transcript") {
		t.Fatalf("context missing transcript section: %q", ctx)
	}
	if !strings.Contains(ctx, "User: previous request") || !strings.Contains(ctx, "Codex: previous reply") {
		t.Fatalf("context missing prior conversation: %q", ctx)
	}
	if strings.Contains(ctx, "current message") {
		t.Fatalf("context included current message: %q", ctx)
	}
}

func TestBuildPromptContextRecentTranscriptRespectsLimits(t *testing.T) {
	store := NewStore(Config{Root: t.TempDir()})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadID:  "171.1",
		MessageID: "171.4",
		UserID:    "U123",
	}
	for i, text := range []string{"oldest message", "middle message", "newest message"} {
		recordEvent := event
		recordEvent.MessageID = "171." + string(rune('1'+i))
		recordEvent.MessageText = text
		if err := store.RecordInbound(recordEvent); err != nil {
			t.Fatalf("RecordInbound(%s) error = %v", text, err)
		}
	}

	ctx, err := BuildPromptContext(store, event, ContextOptions{
		IncludeThreadTranscript: true,
		MaxTranscriptChars:      1000,
		MaxTranscriptRecords:    2,
	})
	if err != nil {
		t.Fatalf("BuildPromptContext() error = %v", err)
	}

	if !strings.Contains(ctx, "Older thread messages omitted") {
		t.Fatalf("context missing truncation note: %q", ctx)
	}
	if strings.Contains(ctx, "oldest message") {
		t.Fatalf("context included oldest record: %q", ctx)
	}
	if !strings.Contains(ctx, "middle message") || !strings.Contains(ctx, "newest message") {
		t.Fatalf("context missing newest records: %q", ctx)
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
