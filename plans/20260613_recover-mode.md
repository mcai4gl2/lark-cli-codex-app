# Slack Recover Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add durable Slack catch-up for known participating threads and show a processing reaction while Codex is handling a message.

**Architecture:** Extend the Slack gateway with a small durable recovery state store under the existing Slack memory root. The gateway records participating thread coordinates and last processed Slack timestamps, runs catch-up on startup and after successful Socket Mode reconnects, and uses Slack reactions plus an agent lifecycle observer to add/remove a processing emoji around asynchronous Codex execution.

**Tech Stack:** Go, Slack Web API (`conversations.replies`, `reactions.add`, `reactions.remove`), Gorilla WebSocket, existing `internal/slack`, `internal/agent`, `internal/slackmemory`, Docker `golang:1.24` for formatting/tests/build.

---

## Scope

Implement three recover modes:

- `thread` (default): catch up all new user messages in known participating threads.
- `mention-dm`: catch up only messages that would normally trigger the bot: app mentions and DMs.
- `off`: disable catch-up.

Do not scan whole channels. A thread becomes participating when the gateway accepts an inbound Slack event for processing or posts a final bot reply in that thread.

## File Structure

- Modify `internal/slack/client.go`
  - Add `Oldest`/`Latest` to `ThreadOptions` if not already present in the request payload.
  - Keep existing reactions methods as the reaction transport.
- Modify `internal/slack/client_test.go`
  - Verify `conversations.replies` receives `oldest` and `latest`.
- Create `internal/slack/recover.go`
  - Define recover mode constants.
  - Define durable `RecoveryStore`.
  - Define thread key/record types.
  - Provide JSON load/save, mark participation, check dedupe, and advance last processed timestamp.
- Create `internal/slack/recover_test.go`
  - Test durable store round-trip.
  - Test timestamp dedupe and advancement.
  - Test default mode normalization.
- Modify `internal/slack/events.go`
  - Add helper to build a normalized `platform.MessageEvent` from a Slack `Message` returned by `conversations.replies`.
  - Preserve existing Events API normalization behavior.
- Modify `internal/slack/events_test.go`
  - Test catch-up message normalization for participating thread messages.
  - Test catch-up policy filtering for `thread`, `mention-dm`, and `off`.
- Modify `internal/agent/codex.go`
  - Add a generic processing lifecycle observer interface.
  - Call observer start/finish around asynchronous agent execution.
- Modify `internal/agent/codex_test.go`
  - Verify lifecycle observer receives start and finish even when execution fails.
- Modify `internal/slack/gateway.go`
  - Add catch-up config fields.
  - Wire recovery store when memory root is available.
  - Run catch-up on startup and after reconnect.
  - Record participation and last processed timestamps around live and catch-up processing.
  - Add Slack processing reaction observer.
- Modify `internal/slack/gateway_test.go`
  - Test catch-up after reconnect processes missed participating thread message.
  - Test catch-up dedupes already processed messages.
  - Test processing reaction add/remove wraps agent dispatch.
- Modify `internal/cmd/slack.go`
  - Add `--recover-mode` flag and include it in startup JSON.
- Modify `internal/config/config.go` and `config.example.yaml`
  - Add `slack.gateway.recover_mode` or equivalent env-backed config using existing config style.
- Modify `USER_GUIDE.md` and `docs/codex-slack-deployment.md`
  - Document recover modes, default behavior, state path, required Slack scopes, and processing emoji behavior.

## Task 1: Add Thread API Oldest/Latest Support

**Files:**
- Modify: `internal/slack/client.go`
- Modify: `internal/slack/client_test.go`

- [ ] **Step 1: Write the failing test**

Add this test to `internal/slack/client_test.go` near `TestClientThreadCallsConversationsReplies`:

