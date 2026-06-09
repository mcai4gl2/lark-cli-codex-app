package inbound

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yjwong/lark-cli/internal/larkbridge"
	"github.com/yjwong/lark-cli/internal/platform"
)

// Config configures how inbound message events are persisted and handled.
type Config struct {
	EventLogPath  string
	AutoReplyText string
	Messenger     platform.Messenger
}

// MessageInput is a normalized message event shape that can be populated from
// webhook callbacks or WebSocket events.
type MessageInput struct {
	Schema       string
	EventID      string
	EventType    string
	TenantKey    string
	AppID        string
	MessageID    string
	RootID       string
	ParentID     string
	ChatID       string
	ChatType     string
	MessageType  string
	SenderType   string
	SenderOpenID string
	SenderUserID string
	UserName     string
	BotID        string
	RawContent   string
	RawEvent     json.RawMessage
}

// LoggedEvent is the JSONL shape persisted by inbound handlers.
type LoggedEvent = platform.MessageEvent

// Handler persists inbound events and optionally sends auto replies.
type Handler struct {
	cfg       Config
	messenger platform.Messenger
	logger    *log.Logger
	mu        sync.Mutex
}

// NewHandler returns a shared inbound handler.
func NewHandler(cfg Config, logger *log.Logger) *Handler {
	if logger == nil {
		logger = log.New(os.Stderr, "lark-inbound: ", log.LstdFlags)
	}
	return &Handler{
		cfg:       cfg,
		messenger: defaultMessenger(cfg.Messenger),
		logger:    logger,
	}
}

// NewLoggedEvent builds a persisted event from a normalized input.
func NewLoggedEvent(input MessageInput) LoggedEvent {
	provider := "lark"
	threadID := input.RootID
	if threadID == "" {
		threadID = input.MessageID
	}
	userID := input.SenderOpenID
	if userID == "" {
		userID = input.SenderUserID
	}

	return LoggedEvent{
		Provider:    provider,
		ReceivedAt:  time.Now().Format(time.RFC3339Nano),
		EventID:     input.EventID,
		EventType:   input.EventType,
		TeamID:      input.TenantKey,
		ChannelID:   input.ChatID,
		ChannelType: input.ChatType,
		MessageID:   input.MessageID,
		ThreadID:    threadID,
		UserID:      userID,
		UserName:    input.UserName,
		BotID:       input.BotID,
		MessageType: input.MessageType,
		MessageText: ExtractMessageText(input.MessageType, input.RawContent),
		RawContent:  input.RawContent,
		RawEvent:    input.RawEvent,
	}
}

// Process persists the event and optionally sends an auto reply.
func (h *Handler) Process(entry LoggedEvent) error {
	if entry.ReceivedAt == "" {
		entry.ReceivedAt = time.Now().Format(time.RFC3339Nano)
	}

	if err := h.appendEvent(entry); err != nil {
		return err
	}

	h.logger.Printf(
		"received message event provider=%s message_id=%s channel_id=%s user_id=%s",
		entry.Provider,
		entry.MessageID,
		entry.ChannelID,
		entry.UserID,
	)

	if h.cfg.AutoReplyText != "" && ShouldAutoReply(entry) {
		if err := h.autoReply(entry); err != nil {
			h.logger.Printf("auto reply failed for message_id=%s: %v", entry.MessageID, err)
		}
	}

	return nil
}

func (h *Handler) appendEvent(entry LoggedEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(h.cfg.EventLogPath), 0700); err != nil {
		return fmt.Errorf("create event log directory: %w", err)
	}

	file, err := os.OpenFile(h.cfg.EventLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()

	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal event log entry: %w", err)
	}

	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write event log: %w", err)
	}

	return nil
}

func (h *Handler) autoReply(entry LoggedEvent) error {
	reply := RenderReplyTemplate(h.cfg.AutoReplyText, entry)
	return h.messenger.Reply(context.Background(), entry, reply)
}

// ExtractMessageText returns the human-readable text when possible.
func ExtractMessageText(messageType, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}

	if messageType == "text" {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			return payload.Text
		}
	}

	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &generic); err == nil {
		if text, ok := generic["text"].(string); ok {
			return text
		}
	}

	return raw
}

// ShouldAutoReply limits auto replies to user-originated messages with IDs.
func ShouldAutoReply(entry LoggedEvent) bool {
	if entry.MessageID == "" {
		return false
	}
	return true
}

// RenderReplyTemplate fills supported placeholders for auto replies.
func RenderReplyTemplate(template string, entry LoggedEvent) string {
	replacer := strings.NewReplacer(
		"{{text}}", entry.MessageText,
		"{{message_id}}", entry.MessageID,
		"{{chat_id}}", entry.ChannelID,
		"{{channel_id}}", entry.ChannelID,
		"{{thread_id}}", entry.ThreadID,
		"{{sender_open_id}}", entry.UserID,
		"{{sender_user_id}}", entry.UserID,
		"{{user_id}}", entry.UserID,
	)
	return replacer.Replace(template)
}

func defaultMessenger(messenger platform.Messenger) platform.Messenger {
	if messenger != nil {
		return messenger
	}
	return larkbridge.NewMessenger(nil)
}
