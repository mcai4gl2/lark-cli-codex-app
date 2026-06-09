# Slack Memory Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Slack memory/audit support that stores channel/thread folders, records inbound and outbound conversation events, persists explicit memory files, and injects compact thread/channel memory into Codex prompts.

**Architecture:** Add a focused `internal/slackmemory` package that owns filesystem layout, event persistence, summaries, and prompt context loading. Wire it into Slack gateway handling before/after agent dispatch, and extend the agent runner with an optional prompt context provider. Keep the first implementation deterministic and explicit: raw JSONL audit always works, summaries/memory are Markdown files managed by Codex or future commands, and automatic LLM summarization is deferred.

**Tech Stack:** Go 1.24, existing Cobra CLI, existing `platform.MessageEvent`, JSONL files, Markdown summaries, Docker-based `gofmt`, `go test`, and `go build`.

---

## Scope

This plan implements the first useful version of memory support:

- Store normalized Slack conversation data under `.slack/conversations/<team>/<channel>/...`.
- Store per-thread inbound events in `threads/<thread_ts>/events.jsonl`.
- Store per-channel daily inbound events in `daily/YYYY-MM-DD.jsonl`.
- Store outbound assistant replies in the same thread `events.jsonl`.
- Support `summary.md` and `memory.md` files per thread as manually/agent-maintained memory artifacts.
- Support channel-level `memory.md` for generic durable memory.
- Inject relevant `summary.md` and `memory.md` content into the Codex prompt.
- Add CLI commands for inspecting memory paths and writing memory notes.

This plan does not implement automatic LLM summarization. The first version gives Codex stable files to update when the user asks, and gives future work a clean storage API.

## File Structure

- Create `internal/slackmemory/store.go`
  - Owns root path, path sanitization, directory creation, JSONL appends, and Markdown read/write helpers.
- Create `internal/slackmemory/store_test.go`
  - Tests folder layout, JSONL writes, path sanitization, and Markdown memory reads.
- Create `internal/slackmemory/context.go`
  - Builds compact prompt context from channel memory, thread memory, and thread summary.
- Create `internal/slackmemory/context_test.go`
  - Tests prompt context loading and truncation.
- Modify `internal/slack/gateway.go`
  - Adds optional memory store config and records inbound Slack events before routing.
- Modify `internal/slack/gateway_test.go`
  - Tests that Slack events are written to the memory folder.
- Modify `internal/agent/codex.go`
  - Adds optional prompt context provider and records outbound replies through an optional observer.
- Modify `internal/agent/codex_test.go`
  - Tests prompt context injection and outbound observer behavior.
- Modify `internal/cmd/slack.go`
  - Adds `slack memory path`, `slack memory show`, and `slack memory append` commands.
- Modify `internal/cmd/slack_test.go`
  - Tests command registration.
- Modify `internal/config/config.go`
  - Adds Slack memory config/env accessors.
- Modify `internal/config/slack_config_test.go`
  - Tests default and environment-provided memory root.
- Modify `config.example.yaml`
  - Documents `slack.memory.root`, `enabled`, and prompt limits.
- Modify `USER_GUIDE.md`
  - Updates the Future Memory Extension section after implementation.

## Storage Layout

The default root is beside the Slack event log:

```text
<repo-or-config-root>/.slack/conversations/
  <team_id_or_no-team>/
    <channel_id>/
      channel.json
      memory.md
      daily/
        YYYY-MM-DD.jsonl
      threads/
        <thread_ts>/
          events.jsonl
          summary.md
          memory.md
```

Each `events.jsonl` line uses this envelope:

```go
type ConversationRecord struct {
    Direction  string                `json:"direction"`
    RecordedAt string                `json:"recorded_at"`
    Event      platform.MessageEvent `json:"event"`
    Text       string                `json:"text,omitempty"`
}
```

`Direction` is `inbound` or `outbound`.

## Task 1: Add Slack Memory Store Filesystem Layout

**Files:**
- Create: `internal/slackmemory/store.go`
- Create: `internal/slackmemory/store_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/slackmemory/store_test.go`:

```go
package slackmemory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slackmemory
```

Expected: FAIL because package `internal/slackmemory` does not exist.

- [ ] **Step 3: Implement the memory store**

Create `internal/slackmemory/store.go`:

```go
package slackmemory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

type Config struct {
	Root string
}

type Store struct {
	root string
	mu   sync.Mutex
}

type ConversationRecord struct {
	Direction  string                `json:"direction"`
	RecordedAt string                `json:"recorded_at"`
	Event      platform.MessageEvent `json:"event"`
	Text       string                `json:"text,omitempty"`
}

func NewStore(cfg Config) *Store {
	return &Store{root: strings.TrimSpace(cfg.Root)}
}

func (s *Store) Enabled() bool {
	return s != nil && s.root != ""
}

func (s *Store) RecordInbound(event platform.MessageEvent) error {
	if !s.Enabled() {
		return nil
	}
	record := ConversationRecord{
		Direction:  "inbound",
		RecordedAt: time.Now().Format(time.RFC3339Nano),
		Event:      event,
		Text:       event.MessageText,
	}
	if err := s.appendThreadRecord(event, record); err != nil {
		return err
	}
	return s.appendDailyRecord(event, record)
}

func (s *Store) RecordOutbound(event platform.MessageEvent, text string) error {
	if !s.Enabled() {
		return nil
	}
	record := ConversationRecord{
		Direction:  "outbound",
		RecordedAt: time.Now().Format(time.RFC3339Nano),
		Event:      event,
		Text:       text,
	}
	return s.appendThreadRecord(event, record)
}

func (s *Store) ThreadDir(event platform.MessageEvent) string {
	return filepath.Join(s.ChannelDir(event), "threads", segment(threadID(event)))
}

func (s *Store) ChannelDir(event platform.MessageEvent) string {
	return filepath.Join(s.root, segment(teamID(event)), segment(event.ChannelID))
}

func (s *Store) ThreadSummaryPath(event platform.MessageEvent) string {
	return filepath.Join(s.ThreadDir(event), "summary.md")
}

func (s *Store) ThreadMemoryPath(event platform.MessageEvent) string {
	return filepath.Join(s.ThreadDir(event), "memory.md")
}

func (s *Store) ChannelMemoryPath(event platform.MessageEvent) string {
	return filepath.Join(s.ChannelDir(event), "memory.md")
}

func (s *Store) ReadMarkdown(path string, maxChars int) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(data))
	if maxChars <= 0 || len([]rune(text)) <= maxChars {
		return text, nil
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxChars])) + "\n\n[truncated]", nil
}

func (s *Store) AppendMarkdown(path, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("memory text is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(text + "\n"); err != nil {
		return err
	}
	return nil
}

func (s *Store) appendThreadRecord(event platform.MessageEvent, record ConversationRecord) error {
	return s.appendJSONL(filepath.Join(s.ThreadDir(event), "events.jsonl"), record)
}

func (s *Store) appendDailyRecord(event platform.MessageEvent, record ConversationRecord) error {
	day := eventDay(event)
	return s.appendJSONL(filepath.Join(s.ChannelDir(event), "daily", day+".jsonl"), record)
}

func (s *Store) appendJSONL(path string, record ConversationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create memory directory: %w", err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal memory record: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open memory log: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write memory log: %w", err)
	}
	return nil
}

func eventDay(event platform.MessageEvent) string {
	t, err := time.Parse(time.RFC3339Nano, event.ReceivedAt)
	if err != nil {
		return time.Now().Format("2006-01-02")
	}
	return t.Format("2006-01-02")
}

func teamID(event platform.MessageEvent) string {
	if strings.TrimSpace(event.TeamID) == "" {
		return "no-team"
	}
	return event.TeamID
}

func threadID(event platform.MessageEvent) string {
	if strings.TrimSpace(event.ThreadID) != "" {
		return event.ThreadID
	}
	return event.MessageID
}

func segment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	return replacer.Replace(value)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slackmemory
```

Expected: PASS.