```go
func TestClientThreadIncludesOldestAndLatest(t *testing.T) {
	var got struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Limit   int    `json:"limit"`
		Oldest  string `json:"oldest"`
		Latest  string `json:"latest"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[],"has_more":false}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	_, err := client.Thread(context.Background(), ThreadOptions{
		Channel:  "C123",
		ThreadTS: "111.222",
		Limit:    100,
		Oldest:   "111.222",
		Latest:   "222.333",
	})
	if err != nil {
		t.Fatalf("Thread() error = %v", err)
	}

	if got.Channel != "C123" || got.TS != "111.222" || got.Limit != 100 || got.Oldest != "111.222" || got.Latest != "222.333" {
		t.Fatalf("request = %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run TestClientThreadIncludesOldestAndLatest -count=1
```

Expected: FAIL because `Thread()` does not include `oldest`/`latest` in the request payload.

- [ ] **Step 3: Implement minimal client change**

In `internal/slack/client.go`, update `Thread()` payload construction to include:

```go
if strings.TrimSpace(opts.Oldest) != "" {
	payload["oldest"] = strings.TrimSpace(opts.Oldest)
}
if strings.TrimSpace(opts.Latest) != "" {
	payload["latest"] = strings.TrimSpace(opts.Latest)
}
```

- [ ] **Step 4: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slack/client.go internal/slack/client_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run TestClientThreadIncludesOldestAndLatest -count=1
```

Expected: PASS.

## Task 2: Add Durable Recovery State Store

**Files:**
- Create: `internal/slack/recover.go`
- Create: `internal/slack/recover_test.go`

- [ ] **Step 1: Write failing store tests**

Create `internal/slack/recover_test.go`:

```go
package slack

import (
	"path/filepath"
	"testing"
)

func TestNormalizeRecoverMode(t *testing.T) {
	tests := map[string]RecoverMode{
		"":           RecoverModeThread,
		"thread":     RecoverModeThread,
		"mention-dm": RecoverModeMentionDM,
		"off":        RecoverModeOff,
		"unknown":    RecoverModeThread,
	}
	for input, want := range tests {
		if got := NormalizeRecoverMode(input); got != want {
			t.Fatalf("NormalizeRecoverMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRecoveryStorePersistsThreadState(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}

	if err := store.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := store.MarkProcessed(key, "111.333"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	reloaded := NewRecoveryStore(store.Path())
	threads, err := reloaded.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("thread count = %d", len(threads))
	}
	if threads[0].Key != key || threads[0].LastProcessedTS != "111.333" {
		t.Fatalf("thread record = %+v", threads[0])
	}
}

func TestRecoveryStoreSkipsAlreadyProcessedMessages(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}
	if err := store.MarkProcessed(key, "111.333"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	if store.ShouldProcess(key, "111.222") {
		t.Fatal("ShouldProcess older message = true")
	}
	if store.ShouldProcess(key, "111.333") {
		t.Fatal("ShouldProcess same message = true")
	}
	if !store.ShouldProcess(key, "111.444") {
		t.Fatal("ShouldProcess newer message = false")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run 'TestNormalizeRecoverMode|TestRecoveryStore' -count=1
```

Expected: FAIL because recover types and store do not exist.

- [ ] **Step 3: Implement recovery store**

Create `internal/slack/recover.go`:

```go
package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type RecoverMode string

const (
	RecoverModeThread    RecoverMode = "thread"
	RecoverModeMentionDM RecoverMode = "mention-dm"
	RecoverModeOff       RecoverMode = "off"
)

type RecoveryThreadKey struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
}

type RecoveryThreadRecord struct {
	Key             RecoveryThreadKey `json:"key"`
	LastProcessedTS string            `json:"last_processed_ts,omitempty"`
	LastSeenAt      string            `json:"last_seen_at,omitempty"`
}

type recoveryStateFile struct {
	Threads []RecoveryThreadRecord `json:"threads"`
}

type RecoveryStore struct {
	path string
	mu   sync.Mutex
}

func NormalizeRecoverMode(mode string) RecoverMode {
	switch RecoverMode(strings.TrimSpace(mode)) {
	case RecoverModeMentionDM:
		return RecoverModeMentionDM
	case RecoverModeOff:
		return RecoverModeOff
	case RecoverModeThread, "":
		return RecoverModeThread
	default:
		return RecoverModeThread
	}
}

func NewRecoveryStore(path string) *RecoveryStore {
	return &RecoveryStore{path: strings.TrimSpace(path)}
}

func (s *RecoveryStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *RecoveryStore) Enabled() bool {
	return s != nil && strings.TrimSpace(s.path) != ""
}

func (s *RecoveryStore) Threads() ([]RecoveryThreadRecord, error) {
	if !s.Enabled() {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	return append([]RecoveryThreadRecord(nil), state.Threads...), nil
}

func (s *RecoveryStore) MarkParticipating(key RecoveryThreadKey) error {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	idx := findRecoveryThread(state.Threads, key)
	now := time.Now().Format(time.RFC3339Nano)
	if idx < 0 {
		state.Threads = append(state.Threads, RecoveryThreadRecord{Key: key, LastSeenAt: now})
	} else {
		state.Threads[idx].LastSeenAt = now
	}
	return s.saveLocked(state)
}

func (s *RecoveryStore) MarkProcessed(key RecoveryThreadKey, messageTS string) error {
	if !s.Enabled() {
		return nil
	}
	messageTS = strings.TrimSpace(messageTS)
	if messageTS == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	idx := findRecoveryThread(state.Threads, key)
	now := time.Now().Format(time.RFC3339Nano)
	if idx < 0 {
		state.Threads = append(state.Threads, RecoveryThreadRecord{Key: key, LastProcessedTS: messageTS, LastSeenAt: now})
	} else {
		if slackTSAfter(messageTS, state.Threads[idx].LastProcessedTS) {
			state.Threads[idx].LastProcessedTS = messageTS
		}
		state.Threads[idx].LastSeenAt = now
	}
	return s.saveLocked(state)
}

func (s *RecoveryStore) ShouldProcess(key RecoveryThreadKey, messageTS string) bool {
	if !s.Enabled() {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return true
	}
	idx := findRecoveryThread(state.Threads, key)
	if idx < 0 {
		return true
	}
	return slackTSAfter(messageTS, state.Threads[idx].LastProcessedTS)
}

func (s *RecoveryStore) loadLocked() (recoveryStateFile, error) {
	var state recoveryStateFile
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func (s *RecoveryStore) saveLocked(state recoveryStateFile) error {
	sort.Slice(state.Threads, func(i, j int) bool {
		a := state.Threads[i].Key
		b := state.Threads[j].Key
		if a.TeamID != b.TeamID {
			return a.TeamID < b.TeamID
		}
		if a.ChannelID != b.ChannelID {
			return a.ChannelID < b.ChannelID
		}
		return a.ThreadTS < b.ThreadTS
	})
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func findRecoveryThread(records []RecoveryThreadRecord, key RecoveryThreadKey) int {
	for i, record := range records {
		if record.Key == key {
			return i
		}
	}
	return -1
}

func slackTSAfter(candidate, previous string) bool {
	candidate = strings.TrimSpace(candidate)
	previous = strings.TrimSpace(previous)
	if candidate == "" {
		return false
	}
	if previous == "" {
		return true
	}
	return candidate > previous
}
```

- [ ] **Step 4: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slack/recover.go internal/slack/recover_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run 'TestNormalizeRecoverMode|TestRecoveryStore' -count=1
```

Expected: PASS.

## Task 3: Normalize Catch-Up Thread Messages

**Files:**
- Modify: `internal/slack/events.go`
- Modify: `internal/slack/events_test.go`

- [ ] **Step 1: Write failing tests**

Add this helper and tests to `internal/slack/events_test.go`:

```go
func catchUpEvent(teamID, channelID, threadTS, messageTS, text string) platform.MessageEvent {
	event, ok := NormalizeThreadMessageForCatchUp(teamID, channelID, "channel", threadTS, Message{
		Type:     "message",
		User:     "U234",
		Text:     text,
		TS:       messageTS,
		ThreadTS: threadTS,
	}, "U999", RecoverModeThread)
	if !ok {
		panic("catchUpEvent helper did not normalize")
	}
	return event
}

func TestNormalizeThreadMessageForCatchUpProcessesParticipatingThreadReply(t *testing.T) {
	event, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", "1710000000.000100", Message{
		Type:     "message",
		User:     "U234",
		Text:     "continue without mention",
		TS:       "1710000000.000300",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeThread)

	if !ok {
		t.Fatal("NormalizeThreadMessageForCatchUp() ok = false")
	}
	if event.Provider != "slack" || event.TeamID != "T123" || event.ChannelID != "C345" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.EventType != "message" || event.ThreadID != "1710000000.000100" || event.MessageID != "1710000000.000300" {
		t.Fatalf("event routing = %+v", event)
	}
	if event.MessageText != "continue without mention" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
}

func TestNormalizeThreadMessageForCatchUpFiltersByMode(t *testing.T) {
	plainReply := Message{Type: "message", User: "U234", Text: "plain", TS: "2", ThreadTS: "1"}
	if _, ok := NormalizeThreadMessageForCatchUp("T", "C", "channel", "1", plainReply, "U999", RecoverModeOff); ok {
		t.Fatal("off mode processed message")
	}
	if _, ok := NormalizeThreadMessageForCatchUp("T", "C", "channel", "1", plainReply, "U999", RecoverModeMentionDM); ok {
		t.Fatal("mention-dm mode processed plain channel reply")
	}

	mentionReply := Message{Type: "message", User: "U234", Text: "<@U999> continue", TS: "3", ThreadTS: "1"}
	event, ok := NormalizeThreadMessageForCatchUp("T", "C", "channel", "1", mentionReply, "U999", RecoverModeMentionDM)
	if !ok {
		t.Fatal("mention-dm mode ignored mention reply")
	}
	if event.MessageText != "continue" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
}
```

Add `github.com/yjwong/lark-cli/internal/platform` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run 'TestNormalizeThreadMessageForCatchUp' -count=1
```

Expected: FAIL because `NormalizeThreadMessageForCatchUp` does not exist.

- [ ] **Step 3: Implement catch-up normalization**

In `internal/slack/events.go`, add:

```go
func NormalizeThreadMessageForCatchUp(teamID, channelID, channelType, threadTS string, message Message, botUserID string, mode RecoverMode) (platform.MessageEvent, bool) {
	if mode == RecoverModeOff {
		return platform.MessageEvent{}, false
	}
	if message.Type != "" && message.Type != "message" {
		return platform.MessageEvent{}, false
	}
	event := slackInnerEvent{
		Type:        "message",
		Subtype:     message.Subtype,
		User:        message.User,
		BotID:       message.BotID,
		Channel:     channelID,
		ChannelType: channelType,
		Text:        message.Text,
		TS:          message.TS,
		ThreadTS:    message.ThreadTS,
	}
	if event.ThreadTS == "" {
		event.ThreadTS = threadTS
	}
	if shouldIgnoreEvent(event, botUserID) {
		return platform.MessageEvent{}, false
	}

	text := strings.TrimSpace(event.Text)
	if mode == RecoverModeMentionDM {
		if channelType == "im" {
			// Direct messages are accepted below.
		} else if slackMentionPattern.MatchString(text) {
			text = stripMention(text, botUserID)
		} else {
			return platform.MessageEvent{}, false
		}
	}
	if strings.TrimSpace(text) == "" {
		return platform.MessageEvent{}, false
	}

	resolvedThread := strings.TrimSpace(event.ThreadTS)
	if resolvedThread == "" {
		resolvedThread = strings.TrimSpace(threadTS)
	}
	if resolvedThread == "" {
		resolvedThread = strings.TrimSpace(event.TS)
	}
	resolvedChannelType := strings.TrimSpace(channelType)
	if resolvedChannelType == "" {
		resolvedChannelType = inferChannelType(channelID)
	}

	return platform.MessageEvent{
		Provider:    "slack",
		ReceivedAt:  time.Now().Format(time.RFC3339Nano),
		EventType:   "message",
		TeamID:      teamID,
		ChannelID:   channelID,
		ChannelType: resolvedChannelType,
		MessageID:   event.TS,
		ThreadID:    resolvedThread,
		UserID:      event.User,
		BotID:       strings.TrimSpace(botUserID),
		MessageType: "text",
		MessageText: strings.TrimSpace(text),
		RawContent:  event.Text,
	}, true
}
```

- [ ] **Step 4: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slack/events.go internal/slack/events_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run 'TestNormalizeThreadMessageForCatchUp' -count=1
```

Expected: PASS.

## Task 4: Add Agent Lifecycle Observer

**Files:**
- Modify: `internal/agent/codex.go`
- Modify: `internal/agent/codex_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

Add this fake observer and test to `internal/agent/codex_test.go`:

```go
type fakeProcessingObserver struct {
	mu       sync.Mutex
	started  []platform.MessageEvent
	finished []platform.MessageEvent
}

func (f *fakeProcessingObserver) ProcessingStarted(event platform.MessageEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, event)
	return nil
}

func (f *fakeProcessingObserver) ProcessingFinished(event platform.MessageEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, event)
	return nil
}

func TestRunnerProcessingObserverWrapsExecution(t *testing.T) {
	bin := helperScript(t, "printf 'agent result' > \"$LARK_CODEX_OUTPUT_FILE\"")
	observer := &fakeProcessingObserver{}
	messenger := &fakeMessenger{}
	runner := NewRunnerWithMessenger(Config{
		Enabled:            true,
		CodexBinary:        bin,
		Workspace:          t.TempDir(),
		ProcessingObserver: observer,
	}, nil, messenger)

	entry := platform.MessageEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		MessageID:   "111.222",
		ThreadID:    "111.222",
		MessageText: "run",
	}
	runner.run(entry)

	if len(observer.started) != 1 || observer.started[0].MessageID != "111.222" {
		t.Fatalf("started = %+v", observer.started)
	}
	if len(observer.finished) != 1 || observer.finished[0].MessageID != "111.222" {
		t.Fatalf("finished = %+v", observer.finished)
	}
}
```

If existing tests do not expose `helperScript`/`fakeMessenger`, adapt the names to existing helpers in `internal/agent/codex_test.go`; keep the assertion shape the same.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run TestRunnerProcessingObserverWrapsExecution -count=1
```

Expected: FAIL because `ProcessingObserver` does not exist.

- [ ] **Step 3: Implement lifecycle observer**

In `internal/agent/codex.go`, add:

```go
type ProcessingObserver interface {
	ProcessingStarted(event platform.MessageEvent) error
	ProcessingFinished(event platform.MessageEvent) error
}
```

Add to `Config`:

```go
ProcessingObserver ProcessingObserver
```

At the start of `run`, before ack reply:

```go
if r.cfg.ProcessingObserver != nil {
	if err := r.cfg.ProcessingObserver.ProcessingStarted(entry); err != nil {
		r.logger.Printf("processing observer start failed for message_id=%s: %v", entry.MessageID, err)
	}
	defer func() {
		if err := r.cfg.ProcessingObserver.ProcessingFinished(entry); err != nil {
			r.logger.Printf("processing observer finish failed for message_id=%s: %v", entry.MessageID, err)
		}
	}()
}
```

- [ ] **Step 4: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/codex.go internal/agent/codex_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run TestRunnerProcessingObserverWrapsExecution -count=1
```

Expected: PASS.

## Task 5: Add Slack Processing Reaction Observer

**Files:**
- Modify: `internal/slack/gateway.go`
- Modify: `internal/slack/gateway_test.go`

- [ ] **Step 1: Write failing reaction observer test**

Add this test to `internal/slack/gateway_test.go`:

```go
func TestProcessingReactionObserverAddsAndRemovesReaction(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	observer := processingReactionObserver{client: client, reactionName: "eyes"}
	entry := platform.MessageEvent{ChannelID: "C123", MessageID: "111.222"}

	if err := observer.ProcessingStarted(entry); err != nil {
		t.Fatalf("ProcessingStarted() error = %v", err)
	}
	if err := observer.ProcessingFinished(entry); err != nil {
		t.Fatalf("ProcessingFinished() error = %v", err)
	}

	want := []string{"/reactions.add", "/reactions.remove"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}
```

Add `reflect` to imports.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run TestProcessingReactionObserverAddsAndRemovesReaction -count=1
```

Expected: FAIL because `processingReactionObserver` does not exist.

- [ ] **Step 3: Implement observer**

In `internal/slack/gateway.go`, add:

```go
type processingReactionObserver struct {
	client       *Client
	reactionName string
}

func (o processingReactionObserver) ProcessingStarted(event platform.MessageEvent) error {
	if o.client == nil || strings.TrimSpace(o.reactionName) == "" || strings.TrimSpace(event.ChannelID) == "" || strings.TrimSpace(event.MessageID) == "" {
		return nil
	}
	_, err := o.client.AddReaction(context.Background(), ReactionOptions{
		Channel:   event.ChannelID,
		Timestamp: event.MessageID,
		Name:      o.reactionName,
	})
	return err
}

func (o processingReactionObserver) ProcessingFinished(event platform.MessageEvent) error {
	if o.client == nil || strings.TrimSpace(o.reactionName) == "" || strings.TrimSpace(event.ChannelID) == "" || strings.TrimSpace(event.MessageID) == "" {
		return nil
	}
	_, err := o.client.RemoveReaction(context.Background(), ReactionOptions{
		Channel:   event.ChannelID,
		Timestamp: event.MessageID,
		Name:      o.reactionName,
	})
	return err
}
```

Add `ProcessingReactionName string` to `slack.Config`. In `NewGateway`, before constructing `agent.NewRunnerWithMessenger`, if the agent is enabled and the reaction name is not empty:

```go
cfg.Agent.ProcessingObserver = processingReactionObserver{
	client:       client,
	reactionName: cfg.ProcessingReactionName,
}
```

- [ ] **Step 4: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slack/gateway.go internal/slack/gateway_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run TestProcessingReactionObserverAddsAndRemovesReaction -count=1
```

Expected: PASS.

## Task 6: Wire Catch-Up Into Gateway

**Files:**
- Modify: `internal/slack/gateway.go`
- Modify: `internal/slack/gateway_test.go`

- [ ] **Step 1: Write failing catch-up tests**

Add test server helpers and tests to `internal/slack/gateway_test.go`:

```go
func TestGatewayCatchUpProcessesParticipatingThreadMessages(t *testing.T) {
	var replies int
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.replies":
			replies++
			_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U234","text":"missed reply","ts":"111.333","thread_ts":"111.222"}],"has_more":false}`))
		case "/chat.postMessage", "/reactions.add", "/reactions.remove":
			_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"999.000"}`))
		default:
			t.Fatalf("unexpected path = %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	messenger := &captureMessenger{}
	memoryRoot := t.TempDir()
	service := NewGateway(Config{
		BotUserID:              "U999",
		BotToken:               "xoxb-test",
		EventLogPath:           filepath.Join(t.TempDir(), "events.jsonl"),
		Messenger:              messenger,
		APIBaseURL:             apiServer.URL,
		MemoryEnabled:          true,
		MemoryRoot:             memoryRoot,
		RecoverMode:            RecoverModeThread,
		ProcessingReactionName: "eyes",
		Agent: AgentConfig{Enabled: false},
		DesktopQueueRoot: t.TempDir(),
	})

	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}
	if err := service.recovery.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := service.catchUp(context.Background()); err != nil {
		t.Fatalf("catchUp() error = %v", err)
	}

	if replies != 1 {
		t.Fatalf("conversations.replies calls = %d", replies)
	}
	data, err := os.ReadFile(service.cfg.EventLogPath)
	if err != nil {
		t.Fatalf("ReadFile(event log): %v", err)
	}
	if !strings.Contains(string(data), "missed reply") {
		t.Fatalf("event log = %s", string(data))
	}
	if service.recovery.ShouldProcess(key, "111.333") {
		t.Fatal("state should not process already handled message")
	}
}
```

