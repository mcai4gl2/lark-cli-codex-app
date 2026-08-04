package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjwong/lark-cli/internal/inbound"
	"github.com/yjwong/lark-cli/internal/platform"
)

type fakeMessenger struct {
	event    platform.MessageEvent
	text     string
	events   []platform.MessageEvent
	texts    []string
	sequence *[]string
}

func (f *fakeMessenger) Reply(_ context.Context, event platform.MessageEvent, text string) error {
	f.event = event
	f.text = text
	f.events = append(f.events, event)
	f.texts = append(f.texts, text)
	if f.sequence != nil {
		*f.sequence = append(*f.sequence, "reply:"+text)
	}
	return nil
}

func (f *fakeMessenger) Send(_ context.Context, _ platform.MessageTarget, _ string) error {
	return nil
}

type fakeContextProvider struct {
	context   string
	err       error
	called    bool
	seenEntry inbound.LoggedEvent
	callCount int
}

func (f *fakeContextProvider) PromptContext(entry inbound.LoggedEvent) (string, error) {
	f.called = true
	f.seenEntry = entry
	f.callCount++
	return f.context, f.err
}

type fakeReplyObserver struct {
	event  platform.MessageEvent
	text   string
	events []platform.MessageEvent
	texts  []string
}

func (f *fakeReplyObserver) ObserveReply(event platform.MessageEvent, text string) error {
	f.event = event
	f.text = text
	f.events = append(f.events, event)
	f.texts = append(f.texts, text)
	return nil
}

type fakeProcessingObserver struct {
	startedEvent  platform.MessageEvent
	finishedEvent platform.MessageEvent
	started       int
	finished      int
	events        *[]string
}

func (f *fakeProcessingObserver) ProcessingStarted(event platform.MessageEvent) error {
	f.startedEvent = event
	f.started++
	if f.events != nil {
		*f.events = append(*f.events, "started")
	}
	return nil
}

func (f *fakeProcessingObserver) ProcessingFinished(event platform.MessageEvent) error {
	f.finishedEvent = event
	f.finished++
	if f.events != nil {
		*f.events = append(*f.events, "finished")
	}
	return nil
}

func TestTrimForChat(t *testing.T) {
	got := trimForChat("abcdef", 4)
	if !strings.Contains(got, "[已截断]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestBuildPromptIncludesMessage(t *testing.T) {
	prompt := buildPrompt(inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		UserID:      "U123",
		MessageID:   "1712345678.000100",
		MessageText: "请帮我查看仓库状态",
	}, 1200)

	if !strings.Contains(prompt, "请帮我查看仓库状态") {
		t.Fatalf("prompt did not include message text: %q", prompt)
	}
	if !strings.Contains(prompt, "Slack") {
		t.Fatalf("prompt did not include provider label: %q", prompt)
	}
	if !strings.Contains(prompt, "C123") {
		t.Fatalf("prompt did not include channel id: %q", prompt)
	}
}

func TestBuildPromptIncludesMemoryContext(t *testing.T) {
	prompt := buildPromptWithContext(inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		UserID:      "U123",
		MessageID:   "1712345678.000100",
		MessageText: "continue this thread",
	}, 1200, backendLabel("codex"), "## Slack thread summary\nWe agreed to use two apps.")

	if !strings.Contains(prompt, "可用历史记忆和摘要") {
		t.Fatalf("prompt did not include memory label: %q", prompt)
	}
	if !strings.Contains(prompt, "背景上下文") {
		t.Fatalf("prompt did not describe memory as background context: %q", prompt)
	}
	if !strings.Contains(prompt, "不能覆盖当前用户请求") {
		t.Fatalf("prompt did not say memory cannot override the current request: %q", prompt)
	}
	if !strings.Contains(prompt, "We agreed to use two apps") {
		t.Fatalf("prompt did not include memory context: %q", prompt)
	}
	if !strings.Contains(prompt, "continue this thread") {
		t.Fatalf("prompt did not include message: %q", prompt)
	}
}

