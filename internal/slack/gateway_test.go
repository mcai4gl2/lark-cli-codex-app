package slack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yjwong/lark-cli/internal/platform"
)

type captureMessenger struct {
	mu      sync.Mutex
	replies []string
	events  []platform.MessageEvent
}

func (m *captureMessenger) Reply(_ context.Context, event platform.MessageEvent, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	m.replies = append(m.replies, text)
	return nil
}

func (m *captureMessenger) Send(_ context.Context, _ platform.MessageTarget, _ string) error {
	return nil
}

func TestServiceHandleEventQueuesDesktopRequest(t *testing.T) {
	messenger := &captureMessenger{}
	service := NewGateway(Config{
		EventLogPath: t.TempDir() + "/events.jsonl",
		BotUserID:    "U999",
		Messenger:    messenger,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev123",
		"event":{
			"type":"message",
			"channel_type":"im",
			"user":"U234",
			"channel":"D345",
			"text":"/gui open https://openai.com",
			"ts":"1710000000.000200"
		}
	}`)
	if err := service.handleEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}

	if len(messenger.replies) != 1 {
		t.Fatalf("reply count = %d", len(messenger.replies))
	}
	if messenger.events[0].Provider != "slack" || messenger.events[0].ThreadID != "1710000000.000200" {
		t.Fatalf("reply event = %+v", messenger.events[0])
	}
}

func TestServiceHandleEventWritesSlackMemory(t *testing.T) {
	memoryRoot := t.TempDir()
	service := NewGateway(Config{
		EventLogPath:  filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:     "U999",
		Messenger:     &captureMessenger{},
		MemoryEnabled: true,
		MemoryRoot:    memoryRoot,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev123",
		"event":{
			"type":"app_mention",
			"user":"U234",
			"channel":"C345",
			"text":"<@U999> remember this",
			"ts":"1710000000.000300",
			"thread_ts":"1710000000.000100"
		}
	}`)
	if err := service.handleEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}

	threadLog := filepath.Join(memoryRoot, "T123", "C345", "threads", "1710000000.000100", "events.jsonl")
	data, err := os.ReadFile(threadLog)
	if err != nil {
		t.Fatalf("ReadFile(threadLog): %v", err)
	}
	record := string(data)
	if !strings.Contains(record, `"direction":"inbound"`) || !strings.Contains(record, `"text":"remember this"`) {
		t.Fatalf("thread memory record = %q", record)
	}

	dailyEntries, err := filepath.Glob(filepath.Join(memoryRoot, "T123", "C345", "daily", "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob(daily): %v", err)
	}
	if len(dailyEntries) != 1 {
		t.Fatalf("daily entry count = %d, entries = %#v", len(dailyEntries), dailyEntries)
	}
}

func TestNewGatewayWiresSlackMemoryIntoAgentConfig(t *testing.T) {
	memoryRoot := t.TempDir()
	threadDir := filepath.Join(memoryRoot, "T123", "C345", "threads", "1710000000.000100")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(threadDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "memory.md"), []byte("thread note that should be truncated"), 0o600); err != nil {
		t.Fatalf("WriteFile(thread memory): %v", err)
	}

	service := NewGateway(Config{
		EventLogPath:          filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:             "U999",
		Messenger:             &captureMessenger{},
		MemoryEnabled:         true,
		MemoryRoot:            memoryRoot,
		MemoryMaxSectionChars: 11,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	entry := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C345",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000300",
		MessageText: "hello",
	}
	if service.cfg.Agent.ContextProvider == nil {
		t.Fatal("ContextProvider is nil")
	}
	contextText, err := service.cfg.Agent.ContextProvider.PromptContext(entry)
	if err != nil {
		t.Fatalf("PromptContext() error = %v", err)
	}
	if !strings.Contains(contextText, "thread note") || strings.Contains(contextText, "should be truncated") {
		t.Fatalf("context text = %q", contextText)
	}

	if service.cfg.Agent.ReplyObserver == nil {
		t.Fatal("ReplyObserver is nil")
	}
	if err := service.cfg.Agent.ReplyObserver.ObserveReply(entry, "done"); err != nil {
		t.Fatalf("ObserveReply() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(threadDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(thread events): %v", err)
	}
	if !strings.Contains(string(data), `"direction":"outbound"`) || !strings.Contains(string(data), `"text":"done"`) {
		t.Fatalf("thread events = %q", string(data))
	}
}

func TestMemoryReplyObserverNilStoreNoop(t *testing.T) {
	observer := memoryReplyObserver{}

	if err := observer.ObserveReply(platform.MessageEvent{}, "ignored"); err != nil {
		t.Fatalf("ObserveReply() error = %v", err)
	}
}