The final assertion intentionally checks current planned API shape. If the store exposes `LastProcessedTS` only through `Threads()`, use:

```go
threads, err := service.recovery.Threads()
if err != nil {
	t.Fatalf("Threads() error = %v", err)
}
if len(threads) != 1 || threads[0].LastProcessedTS != "111.333" {
	t.Fatalf("threads = %+v", threads)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run TestGatewayCatchUpProcessesParticipatingThreadMessages -count=1
```

Expected: FAIL because gateway recovery fields and `catchUp` do not exist.

- [ ] **Step 3: Implement gateway recovery fields**

In `internal/slack/gateway.go`, add to `Config`:

```go
RecoverMode            RecoverMode
ProcessingReactionName string
```

Add to `Gateway`:

```go
recoverMode RecoverMode
recovery    *RecoveryStore
```

In `NewGateway`, after memory root normalization:

```go
recoverMode := NormalizeRecoverMode(cfg.RecoverMode)
var recovery *RecoveryStore
if recoverMode != RecoverModeOff && strings.TrimSpace(cfg.MemoryRoot) != "" {
	recovery = NewRecoveryStore(filepath.Join(cfg.MemoryRoot, ".state", "recover-state.json"))
}
```

Assign `recoverMode` and `recovery` in the returned `Gateway`.