func TestCodexBackendExecuteReturnsLastMessageOutput(t *testing.T) {
	tempDir := t.TempDir()
	backend := CodexBackend{}
	result, err := backend.Execute(context.Background(), BackendRequest{
		Entry: inbound.LoggedEvent{
			Provider:    "slack",
			ChannelID:   "C123",
			MessageID:   "1712345678.000100",
			MessageText: "do the thing",
		},
		Prompt:         "prompt text",
		Workspace:      t.TempDir(),
		Binary:         fakeCodexExecutable(t),
		ResultMaxChars: 100,
		TempDir:        tempDir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Text != "codex final output" {
		t.Fatalf("result.Text = %q", result.Text)
	}
	if result.SessionID != "test-session-001" {
		t.Fatalf("result.SessionID = %q, want test-session-001", result.SessionID)
	}
}

func TestResolveBackendSelectsAgy(t *testing.T) {
	backend, err := resolveBackend(Config{Backend: "agy"})
	if err != nil {
		t.Fatalf("resolveBackend: %v", err)
	}
	if backend.Name() != "agy" {
		t.Fatalf("backend = %q, want agy", backend.Name())
	}
}

func TestRunnerRepliesThroughMessenger(t *testing.T) {
	messenger := &fakeMessenger{}
	runner := NewRunnerWithMessenger(Config{Enabled: true, ResultMaxChars: 4}, nil, messenger)
	entry := inbound.LoggedEvent{
		Provider:  "slack",
		ChannelID: "C123",
		ThreadID:  "1712345678.000100",
		MessageID: "1712345678.000100",
		UserID:    "U123",
	}

	if err := runner.replyObserved(entry, "abcdef"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if messenger.event.ChannelID != "C123" {
		t.Fatalf("unexpected reply event: %#v", messenger.event)
	}
	if !strings.Contains(messenger.text, "[已截断]") {
		t.Fatalf("expected trimmed reply, got %q", messenger.text)
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

	if err := runner.replyObserved(entry, "done"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if observer.event.ChannelID != "C123" || observer.text != "done" {
		t.Fatalf("observer = %+v text=%q", observer.event, observer.text)
	}
}

func TestRunnerReplyObserverReceivesTrimmedText(t *testing.T) {
	messenger := &fakeMessenger{}
	observer := &fakeReplyObserver{}
	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		ResultMaxChars: 4,
		ReplyObserver:  observer,
	}, nil, messenger)
	entry := inbound.LoggedEvent{
		Provider:  "slack",
		ChannelID: "C123",
		ThreadID:  "1712345678.000100",
		MessageID: "1712345678.000100",
		UserID:    "U123",
	}

	if err := runner.replyObserved(entry, "abcdef"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if observer.text != messenger.text {
		t.Fatalf("observer text = %q, messenger text = %q", observer.text, messenger.text)
	}
	if !strings.Contains(observer.text, "[已截断]") {
		t.Fatalf("expected observer to receive trimmed text, got %q", observer.text)
	}
}

func TestRunnerRunDoesNotObserveAck(t *testing.T) {
	messenger := &fakeMessenger{}
	observer := &fakeReplyObserver{}
	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		CodexBinary:    fakeCodexExecutable(t),
		Workspace:      t.TempDir(),
		AckText:        "working",
		ResultMaxChars: 100,
		Timeout:        time.Second,
		ReplyObserver:  observer,
	}, nil, messenger)
	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345678.000100",
		UserID:      "U123",
		MessageText: "do the thing",
	}

	runner.run(entry)

	if len(messenger.texts) != 2 {
		t.Fatalf("reply count = %d, texts = %#v", len(messenger.texts), messenger.texts)
	}
	if messenger.texts[0] != "working" {
		t.Fatalf("ack text = %q", messenger.texts[0])
	}
	if len(observer.texts) != 1 {
		t.Fatalf("observer texts = %#v, want final output only", observer.texts)
	}
	if observer.texts[0] != "codex final output" {
		t.Fatalf("observer text = %q, want final output only", observer.texts[0])
	}
}

func TestRunnerRunSkipsBlankAck(t *testing.T) {
	var events []string
	messenger := &fakeMessenger{sequence: &events}
	observer := &fakeProcessingObserver{events: &events}
	runner := NewRunnerWithMessenger(Config{
		Enabled:            true,
		CodexBinary:        fakeCodexExecutable(t),
		Workspace:          t.TempDir(),
		AckText:            "   ",
		ResultMaxChars:     100,
		Timeout:            time.Second,
		ProcessingObserver: observer,
	}, nil, messenger)
	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345678.000100",
		UserID:      "U123",
		MessageText: "do the thing",
	}

	runner.run(entry)

	want := []string{"started", "reply:codex final output", "finished"}
	if strings.Join(events, "|") != strings.Join(want, "|") {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	if len(messenger.texts) != 1 {
		t.Fatalf("reply count = %d, texts = %#v", len(messenger.texts), messenger.texts)
	}
	if observer.started != 1 || observer.finished != 1 {
		t.Fatalf("started=%d finished=%d", observer.started, observer.finished)
	}
}

func TestRunnerProcessingObserver(t *testing.T) {
	t.Run("starts before replies complete and finishes after execution", func(t *testing.T) {
		var events []string
		messenger := &fakeMessenger{sequence: &events}
		observer := &fakeProcessingObserver{events: &events}
		runner := NewRunnerWithMessenger(Config{
			Enabled:            true,
			CodexBinary:        fakeCodexExecutable(t),
			Workspace:          t.TempDir(),
			AckText:            "working",
			ResultMaxChars:     100,
			Timeout:            time.Second,
			ProcessingObserver: observer,
		}, nil, messenger)
		entry := inbound.LoggedEvent{
			Provider:    "slack",
			ChannelID:   "C123",
			ThreadID:    "1712345678.000100",
			MessageID:   "1712345678.000100",
			UserID:      "U123",
			MessageText: "do the thing",
		}

		runner.run(entry)

		want := []string{"started", "reply:working", "reply:codex final output", "finished"}
		if strings.Join(events, "|") != strings.Join(want, "|") {
			t.Fatalf("events = %#v, want %#v", events, want)
		}
		if observer.started != 1 || observer.finished != 1 {
			t.Fatalf("started=%d finished=%d", observer.started, observer.finished)
		}
		if observer.startedEvent.MessageID != entry.MessageID || observer.finishedEvent.MessageID != entry.MessageID {
			t.Fatalf("observer events = started:%+v finished:%+v", observer.startedEvent, observer.finishedEvent)
		}
	})

	t.Run("finishes when execute fails", func(t *testing.T) {
		observer := &fakeProcessingObserver{}
		messenger := &fakeMessenger{}
		runner := NewRunnerWithMessenger(Config{
			Enabled:            true,
			CodexBinary:        failingCodexExecutable(t),
			Workspace:          t.TempDir(),
			AckText:            "working",
			ResultMaxChars:     100,
			Timeout:            time.Second,
			ProcessingObserver: observer,
		}, nil, messenger)
		entry := inbound.LoggedEvent{
			Provider:    "slack",
			ChannelID:   "C123",
			ThreadID:    "1712345678.000100",
			MessageID:   "1712345678.000100",
			UserID:      "U123",
			MessageText: "do the thing",
		}

		runner.run(entry)

		if observer.started != 1 || observer.finished != 1 {
			t.Fatalf("started=%d finished=%d", observer.started, observer.finished)
		}
		if len(messenger.texts) != 2 {
			t.Fatalf("reply count = %d, texts = %#v", len(messenger.texts), messenger.texts)
		}
		if !strings.Contains(messenger.texts[1], "simulated codex failure") {
			t.Fatalf("failure reply = %q", messenger.texts[1])
		}
	})
}

func TestRunnerExecuteUsesContextProviderInPrompt(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "prompt.txt")
	provider := &fakeContextProvider{context: "Memory says this thread chose SQLite."}
	runner := NewRunnerWithMessenger(Config{
		CodexBinary:     fakeCodexExecutable(t),
		Workspace:       t.TempDir(),
		ResultMaxChars:  100,
		Timeout:         time.Second,
		ContextProvider: provider,
	}, nil, &fakeMessenger{})
	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345678.000100",
		UserID:      "U123",
		MessageText: "continue implementation",
	}
	t.Setenv("FAKE_CODEX_CAPTURE_PROMPT", capturePath)

	result, err := runner.execute(entry)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if result != "codex final output" {
		t.Fatalf("result = %q", result)
	}
	if !provider.called || provider.seenEntry.MessageID != entry.MessageID || provider.seenEntry.ChannelID != entry.ChannelID {
		t.Fatalf("provider called=%v seenEntry=%+v", provider.called, provider.seenEntry)
	}
	promptData, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("ReadFile(capturePath): %v", err)
	}
	prompt := string(promptData)
	if !strings.Contains(prompt, "Memory says this thread chose SQLite.") {
		t.Fatalf("prompt did not include provider context: %q", prompt)
	}
	if !strings.Contains(prompt, "continue implementation") {
		t.Fatalf("prompt did not include user message: %q", prompt)
	}
}

