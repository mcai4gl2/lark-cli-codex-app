package inbound

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLoggedEventExtractsText(t *testing.T) {
	entry := NewLoggedEvent(MessageInput{
		Schema:       "2.0",
		EventType:    "im.message.receive_v1",
		MessageID:    "om_123",
		RootID:       "om_root",
		ChatID:       "oc_123",
		MessageType:  "text",
		SenderOpenID: "ou_123",
		RawContent:   `{"text":"hello inbound"}`,
	})

	if entry.Provider != "lark" {
		t.Fatalf("unexpected provider: %s", entry.Provider)
	}
	if entry.MessageText != "hello inbound" {
		t.Fatalf("unexpected message text: %s", entry.MessageText)
	}
	if entry.MessageID != "om_123" {
		t.Fatalf("unexpected message_id: %s", entry.MessageID)
	}
	if entry.ChannelID != "oc_123" {
		t.Fatalf("unexpected channel_id: %s", entry.ChannelID)
	}
	if entry.ThreadID != "om_root" {
		t.Fatalf("unexpected thread_id: %s", entry.ThreadID)
	}
	if entry.UserID != "ou_123" {
		t.Fatalf("unexpected user_id: %s", entry.UserID)
	}
	if entry.ReceivedAt == "" {
		t.Fatalf("expected received_at to be populated")
	}
}

func TestNewLoggedEventUsesMessageIDAsDefaultThread(t *testing.T) {
	entry := NewLoggedEvent(MessageInput{
		MessageID:  "om_123",
		ChatID:     "oc_123",
		RawContent: `{"text":"hello inbound"}`,
	})

	if entry.ThreadID != "om_123" {
		t.Fatalf("unexpected default thread_id: %s", entry.ThreadID)
	}
}

func TestHandlerProcessWritesJSONL(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	handler := NewHandler(Config{
		EventLogPath: logPath,
	}, log.New(io.Discard, "", 0))

	entry := NewLoggedEvent(MessageInput{
		EventType:   "im.message.receive_v1",
		MessageID:   "om_123",
		MessageType: "text",
		RawContent:  `{"text":"hello inbound"}`,
	})

	if err := handler.Process(entry); err != nil {
		t.Fatalf("process event: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d", len(lines))
	}

	var got LoggedEvent
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal log entry: %v", err)
	}

	if got.MessageText != "hello inbound" {
		t.Fatalf("unexpected message text: %s", got.MessageText)
	}
}