- [ ] **Step 4: Implement catch-up**

Add these methods to `internal/slack/gateway.go`:

```go
func (g *Gateway) catchUp(ctx context.Context) error {
	if g.recoverMode == RecoverModeOff || g.recovery == nil || !g.recovery.Enabled() {
		return nil
	}
	threads, err := g.recovery.Threads()
	if err != nil {
		return err
	}
	for _, thread := range threads {
		if ctx.Err() != nil {
			return nil
		}
		if err := g.catchUpThread(ctx, thread); err != nil {
			g.logger.Printf("Slack catch-up failed channel_id=%s thread_ts=%s: %v", thread.Key.ChannelID, thread.Key.ThreadTS, err)
		}
	}
	return nil
}

func (g *Gateway) catchUpThread(ctx context.Context, thread RecoveryThreadRecord) error {
	result, err := g.client.Thread(ctx, ThreadOptions{
		Channel:  thread.Key.ChannelID,
		ThreadTS: thread.Key.ThreadTS,
		Limit:    100,
		Oldest:   thread.LastProcessedTS,
	})
	if err != nil {
		return err
	}
	for i := len(result.Messages) - 1; i >= 0; i-- {
		message := result.Messages[i]
		if !g.recovery.ShouldProcess(thread.Key, message.TS) {
			continue
		}
		entry, ok := NormalizeThreadMessageForCatchUp(thread.Key.TeamID, thread.Key.ChannelID, inferChannelType(thread.Key.ChannelID), thread.Key.ThreadTS, message, g.cfg.BotUserID, g.recoverMode)
		if !ok {
			if err := g.recovery.MarkProcessed(thread.Key, message.TS); err != nil {
				return err
			}
			continue
		}
		if err := g.processEntry(ctx, entry); err != nil {
			return err
		}
		if err := g.recovery.MarkProcessed(thread.Key, entry.MessageID); err != nil {
			return err
		}
	}
	return nil
}
```