- [ ] **Step 5: Format**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slackmemory/store.go internal/slackmemory/store_test.go
```

- [ ] **Step 6: Commit**

```bash
git add internal/slackmemory/store.go internal/slackmemory/store_test.go
git commit -m "feat: add slack memory store"
```

## Task 2: Build Prompt Memory Context Loader

**Files:**
- Create: `internal/slackmemory/context.go`
- Create: `internal/slackmemory/context_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/slackmemory/context_test.go`:

```go
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

	mustWrite(t, store.ChannelMemoryPath(event), "# Channel Memory\nUser prefers concise plans.")
	mustWrite(t, store.ThreadMemoryPath(event), "# Thread Memory\nThis thread is about Slack setup.")
	mustWrite(t, store.ThreadSummaryPath(event), "# Summary\nWe chose two Slack apps.")

	ctx, err := BuildPromptContext(store, event, ContextOptions{MaxSectionChars: 500})
	if err != nil {
		t.Fatalf("BuildPromptContext() error = %v", err)
	}

	for _, want := range []string{"Channel Memory", "Thread Memory", "Summary", "two Slack apps"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q: %s", want, ctx)
		}
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

func mustWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slackmemory
```

Expected: FAIL because `BuildPromptContext` and `ContextOptions` are undefined.

- [ ] **Step 3: Implement context loader**

Create `internal/slackmemory/context.go`:

```go
package slackmemory

import (
	"fmt"
	"strings"

	"github.com/yjwong/lark-cli/internal/platform"
)

type ContextOptions struct {
	MaxSectionChars int
}