func TestRunnerExecuteContinuesWhenContextProviderErrors(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "prompt.txt")
	provider := &fakeContextProvider{context: "do not include this", err: errors.New("memory unavailable")}
	runner := NewRunnerWithMessenger(Config{
		CodexBinary:     fakeCodexExecutable(t),
		Workspace:       t.TempDir(),
		ResultMaxChars:  100,
		Timeout:         time.Second,
		ContextProvider: provider,
	}, nil, &fakeMessenger{})
	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345678.000100",
		UserID:      "U123",
		MessageText: "run despite memory failure",
	}
	t.Setenv("FAKE_CODEX_CAPTURE_PROMPT", capturePath)

	result, err := runner.execute(entry)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if result != "codex final output" {
		t.Fatalf("result = %q", result)
	}
	if provider.callCount != 1 {
		t.Fatalf("provider call count = %d", provider.callCount)
	}
	promptData, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("ReadFile(capturePath): %v", err)
	}
	prompt := string(promptData)
	if strings.Contains(prompt, "do not include this") {
		t.Fatalf("prompt included context from errored provider: %q", prompt)
	}
	if !strings.Contains(prompt, "run despite memory failure") {
		t.Fatalf("prompt did not include user message: %q", prompt)
	}
}

func fakeCodexExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codex")
	script := `#!/bin/sh
capture="${FAKE_CODEX_CAPTURE_PROMPT:-}"
output=""
prev=""
for arg in "$@"; do
	if [ "$prev" = "--output-last-message" ]; then
		output="$arg"
	fi
	prev="$arg"
done
prompt="${arg}"
if [ -n "$capture" ]; then
	printf '%s' "$prompt" > "$capture"
fi
if [ -z "$output" ]; then
	echo "missing output file" >&2
	exit 2
fi
echo '{"type":"thread.started","thread_id":"test-session-001"}'
printf '%s' "codex final output" > "$output"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake codex): %v", err)
	}
	return path
}

func failingCodexExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "failing-codex")
	script := `#!/bin/sh