Extract live event processing after normalization into:

```go
func (g *Gateway) processEntry(ctx context.Context, entry platform.MessageEvent) error {
	if err := g.handler.Process(entry); err != nil {
		return err
	}
	if g.memory != nil {
		if err := g.memory.RecordInbound(entry); err != nil {
			return err
		}
	}
	key := recoveryThreadKey(entry)
	if g.recovery != nil {
		if err := g.recovery.MarkParticipating(key); err != nil {
			g.logger.Printf("failed to mark Slack recovery thread message_id=%s: %v", entry.MessageID, err)
		}
	}
	if request, ok := desktop.ExtractRequest(entry.MessageText); ok {
		task, err := g.desktop.Enqueue(entry, request)
		if err != nil {
			return err
		}
		ack := fmt.Sprintf("Desktop GUI task queued: %s. I will reply here when it finishes.", task.ID)
		if err := g.desktop.Reply(task, ack); err != nil {
			g.logger.Printf("failed to acknowledge desktop task %s: %v", task.ID, err)
		}
		if g.recovery != nil {
			_ = g.recovery.MarkProcessed(key, entry.MessageID)
		}
		return nil
	}
	g.agent.Dispatch(entry)
	if g.recovery != nil {
		_ = g.recovery.MarkProcessed(key, entry.MessageID)
	}
	return nil
}
```