func BuildPromptContext(store *Store, event platform.MessageEvent, opts ContextOptions) (string, error) {
	if store == nil || !store.Enabled() {
		return "", nil
	}
	max := opts.MaxSectionChars
	if max <= 0 {
		max = 2000
	}

	sections := []struct {
		title string
		path  string
	}{
		{title: "Slack channel memory", path: store.ChannelMemoryPath(event)},
		{title: "Slack thread memory", path: store.ThreadMemoryPath(event)},
		{title: "Slack thread summary", path: store.ThreadSummaryPath(event)},
	}

	var parts []string
	for _, section := range sections {
		text, err := store.ReadMarkdown(section.path, max)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", section.title, err)
		}
		if strings.TrimSpace(text) != "" {
			parts = append(parts, "## "+section.title+"\n"+text)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slackmemory
```

Expected: PASS.

- [ ] **Step 5: Format**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slackmemory/context.go internal/slackmemory/context_test.go
```

- [ ] **Step 6: Commit**

```bash
git add internal/slackmemory/context.go internal/slackmemory/context_test.go
git commit -m "feat: load slack memory prompt context"
```

## Task 3: Add Agent Prompt Context And Outbound Observer Hooks

**Files:**
- Modify: `internal/agent/codex.go`
- Modify: `internal/agent/codex_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/agent/codex_test.go`:

```go
type fakeContextProvider struct {
	context string
}

func (f fakeContextProvider) PromptContext(_ inbound.LoggedEvent) (string, error) {
	return f.context, nil
}

type fakeReplyObserver struct {
	event platform.MessageEvent
	text  string
}

func (f *fakeReplyObserver) ObserveReply(event platform.MessageEvent, text string) error {
	f.event = event
	f.text = text
	return nil
}

func TestBuildPromptIncludesMemoryContext(t *testing.T) {
	prompt := buildPromptWithContext(inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		UserID:      "U123",
		MessageID:   "1712345678.000100",
		MessageText: "continue this thread",
	}, 1200, "## Slack thread summary\nWe agreed to use two apps.")

	if !strings.Contains(prompt, "We agreed to use two apps") {
		t.Fatalf("prompt did not include memory context: %q", prompt)
	}
	if !strings.Contains(prompt, "continue this thread") {
		t.Fatalf("prompt did not include message: %q", prompt)
	}
}

func TestRunnerReplyNotifiesObserver(t *testing.T) {
	messenger := &fakeMessenger{}
	observer := &fakeReplyObserver{}
	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		ResultMaxChars: 100,
		ReplyObserver:  observer,
	}, nil, messenger)
	entry := inbound.LoggedEvent{
		Provider:  "slack",
		ChannelID: "C123",
		ThreadID:  "1712345678.000100",
		MessageID: "1712345678.000100",
		UserID:    "U123",
	}

	if err := runner.reply(entry, "done"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if observer.event.ChannelID != "C123" || observer.text != "done" {
		t.Fatalf("observer = %+v text=%q", observer.event, observer.text)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent
```

Expected: FAIL because `buildPromptWithContext`, `PromptContext`, `ReplyObserver`, and `ObserveReply` do not exist.

- [ ] **Step 3: Implement agent hooks**

Modify `internal/agent/codex.go`:

```go
type PromptContextProvider interface {
	PromptContext(entry inbound.LoggedEvent) (string, error)
}

type ReplyObserver interface {
	ObserveReply(event platform.MessageEvent, text string) error
}

type Config struct {
	Enabled        bool
	CodexBinary    string
	Workspace      string
	Model          string
	AckText        string
	ResultMaxChars int
	Timeout        time.Duration
	ContextProvider PromptContextProvider
	ReplyObserver   ReplyObserver
}
```

Update `execute`:

```go
promptContext := ""
if r.cfg.ContextProvider != nil {
	contextText, err := r.cfg.ContextProvider.PromptContext(entry)
	if err != nil {
		r.logger.Printf("failed to load prompt context for message_id=%s: %v", entry.MessageID, err)
	} else {
		promptContext = contextText
	}
}
prompt := buildPromptWithContext(entry, r.cfg.ResultMaxChars, promptContext)
```

Replace existing `buildPrompt` with:

```go
func buildPrompt(entry inbound.LoggedEvent, resultMaxChars int) string {
	return buildPromptWithContext(entry, resultMaxChars, "")
}

func buildPromptWithContext(entry inbound.LoggedEvent, resultMaxChars int, memoryContext string) string {
	providerLabel := providerLabel(entry.Provider)
	memoryBlock := ""
	if strings.TrimSpace(memoryContext) != "" {
		memoryBlock = "\n\n可用历史记忆和摘要：\n" + strings.TrimSpace(memoryContext)
	}
	return strings.TrimSpace(fmt.Sprintf(`
你是一个本地 Codex 执行代理，这次任务来自%s聊天消息。

要求：
- 直接处理用户请求，尽量少讲方案、多做事。
- 默认使用中文回复，输出要适合直接发回%s。
- 如果任务能执行，就执行后汇报结果。
- 如果缺少关键信息或存在明显风险，只说最关键的阻塞点。
- 最终回复尽量控制在 %d 个字符以内。

上下文：
- provider: %s
- channel_id: %s
- user_id: %s
- message_id: %s
- thread_id: %s%s

用户消息：
%s
`, providerLabel, providerLabel, resultMaxChars, entry.Provider, entry.ChannelID, entry.UserID, entry.MessageID, entry.ThreadID, memoryBlock, entry.MessageText))
}
```

Update `reply`:

```go
func (r *Runner) reply(entry inbound.LoggedEvent, text string) error {
	trimmed := trimForChat(text, r.cfg.ResultMaxChars)
	if err := r.messenger.Reply(context.Background(), entry, trimmed); err != nil {
		return err
	}
	if r.cfg.ReplyObserver != nil {
		if err := r.cfg.ReplyObserver.ObserveReply(entry, trimmed); err != nil {
			r.logger.Printf("reply observer failed for message_id=%s: %v", entry.MessageID, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent
```

Expected: PASS.

- [ ] **Step 5: Format**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/codex.go internal/agent/codex_test.go
```

- [ ] **Step 6: Commit**

```bash
git add internal/agent/codex.go internal/agent/codex_test.go
git commit -m "feat: add agent memory hooks"
```

## Task 4: Wire Memory Store Into Slack Gateway

**Files:**
- Modify: `internal/slack/gateway.go`
- Modify: `internal/slack/gateway_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/slack/gateway_test.go`:

```go
func TestServiceHandleEventWritesSlackMemory(t *testing.T) {
	memoryRoot := t.TempDir()
	service := NewGateway(Config{
		EventLogPath: t.TempDir() + "/events.jsonl",
		BotUserID:    "U999",
		Messenger:    &captureMessenger{},
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
		MemoryRoot:       memoryRoot,
		MemoryEnabled:    true,
	})

	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev123",
		"event":{
			"type":"message",
			"channel_type":"im",
			"user":"U234",
			"channel":"D345",
			"text":"remember this",
			"ts":"1710000000.000200"
		}
	}`)
	if err := service.handleEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}

	path := filepath.Join(memoryRoot, "T123", "D345", "threads", "1710000000.000200", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	if !strings.Contains(string(data), "remember this") {
		t.Fatalf("memory log = %s", string(data))
	}
}
```

Also add imports to `internal/slack/gateway_test.go`:

```go
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack
```

Expected: FAIL because `Config.MemoryRoot` and `Config.MemoryEnabled` do not exist.

- [ ] **Step 3: Implement Slack gateway memory wiring**

Modify `internal/slack/gateway.go`:

```go
import "github.com/yjwong/lark-cli/internal/slackmemory"
```

Add config fields:

```go
MemoryEnabled bool
MemoryRoot    string
MemoryMaxSectionChars int
```

Add gateway field:

```go
memory *slackmemory.Store
```

In `NewGateway`, before creating `agent.NewRunnerWithMessenger`:

```go
memoryStore := (*slackmemory.Store)(nil)
if cfg.MemoryEnabled && strings.TrimSpace(cfg.MemoryRoot) != "" {
	memoryStore = slackmemory.NewStore(slackmemory.Config{Root: cfg.MemoryRoot})
	cfg.Agent.ContextProvider = memoryPromptProvider{
		store:           memoryStore,
		maxSectionChars: cfg.MemoryMaxSectionChars,
	}
	cfg.Agent.ReplyObserver = memoryReplyObserver{store: memoryStore}
}
```

Add to returned `Gateway`:

```go
memory: memoryStore,
```

Add helper types in `internal/slack/gateway.go`:

```go
type memoryPromptProvider struct {
	store           *slackmemory.Store
	maxSectionChars int
}

func (p memoryPromptProvider) PromptContext(entry inbound.LoggedEvent) (string, error) {
	return slackmemory.BuildPromptContext(p.store, entry, slackmemory.ContextOptions{
		MaxSectionChars: p.maxSectionChars,
	})
}

type memoryReplyObserver struct {
	store *slackmemory.Store
}

func (o memoryReplyObserver) ObserveReply(event platform.MessageEvent, text string) error {
	if o.store == nil {
		return nil
	}
	return o.store.RecordOutbound(event, text)
}
```

In `handleEvent`, after `g.handler.Process(entry)` succeeds and before desktop/agent routing:

```go
if g.memory != nil {
	if err := g.memory.RecordInbound(entry); err != nil {
		return err
	}
}
```

- [ ] **Step 4: Run tests to verify pass**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack ./internal/slackmemory ./internal/agent
```

Expected: PASS.

- [ ] **Step 5: Format**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slack/gateway.go internal/slack/gateway_test.go
```

- [ ] **Step 6: Commit**

```bash
git add internal/slack/gateway.go internal/slack/gateway_test.go
git commit -m "feat: wire slack memory into gateway"
```

## Task 5: Add Config And CLI Flags For Memory

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/slack_config_test.go`
- Modify: `internal/cmd/slack.go`

- [ ] **Step 1: Write failing config test**

Add to `internal/config/slack_config_test.go`:

```go
func TestSlackMemoryConfigFromEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LARK_CONFIG_DIR", filepath.Join(tmp, ".lark"))
	t.Setenv("SLACK_MEMORY_ENABLED", "true")
	t.Setenv("SLACK_MEMORY_ROOT", "custom-memory")
	t.Setenv("SLACK_MEMORY_MAX_SECTION_CHARS", "1234")
	resetConfigForTest()
	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if !GetSlackMemoryEnabled() {
		t.Fatalf("GetSlackMemoryEnabled() = false")
	}
	if got := GetSlackMemoryRoot(); got != filepath.Join(tmp, "custom-memory") {
		t.Fatalf("GetSlackMemoryRoot() = %q", got)
	}
	if got := GetSlackMemoryMaxSectionChars(); got != 1234 {
		t.Fatalf("GetSlackMemoryMaxSectionChars() = %d", got)
	}
}
```

- [ ] **Step 2: Run config tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/config
```

Expected: FAIL because Slack memory accessors are undefined.

- [ ] **Step 3: Implement config**

Modify `Config.Slack` in `internal/config/config.go`:

```go
Memory struct {
	Enabled bool `mapstructure:"enabled"`
	Root string `mapstructure:"root"`
	MaxSectionChars int `mapstructure:"max_section_chars"`
} `mapstructure:"memory"`
```

Add defaults in `Init`:

```go
viper.SetDefault("slack.memory.enabled", false)
viper.SetDefault("slack.memory.root", filepath.Join(rootDir, ".slack", "conversations"))
viper.SetDefault("slack.memory.max_section_chars", 2000)
```

Add env bindings:

```go
viper.BindEnv("slack.memory.enabled", "SLACK_MEMORY_ENABLED")
viper.BindEnv("slack.memory.root", "SLACK_MEMORY_ROOT")
viper.BindEnv("slack.memory.max_section_chars", "SLACK_MEMORY_MAX_SECTION_CHARS")
```

Add accessors near other Slack getters:

```go
func GetSlackMemoryEnabled() bool {
	return viper.GetBool("slack.memory.enabled")
}

func GetSlackMemoryRoot() string {
	path := strings.TrimSpace(viper.GetString("slack.memory.root"))
	if path == "" {
		return filepath.Join(rootDir, ".slack", "conversations")
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(rootDir, path)
	}
	return path
}

func GetSlackMemoryMaxSectionChars() int {
	max := viper.GetInt("slack.memory.max_section_chars")
	if max <= 0 {
		return 2000
	}
	return max
}
```

- [ ] **Step 4: Add Slack gateway CLI flags**

Modify `internal/cmd/slack.go` globals:

```go
slackGatewayMemoryEnabled bool
slackGatewayMemoryRoot string
slackGatewayMemoryMaxSectionChars int
```

In `slackGatewayServeCmd.Run`, include memory config:

```go
MemoryEnabled: config.GetSlackMemoryEnabled(),
MemoryRoot: config.GetSlackMemoryRoot(),
MemoryMaxSectionChars: config.GetSlackMemoryMaxSectionChars(),
```

Apply changed flags:

```go
if cmd.Flags().Changed("memory") {
	cfg.MemoryEnabled = slackGatewayMemoryEnabled
}
if strings.TrimSpace(slackGatewayMemoryRoot) != "" {
	cfg.MemoryRoot = strings.TrimSpace(slackGatewayMemoryRoot)
}
if cmd.Flags().Changed("memory-max-section-chars") {
	cfg.MemoryMaxSectionChars = slackGatewayMemoryMaxSectionChars
}
```

Add output fields:

```go
"memory_enabled": cfg.MemoryEnabled,
"memory_root": cfg.MemoryRoot,
```

Add flags:

```go
slackGatewayServeCmd.Flags().BoolVar(&slackGatewayMemoryEnabled, "memory", false, "enable Slack conversation memory and audit folders")
slackGatewayServeCmd.Flags().StringVar(&slackGatewayMemoryRoot, "memory-root", "", "root directory for Slack conversation memory")
slackGatewayServeCmd.Flags().IntVar(&slackGatewayMemoryMaxSectionChars, "memory-max-section-chars", 0, "max characters loaded from each memory section into Codex prompts")
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/config ./internal/cmd ./internal/slack
```

Expected: PASS.

- [ ] **Step 6: Format**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/config/config.go internal/config/slack_config_test.go internal/cmd/slack.go
```

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/slack_config_test.go internal/cmd/slack.go
git commit -m "feat: configure slack memory"
```

## Task 6: Add Slack Memory CLI Commands

**Files:**
- Modify: `internal/cmd/slack.go`
- Modify: `internal/cmd/slack_test.go`

- [ ] **Step 1: Write failing command registration test**

Add to `TestSlackMessageCommandsAreRegistered` in `internal/cmd/slack_test.go`:

```go
{"slack", "memory", "path"},
{"slack", "memory", "show"},
{"slack", "memory", "append"},
```

- [ ] **Step 2: Run command tests to verify failure**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/cmd
```

Expected: FAIL because `slack memory` commands are not registered.

- [ ] **Step 3: Implement commands**

Modify `internal/cmd/slack.go` imports:

```go
import "github.com/yjwong/lark-cli/internal/platform"
import "github.com/yjwong/lark-cli/internal/slackmemory"
```

Add command globals:

```go
var slackMemoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Slack memory commands",
	Long:  "Inspect and update local Slack memory files.",
}

