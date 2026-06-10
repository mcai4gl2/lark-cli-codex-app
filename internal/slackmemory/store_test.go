package slackmemory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

func TestStoreWritesInboundEventToThreadAndDailyLogs(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C123",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000100",
		UserID:      "U123",
		MessageText: "hello",
		ReceivedAt:  "2026-06-10T12:34:56Z",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	threadLog := filepath.Join(root, "T123", "C123", "threads", "1710000000.000100", "events.jsonl")
	dailyLog := filepath.Join(root, "T123", "C123", "daily", "2026-06-10.jsonl")

	for _, path := range []string{threadLog, dailyLog} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != 1 {
			t.Fatalf("%s line count = %d", path, len(lines))
		}
		var record ConversationRecord
		if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
			t.Fatalf("unmarshal record: %v", err)
		}
		if record.Direction != "inbound" || record.Text != "hello" || record.Event.ChannelID != "C123" {
			t.Fatalf("record = %+v", record)
		}
	}
}

func TestStoreWritesOutboundEventToThreadOnly(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadID:  "1710000000.000100",
		MessageID: "1710000000.000100",
	}

	if err := store.RecordOutbound(event, "done"); err != nil {
		t.Fatalf("RecordOutbound() error = %v", err)
	}

	threadLog := filepath.Join(root, "T123", "C123", "threads", "1710000000.000100", "events.jsonl")
	data, err := os.ReadFile(threadLog)
	if err != nil {
		t.Fatalf("ReadFile(threadLog): %v", err)
	}
	var record ConversationRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	if record.Direction != "outbound" || record.Text != "done" {
		t.Fatalf("record = %+v", record)
	}

	dailyLog := filepath.Join(root, "T123", "C123", "daily", time.Now().Format("2006-01-02")+".jsonl")
	if _, err := os.Stat(dailyLog); !os.IsNotExist(err) {
		t.Fatalf("outbound created daily log %s: %v", dailyLog, err)
	}
}

func TestStoreEnabledReflectsRoot(t *testing.T) {
	if NewStore(Config{}).Enabled() {
		t.Fatal("empty root store Enabled() = true")
	}
	if !NewStore(Config{Root: t.TempDir()}).Enabled() {
		t.Fatal("non-empty root store Enabled() = false")
	}
	var store *Store
	if store.Enabled() {
		t.Fatal("nil store Enabled() = true")
	}
}

func TestDisabledStoreRecordMethodsAreNoOps(t *testing.T) {
	t.Chdir(t.TempDir())

	store := NewStore(Config{})
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C123",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000100",
		MessageText: "hello",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() disabled error = %v", err)
	}
	if err := store.RecordOutbound(event, "done"); err != nil {
		t.Fatalf("RecordOutbound() disabled error = %v", err)
	}
	if _, err := os.Stat("T123"); !os.IsNotExist(err) {
		t.Fatalf("disabled store created relative path: %v", err)
	}
}

func TestNilStoreRecordMethodsAreNoOps(t *testing.T) {
	t.Chdir(t.TempDir())

	var store *Store
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C123",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000100",
		MessageText: "hello",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() nil store error = %v", err)
	}
	if err := store.RecordOutbound(event, "done"); err != nil {
		t.Fatalf("RecordOutbound() nil store error = %v", err)
	}
	if _, err := os.Stat("T123"); !os.IsNotExist(err) {
		t.Fatalf("nil store created relative path: %v", err)
	}
}

func TestStoreSanitizesMissingAndUnsafePathSegments(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "",
		ChannelID: "../C123",
		ThreadID:  "",
		MessageID: "1710000000.000100",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	expected := filepath.Join(root, "no-team", "_C123", "threads", "1710000000.000100", "events.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected sanitized path %s: %v", expected, err)
	}
}

func TestStoreUsesUnknownForEmptyNonTeamSegments(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: "",
		ThreadID:  "",
		MessageID: "1710000000.000100",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	expected := filepath.Join(root, "T123", "unknown", "threads", "1710000000.000100", "events.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected unknown channel path %s: %v", expected, err)
	}
}

func TestStoreSanitizesBackslashAndColonPathSegments(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T:123",
		ChannelID: `C\123`,
		ThreadID:  `thread:1\2`,
		MessageID: "1710000000.000100",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	expected := filepath.Join(root, "T_123", "C_123", "threads", "thread_1_2", "events.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected sanitized backslash/colon path %s: %v", expected, err)
	}
}