Update `handleEvent` to normalize, then call `processEntry`.

Add helper:

```go
func recoveryThreadKey(event platform.MessageEvent) RecoveryThreadKey {
	return RecoveryThreadKey{
		TeamID:    event.TeamID,
		ChannelID: event.ChannelID,
		ThreadTS:  threadIDFromEvent(event),
	}
}

func threadIDFromEvent(event platform.MessageEvent) string {
	if strings.TrimSpace(event.ThreadID) != "" {
		return event.ThreadID
	}
	return event.MessageID
}
```

- [ ] **Step 5: Trigger catch-up on startup and reconnect**

In `Serve`, after bot user ID is known and before opening the first socket, run:

```go
if err := g.catchUp(ctx); err != nil {
	g.logger.Printf("Slack catch-up failed at startup: %v", err)
}
```

After `serveSocketConnection` returns an error and before sleeping/reconnecting, run catch-up after the next successful connection. To do that, call `g.catchUp(ctx)` inside `serveSocketConnection` immediately after logging `connected to Slack Socket Mode`:

```go
if err := g.catchUp(ctx); err != nil {
	g.logger.Printf("Slack catch-up failed after connect: %v", err)
}
```

Do not return the catch-up error from `serveSocketConnection`; catch-up failure should not kill the gateway.