var (
	slackMemoryTeamID string
	slackMemoryChannel string
	slackMemoryThreadTS string
	slackMemoryScope string
	slackMemoryText string
)
```

Add helper:

```go
func slackMemoryEventFromFlags() platform.MessageEvent {
	return platform.MessageEvent{
		Provider:  "slack",
		TeamID:    slackMemoryTeamID,
		ChannelID: slackMemoryChannel,
		ThreadID:  slackMemoryThreadTS,
		MessageID: slackMemoryThreadTS,
	}
}
```

Add commands:

```go
var slackMemoryPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print Slack memory paths",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMemoryChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		store := slackmemory.NewStore(slackmemory.Config{Root: config.GetSlackMemoryRoot()})
		event := slackMemoryEventFromFlags()
		output.JSON(map[string]interface{}{
			"root": store.Root(),
			"channel_memory": store.ChannelMemoryPath(event),
			"thread_memory": store.ThreadMemoryPath(event),
			"thread_summary": store.ThreadSummaryPath(event),
		})
	},
}

var slackMemoryShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show Slack memory markdown",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMemoryChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		store := slackmemory.NewStore(slackmemory.Config{Root: config.GetSlackMemoryRoot()})
		event := slackMemoryEventFromFlags()
		path := store.ChannelMemoryPath(event)
		if slackMemoryScope == "thread" {
			path = store.ThreadMemoryPath(event)
		}
		if slackMemoryScope == "summary" {
			path = store.ThreadSummaryPath(event)
		}
		text, err := store.ReadMarkdown(path, 0)
		if err != nil {
			output.Fatal("MEMORY_ERROR", err)
		}
		output.JSON(map[string]interface{}{"path": path, "text": text})
	},
}