func TestStoreSanitizesDotPathSegments(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: ".",
		ThreadID:  ".",
		MessageID: "1710000000.000100",
	}

	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	expected := filepath.Join(root, "T123", "_", "threads", "_", "events.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected sanitized dot path %s: %v", expected, err)
	}
}

func TestStoreUsesTodayForDailyLogWhenReceivedAtIsUnparseable(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C123",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000100",
		MessageText: "hello",
		ReceivedAt:  "not-a-time",
	}

	before := time.Now().Format("2006-01-02")
	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}
	after := time.Now().Format("2006-01-02")

	for _, date := range []string{before, after} {
		dailyLog := filepath.Join(root, "T123", "C123", "daily", date+".jsonl")
		if _, err := os.Stat(dailyLog); err == nil {
			return
		}
	}
	t.Fatalf("expected fallback daily log for either %s or %s", before, after)
}

func TestStoreHelperPathsAndMarkdownHelpers(t *testing.T) {
	root := t.TempDir()
	store := NewStore(Config{Root: root})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadID:  "",
		MessageID: "1710000000.000100",
	}

	channelDir := filepath.Join(root, "T123", "C123")
	threadDir := filepath.Join(channelDir, "threads", "1710000000.000100")
	if got := store.ChannelDir(event); got != channelDir {
		t.Fatalf("ChannelDir() = %q, want %q", got, channelDir)
	}
	if got := store.ThreadDir(event); got != threadDir {
		t.Fatalf("ThreadDir() = %q, want %q", got, threadDir)
	}
	if got := store.ThreadSummaryPath(event); got != filepath.Join(threadDir, "summary.md") {
		t.Fatalf("ThreadSummaryPath() = %q", got)
	}
	if got := store.ThreadMemoryPath(event); got != filepath.Join(threadDir, "memory.md") {
		t.Fatalf("ThreadMemoryPath() = %q", got)
	}
	if got := store.ChannelMemoryPath(event); got != filepath.Join(channelDir, "memory.md") {
		t.Fatalf("ChannelMemoryPath() = %q", got)
	}

	memoryPath := store.ThreadMemoryPath(event)
	if got, err := store.ReadMarkdown(memoryPath, 100); err != nil || got != "" {
		t.Fatalf("ReadMarkdown() missing = %q, %v", got, err)
	}
	if err := store.AppendMarkdown(memoryPath, " hello 世界 "); err != nil {
		t.Fatalf("AppendMarkdown() error = %v", err)
	}
	if err := store.AppendMarkdown(memoryPath, "\nagain"); err != nil {
		t.Fatalf("AppendMarkdown() second error = %v", err)
	}
	if err := store.AppendMarkdown(memoryPath, " \n\t "); err == nil {
		t.Fatal("AppendMarkdown() empty error = nil")
	}
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("ReadFile(memoryPath): %v", err)
	}
	if string(data) != "hello 世界\nagain\n" {
		t.Fatalf("markdown file = %q", string(data))
	}
	got, err := store.ReadMarkdown(memoryPath, 8)
	if err != nil {
		t.Fatalf("ReadMarkdown() error = %v", err)
	}
	if got != "hello 世界" {
		t.Fatalf("ReadMarkdown() truncated = %q", got)
	}
}

func TestNilStoreHelperPathsDoNotPanic(t *testing.T) {
	var store *Store
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadID:  "1710000000.000100",
		MessageID: "1710000000.000100",
	}

	if got := store.Root(); got != "" {
		t.Fatalf("Root() = %q", got)
	}
	if got := store.ChannelDir(event); got != filepath.Join("T123", "C123") {
		t.Fatalf("ChannelDir() = %q", got)
	}
	if got := store.ThreadDir(event); got != filepath.Join("T123", "C123", "threads", "1710000000.000100") {
		t.Fatalf("ThreadDir() = %q", got)
	}
	if got := store.ThreadSummaryPath(event); got != filepath.Join("T123", "C123", "threads", "1710000000.000100", "summary.md") {
		t.Fatalf("ThreadSummaryPath() = %q", got)
	}
	if got := store.ThreadMemoryPath(event); got != filepath.Join("T123", "C123", "threads", "1710000000.000100", "memory.md") {
		t.Fatalf("ThreadMemoryPath() = %q", got)
	}
	if got := store.ChannelMemoryPath(event); got != filepath.Join("T123", "C123", "memory.md") {
		t.Fatalf("ChannelMemoryPath() = %q", got)
	}
}