- [ ] **Step 6: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/slack/gateway.go internal/slack/gateway_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack -run TestGatewayCatchUpProcessesParticipatingThreadMessages -count=1
```

Expected: PASS.

## Task 7: Add Config and CLI Flags

**Files:**
- Modify: `internal/cmd/slack.go`
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`
- Modify: `internal/cmd/slack_test.go`

- [ ] **Step 1: Write failing config/command tests**

Add or extend tests in `internal/cmd/slack_test.go`:

```go
func TestSlackGatewayServeHasRecoverFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"slack", "gateway", "serve"})
	if err != nil {
		t.Fatalf("Find(slack gateway serve) error = %v", err)
	}
	if cmd.Flags().Lookup("recover-mode") == nil {
		t.Fatal("recover-mode flag is missing")
	}
	if cmd.Flags().Lookup("processing-reaction") == nil {
		t.Fatal("processing-reaction flag is missing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/cmd -run TestSlackGatewayServeHasRecoverFlags -count=1
```

Expected: FAIL because flags do not exist.

- [ ] **Step 3: Implement config getters**

In `internal/config/config.go`, following existing Slack config getter style, add:

```go
func GetSlackGatewayRecoverMode() string {
	return strings.TrimSpace(viper.GetString("slack.gateway.recover_mode"))
}

func GetSlackGatewayProcessingReaction() string {
	reaction := strings.TrimSpace(viper.GetString("slack.gateway.processing_reaction"))
	if reaction == "" {
		return "eyes"
	}
	return reaction
}
```

If `config.go` does not already import `strings`, add it.

- [ ] **Step 4: Wire CLI variables and flags**

In `internal/cmd/slack.go`, add package variables:

```go
slackGatewayRecoverMode        string
slackGatewayProcessingReaction string
```

Set config fields in `slackGatewayServeCmd.Run`:

```go
cfg.RecoverMode = slackgateway.NormalizeRecoverMode(config.GetSlackGatewayRecoverMode())
cfg.ProcessingReactionName = config.GetSlackGatewayProcessingReaction()
if strings.TrimSpace(slackGatewayRecoverMode) != "" {
	cfg.RecoverMode = slackgateway.NormalizeRecoverMode(slackGatewayRecoverMode)
}
if cmd.Flags().Changed("processing-reaction") {
	cfg.ProcessingReactionName = strings.Trim(strings.TrimSpace(slackGatewayProcessingReaction), ":")
}
```

Include in startup JSON:

```go
"recover_mode": cfg.RecoverMode,
"processing_reaction": cfg.ProcessingReactionName,
```

Register flags near existing gateway flags:

```go
slackGatewayServeCmd.Flags().StringVar(&slackGatewayRecoverMode, "recover-mode", "", "Slack catch-up mode: thread, mention-dm, or off")
slackGatewayServeCmd.Flags().StringVar(&slackGatewayProcessingReaction, "processing-reaction", "", "emoji reaction used while processing Slack messages; empty disables reactions")
```

- [ ] **Step 5: Update example config**

In `config.example.yaml`, add under the Slack gateway section:

```yaml
  gateway:
    recover_mode: thread
    processing_reaction: eyes
```

Match the indentation and existing key placement in the file.