var slackMemoryAppendCmd = &cobra.Command{
	Use:   "append",
	Short: "Append text to Slack memory markdown",
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMemoryChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if strings.TrimSpace(slackMemoryText) == "" {
			output.Fatalf("VALIDATION_ERROR", "--text is required")
		}
		store := slackmemory.NewStore(slackmemory.Config{Root: config.GetSlackMemoryRoot()})
		event := slackMemoryEventFromFlags()
		path := store.ChannelMemoryPath(event)
		if slackMemoryScope == "thread" {
			path = store.ThreadMemoryPath(event)
		}
		if slackMemoryScope == "summary" {
			path = store.ThreadSummaryPath(event)
		}
		if err := store.AppendMarkdown(path, slackMemoryText); err != nil {
			output.Fatal("MEMORY_ERROR", err)
		}
		output.JSON(map[string]interface{}{"success": true, "path": path})
	},
}
```

Add `Root()` method to `internal/slackmemory/store.go`:

```go
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}
```

Register flags and commands:

```go
for _, c := range []*cobra.Command{slackMemoryPathCmd, slackMemoryShowCmd, slackMemoryAppendCmd} {
	c.Flags().StringVar(&slackMemoryTeamID, "team", "", "Slack team ID")
	c.Flags().StringVar(&slackMemoryChannel, "channel", "", "Slack channel ID (required)")
	c.Flags().StringVar(&slackMemoryThreadTS, "thread-ts", "", "Slack thread timestamp")
}
slackMemoryShowCmd.Flags().StringVar(&slackMemoryScope, "scope", "channel", "memory scope: channel, thread, or summary")
slackMemoryAppendCmd.Flags().StringVar(&slackMemoryScope, "scope", "channel", "memory scope: channel, thread, or summary")
slackMemoryAppendCmd.Flags().StringVar(&slackMemoryText, "text", "", "memory text to append")
slackMemoryCmd.AddCommand(slackMemoryPathCmd, slackMemoryShowCmd, slackMemoryAppendCmd)
slackCmd.AddCommand(slackMemoryCmd)
```

- [ ] **Step 4: Run focused tests**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/cmd ./internal/slackmemory
```