echo "simulated codex failure" >&2
exit 7
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(failing codex): %v", err)
	}
	return path
}

func fakeCodexResumeExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codex-resume")
	script := `#!/bin/sh
capture="${FAKE_CODEX_CAPTURE_PROMPT:-}"
output=""
prev=""
is_resume=""
for arg in "$@"; do
	if [ "$prev" = "--output-last-message" ]; then
		output="$arg"
	fi
	if [ "$arg" = "resume" ]; then
		is_resume="yes"
	fi
	prev="$arg"
done
prompt="${arg}"
if [ -n "$capture" ]; then
	printf '%s' "$prompt" > "$capture"
fi
if [ -z "$output" ]; then
	echo "missing output file" >&2
	exit 2
fi
echo '{"type":"thread.started","thread_id":"test-session-001"}'
if [ "$is_resume" = "yes" ]; then
	printf '%s' "resumed output" > "$output"
else
	printf '%s' "new session output" > "$output"
fi
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake codex resume): %v", err)
	}
	return path
}

func TestCodexBackendResumePassesSessionID(t *testing.T) {
	tempDir := t.TempDir()
	backend := CodexBackend{}
	result, err := backend.Execute(context.Background(), BackendRequest{
		Entry: inbound.LoggedEvent{
			Provider:    "slack",
			ChannelID:   "C123",
			MessageID:   "1712345678.000100",
			MessageText: "follow up",
		},
		Prompt:         "follow up",
		Workspace:      t.TempDir(),
		Binary:         fakeCodexResumeExecutable(t),
		ResultMaxChars: 100,
		TempDir:        tempDir,
		SessionID:      "existing-session-id",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Text != "resumed output" {
		t.Fatalf("result.Text = %q, want 'resumed output'", result.Text)
	}
}

func TestRunnerSessionResumeFlow(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSessionStore(filepath.Join(sessionDir, "sessions.json"))
	binary := fakeCodexResumeExecutable(t)
	messenger := &fakeMessenger{}

	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		CodexBinary:    binary,
		Workspace:      t.TempDir(),
		ResultMaxChars: 100,
		Timeout:        5 * time.Second,
		SessionResume:  true,
		SessionStore:   store,
	}, nil, messenger)

	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345678.000100",
		UserID:      "U123",
		MessageText: "first message",
	}

	result, err := runner.execute(entry)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if result != "new session output" {
		t.Fatalf("first result = %q, want 'new session output'", result)
	}

	key := sessionKeyFromEntry(entry)
	id, _ := store.Lookup(key)
	if id == "" {
		t.Fatal("session not stored after first execution")
	}

	entry2 := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345679.000200",
		UserID:      "U123",
		MessageText: "follow up",
	}

	capturePath := filepath.Join(t.TempDir(), "prompt2.txt")
	t.Setenv("FAKE_CODEX_CAPTURE_PROMPT", capturePath)

	result2, err := runner.execute(entry2)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if result2 != "resumed output" {
		t.Fatalf("second result = %q, want 'resumed output'", result2)
	}

	promptData, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("ReadFile(capturePath): %v", err)
	}
	prompt := string(promptData)
	if prompt != "follow up" {
		t.Fatalf("resume prompt = %q, want raw user message 'follow up'", prompt)
	}
}

func TestRunnerSessionResumeFallback(t *testing.T) {
	sessionDir := t.TempDir()
	store := NewSessionStore(filepath.Join(sessionDir, "sessions.json"))
	key := SessionKey{Provider: "slack", ChannelID: "C123", ThreadTS: "1712345678.000100"}
	store.Put(key, "codex", "stale-session-id")

	scriptDir := t.TempDir()
	callCount := filepath.Join(scriptDir, "call-count")
	os.WriteFile(callCount, []byte("0"), 0o644)

	path := filepath.Join(scriptDir, "fake-codex-fallback")
	script := `#!/bin/sh