- [ ] **Step 6: Format and verify**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/cmd/slack.go internal/cmd/slack_test.go internal/config/config.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/cmd ./internal/config -run 'TestSlackGatewayServeHasRecoverFlags|Test' -count=1
```

Expected: PASS.

## Task 8: Documentation

**Files:**
- Modify: `USER_GUIDE.md`
- Modify: `docs/codex-slack-deployment.md`

- [ ] **Step 1: Update user guide**

In `USER_GUIDE.md`, add a Slack recover mode section:

```markdown
### Slack Recover Mode

The Slack gateway stores recover state under the configured memory root:

```text
.slack/conversations/.state/recover-state.json
```

`--recover-mode thread` is the default. In this mode the gateway remembers
threads where the bot has participated and, on startup or Socket Mode
reconnect, calls `conversations.replies` for those threads. It processes new
user messages after the last processed Slack timestamp, including plain replies
that do not mention the bot.

Other modes:

- `--recover-mode mention-dm`: catch up only direct messages and messages that
  mention the bot.
- `--recover-mode off`: disable catch-up.

The gateway adds the configured processing reaction, default `eyes`, when it
accepts a message for Codex processing and removes that reaction after Codex
finishes. Use `--processing-reaction ""` to disable reactions.
```

- [ ] **Step 2: Update deployment doc**

In `docs/codex-slack-deployment.md`, add to Operations:

```markdown
Recover mode defaults to `thread`, which is intended for dedicated Codex Chat
threads. If the gateway reconnects or restarts, it catches up missed messages
from known participating threads. Plain channel messages outside known
participating threads are not scanned.
```

- [ ] **Step 3: Verify docs contain expected terms**

Run:

```bash
rg -n "recover-mode|Recover Mode|processing reaction|recover-state" USER_GUIDE.md docs/codex-slack-deployment.md
```

Expected: matches in both docs.

## Task 9: Integration Verification and Deployment

**Files:**
- All modified Go and docs files.

- [ ] **Step 1: Focused package tests**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slack ./internal/agent ./internal/cmd ./internal/config
```

Expected: all packages PASS.

- [ ] **Step 2: Full test suite**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...
```

Expected: all packages PASS.

- [ ] **Step 3: Build CLI**

Run:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 sh -c 'git config --global --add safe.directory /work && go build -ldflags "-s -w" -o ./lark ./cmd/lark'
```

Expected: command exits 0 and writes `./lark`.

- [ ] **Step 4: Install and restart homelab gateway**

Run:

```bash
old_pid="$(cat "$HOME/CodexChat/.slack/gateway.pid" 2>/dev/null || true)"
if [ -n "$old_pid" ] && kill -0 "$old_pid" 2>/dev/null; then
  kill "$old_pid"
  for i in $(seq 1 30); do
    kill -0 "$old_pid" 2>/dev/null || break
    sleep 0.2
  done
fi
install -m 0755 ./lark "$HOME/.local/bin/lark"
"$HOME/bin/codex-chat-gateway.sh"
```

Expected: script prints `started lark slack gateway: pid=<pid>`.

- [ ] **Step 5: Verify startup JSON and live process**

Run:

```bash
pid="$(cat "$HOME/CodexChat/.slack/gateway.pid")"
ps -p "$pid" -o pid,ppid,sid,stat,etime,cmd
tail -n 80 "$HOME/CodexChat/.slack/gateway.log"
```

Expected:

- Process is alive.
- Startup JSON includes `"recover_mode": "thread"`.
- Startup JSON includes `"processing_reaction": "eyes"`.
- Log includes `connected to Slack Socket Mode`.

- [ ] **Step 6: Manual Slack smoke test**

In Slack:

1. Mention the bot in a channel thread or DM the bot.
2. Confirm the processing reaction appears on the user message.
3. Confirm the processing reaction is removed after the final Codex reply.
4. Reply again in the same thread without mentioning the bot.
5. Restart the gateway during or before delivery.
6. Confirm catch-up processes the new thread reply without requiring repost.

Expected local checks:

```bash
tail -n 20 "$HOME/CodexChat/.slack/gateway-events.jsonl"
cat "$HOME/CodexChat/.slack/conversations/.state/recover-state.json"
```

Expected:

- Event log contains the caught-up message.
- Recover state contains the thread and advances `last_processed_ts`.

## Self-Review

- Spec coverage: Plan covers configurable recover modes, known participating thread catch-up, durable last processed message state, startup/reconnect catch-up, processing reaction add/remove, docs, Docker tests, and homelab deployment.
- Placeholder scan: No placeholder steps are left; each code task includes concrete test names, code snippets, commands, and expected results.
- Type consistency: `RecoverMode`, `RecoveryStore`, `RecoveryThreadKey`, `NormalizeThreadMessageForCatchUp`, `ProcessingObserver`, and `processingReactionObserver` names are consistent across tasks.

## Execution Options

Plan complete and saved to `plans/20260613_recover-mode.md`. Two execution options:

1. **Subagent-Driven (recommended)** - dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.

Choose one before implementation starts.