Expected: PASS.

- [ ] **Step 5: Format**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/cmd/slack.go internal/cmd/slack_test.go internal/slackmemory/store.go
```

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/slack.go internal/cmd/slack_test.go internal/slackmemory/store.go
git commit -m "feat: add slack memory commands"
```

## Task 7: Update Docs And Example Config

**Files:**
- Modify: `config.example.yaml`
- Modify: `USER_GUIDE.md`
- Modify: `slack-migration.md`

- [ ] **Step 1: Update `config.example.yaml`**

Add under `slack:`:

```yaml
  memory:
    enabled: false
    # Root directory for channel/thread audit logs and memory markdown files.
    root: ".slack/conversations"
    # Maximum characters loaded from each memory file into Codex prompts.
    max_section_chars: 2000
```

- [ ] **Step 2: Update `USER_GUIDE.md`**

Replace the Future Memory Extension limitation paragraph with current behavior:

```markdown
When Slack memory is enabled, the gateway writes inbound and outbound thread
records under `.slack/conversations/`. It also loads channel memory, thread
memory, and thread summaries into the Codex prompt when those Markdown files
exist.

Enable it:

```bash
./lark slack gateway serve \
  --agent \
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
  --agent-workspace "$HOME/CodexChat"
```
```

- [ ] **Step 3: Update `slack-migration.md`**