output=""
prev=""
is_resume=""
for arg in "$@"; do
	if [ "$prev" = "--output-last-message" ]; then
		output="$arg"
	fi
	if [ "$arg" = "resume" ]; then
		is_resume="yes"
	fi
	prev="$arg"
done
count=$(cat "` + callCount + `")
count=$((count + 1))
printf '%s' "$count" > "` + callCount + `"
if [ "$is_resume" = "yes" ]; then
	echo "resume failed" >&2
	exit 1
fi
echo '{"type":"thread.started","thread_id":"new-session-after-fallback"}'
printf '%s' "fallback output" > "$output"
`
	os.WriteFile(path, []byte(script), 0o755)

	messenger := &fakeMessenger{}
	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		CodexBinary:    path,
		Workspace:      t.TempDir(),
		ResultMaxChars: 100,
		Timeout:        5 * time.Second,
		SessionResume:  true,
		SessionStore:   store,
	}, nil, messenger)

	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C123",
		ThreadID:    "1712345678.000100",
		MessageID:   "1712345679.000200",
		UserID:      "U123",
		MessageText: "after stale session",
	}

	result, err := runner.execute(entry)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result != "fallback output" {
		t.Fatalf("result = %q, want 'fallback output'", result)
	}

	countData, _ := os.ReadFile(callCount)
	if string(countData) != "2" {
		t.Fatalf("call count = %q, want 2 (resume + fallback)", string(countData))
	}

	id, _ := store.Lookup(key)
	if id != "new-session-after-fallback" {
		t.Fatalf("session after fallback = %q, want 'new-session-after-fallback'", id)
	}
}

func TestSessionKeyFromEntry(t *testing.T) {
	entry := inbound.LoggedEvent{
		Provider:  "slack",
		ChannelID: "C123",
		ThreadID:  "1712345678.000100",
		MessageID: "1712345679.000200",
	}
	key := sessionKeyFromEntry(entry)
	if key.Provider != "slack" || key.ChannelID != "C123" || key.ThreadTS != "1712345678.000100" {
		t.Fatalf("key = %+v", key)
	}

	entryNoThread := inbound.LoggedEvent{
		Provider:  "slack",
		ChannelID: "C123",
		MessageID: "1712345679.000200",
	}
	key2 := sessionKeyFromEntry(entryNoThread)
	if key2.ThreadTS != "1712345679.000200" {
		t.Fatalf("key.ThreadTS = %q, want message_id as fallback", key2.ThreadTS)
	}
}

func TestRunnerDirectivePinsBackendForThread(t *testing.T) {
	dir := t.TempDir()
	agyPath := filepath.Join(dir, "agy")
	if err := os.WriteFile(agyPath, []byte("#!/bin/sh\nprintf '%s' 'from-agy'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(agy): %v", err)
	}
	codexPath := filepath.Join(dir, "codex")
	if err := os.WriteFile(codexPath, []byte("#!/bin/sh\necho 'codex-should-not-run' >&2\nexit 99\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(codex): %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		Backend:        "codex",
		CodexBinary:    codexPath,
		Workspace:      t.TempDir(),
		ResultMaxChars: 200,
		Timeout:        5 * time.Second,
		SessionResume:  true,
		SessionStore:   store,
	}, nil, &fakeMessenger{})

	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C1",
		ThreadID:    "1.0",
		MessageID:   "1.0",
		UserID:      "U1",
		MessageText: "/agy first task",
	}
	out, err := runner.execute(entry)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "from-agy" {
		t.Fatalf("out = %q", out)
	}
	rec, ok, _ := store.LookupRecord(sessionKeyFromEntry(entry))
	if !ok || rec.Backend != "agy" {
		t.Fatalf("pin = %+v ok=%v", rec, ok)
	}

	entry2 := entry
	entry2.MessageID = "2.0"
	entry2.MessageText = "second without directive"
	out2, err := runner.execute(entry2)
	if err != nil {
		t.Fatalf("execute2: %v", err)
	}
	if out2 != "from-agy" {
		t.Fatalf("sticky out = %q", out2)
	}
}

func TestRunnerBackendSwitchClearsSessionID(t *testing.T) {
	dir := t.TempDir()
	agyPath := filepath.Join(dir, "agy")
	if err := os.WriteFile(agyPath, []byte("#!/bin/sh\nprintf '%s' 'from-agy'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(agy): %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	codexBin := fakeCodexResumeExecutable(t)
	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		Backend:        "codex",
		CodexBinary:    codexBin,
		Workspace:      t.TempDir(),
		ResultMaxChars: 200,
		Timeout:        5 * time.Second,
		SessionResume:  true,
		SessionStore:   store,
	}, nil, &fakeMessenger{})

	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C1",
		ThreadID:    "9.0",
		MessageID:   "9.0",
		UserID:      "U1",
		MessageText: "start on codex",
	}
	if _, err := runner.execute(entry); err != nil {
		t.Fatalf("codex execute: %v", err)
	}
	rec, ok, _ := store.LookupRecord(sessionKeyFromEntry(entry))
	if !ok || rec.SessionID == "" || rec.Backend != "codex" {
		t.Fatalf("after codex: %+v ok=%v", rec, ok)
	}

	entry2 := entry
	entry2.MessageID = "9.1"
	entry2.MessageText = "/agy switch please"
	out, err := runner.execute(entry2)
	if err != nil {
		t.Fatalf("switch execute: %v", err)
	}
	if out != "from-agy" {
		t.Fatalf("out = %q", out)
	}
	rec2, ok, _ := store.LookupRecord(sessionKeyFromEntry(entry2))
	if !ok || rec2.Backend != "agy" {
		t.Fatalf("after switch: %+v ok=%v", rec2, ok)
	}
	if rec2.SessionID != "" {
		t.Fatalf("session id should be cleared on backend switch, got %q", rec2.SessionID)
	}
}

func TestRunnerUnrecognizedDirectivePassthrough(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	capturePath := filepath.Join(t.TempDir(), "prompt.txt")
	t.Setenv("FAKE_CODEX_CAPTURE_PROMPT", capturePath)

	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		Backend:        "codex",
		CodexBinary:    fakeCodexResumeExecutable(t),
		Workspace:      t.TempDir(),
		ResultMaxChars: 200,
		Timeout:        5 * time.Second,
		SessionResume:  true,
		SessionStore:   store,
	}, nil, &fakeMessenger{})

	entry := inbound.LoggedEvent{
		Provider:    "slack",
		ChannelID:   "C1",
		ThreadID:    "3.0",
		MessageID:   "3.0",
		UserID:      "U1",
		MessageText: "/not-a-backend please help",
	}
	if _, err := runner.execute(entry); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "/not-a-backend please help") {
		t.Fatalf("prompt missing literal slash text: %q", data)
	}
	rec, ok, _ := store.LookupRecord(sessionKeyFromEntry(entry))
	if !ok || rec.Backend != "codex" {
		t.Fatalf("should pin default codex, got %+v ok=%v", rec, ok)
	}
}

func TestValidateDefaultBackend(t *testing.T) {
	if err := ValidateDefaultBackend(""); err != nil {
		t.Fatalf("empty should default to codex: %v", err)
	}
	if err := ValidateDefaultBackend("grok"); err != nil {
		t.Fatalf("grok should be valid: %v", err)
	}
	if err := ValidateDefaultBackend("nope"); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