Add a new section after Phase 3:

```markdown
### Memory Extension

Status: Complete for filesystem-backed audit and explicit memory.

- [x] Store Slack inbound events in channel daily logs and thread logs.
- [x] Store outbound replies in thread logs.
- [x] Support channel memory, thread memory, and thread summaries as Markdown.
- [x] Inject existing memory Markdown into Codex prompts.
- [x] Add CLI commands for memory path/show/append.
- [ ] Automatic summarization remains deferred.
```

- [ ] **Step 4: Documentation review**

Run:

```bash
rg -n "Future Memory Extension|--memory|slack memory" USER_GUIDE.md config.example.yaml slack-migration.md
```

Expected: no obsolete `Future Memory Extension` section remains; the output should show the new memory command/config references.

- [ ] **Step 5: Commit**

```bash
git add config.example.yaml USER_GUIDE.md slack-migration.md
git commit -m "docs: document slack memory support"
```

## Task 8: Full Verification

**Files:**
- No source edits unless verification finds failures.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slackmemory ./internal/slack ./internal/agent ./internal/cmd ./internal/config
```

Expected: PASS.

- [ ] **Step 2: Run full test suite**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...
```

Expected: PASS.

- [ ] **Step 3: Build CLI**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 sh -c 'git config --global --add safe.directory /work && go build -ldflags "-s -w" -o ./lark ./cmd/lark'
```

Expected: exit 0.

- [ ] **Step 4: Remove generated binary if ignored**

Run:

```bash
rm -f ./lark
git status --short
```

Expected: only intended source/doc changes remain.

- [ ] **Step 5: Manual smoke test**

Run a local gateway:

```bash
LARK_CONFIG_DIR="$HOME/.lark-codex-chat" \
SLACK_APP_TOKEN="xapp-..." \
SLACK_BOT_TOKEN="xoxb-..." \
./lark slack gateway serve \
  --agent \
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
  --agent-workspace "$HOME/CodexChat"
```

Send a Slack DM:

```text
Remember that I prefer concise implementation plans. Save this in memory.
```

Expected files:

```text
$HOME/CodexChat/.slack/conversations/<team>/<channel>/daily/<today>.jsonl
$HOME/CodexChat/.slack/conversations/<team>/<channel>/threads/<thread>/events.jsonl
```

Then append explicit memory:

```bash
./lark slack memory append --channel D123 --scope channel --text "- User prefers concise implementation plans."
./lark slack memory show --channel D123 --scope channel
```

Expected: JSON output contains the appended memory text.

- [ ] **Step 6: Final commit if any verification fixes were needed**

```bash
git add .
git commit -m "chore: verify slack memory support"
```

Only run this commit if verification required follow-up changes.

## Risks And Follow-Ups

- **Automatic summarization:** Deferred. Add later as a separate plan that invokes Codex or another summarizer after a thread exceeds a size threshold.
- **Prompt bloat:** Controlled by `slack.memory.max_section_chars`; keep the default conservative.
- **Concurrent writes:** Store uses one process-local mutex. If multiple gateway processes write the same memory root, JSONL append is usually safe enough on local filesystems but not guaranteed as a cross-process transaction.
- **Privacy:** Memory files may contain sensitive Slack content. Users should place memory roots in private local folders and avoid committing `.slack/conversations`.
- **Multiple Slack apps:** Recommended. Each app can use a separate `LARK_CONFIG_DIR`, token set, event log, memory root, and workspace.

## Self-Review

- Spec coverage: The plan covers channel/thread folders, raw audit logs, summaries/memory Markdown files, prompt injection, CLI memory commands, config, docs, and verification.
- Placeholder scan: No placeholder markers are used as implementation instructions.
- Type consistency: `slackmemory.Store`, `ConversationRecord`, `ContextOptions`, `PromptContextProvider`, and `ReplyObserver` are introduced before later tasks use them.
