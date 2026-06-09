package platform

import "encoding/json"

// MessageEvent is the provider-neutral event shape persisted by inbound
// handlers and consumed by shared agent/desktop code.
type MessageEvent struct {
	Provider    string          `json:"provider"`
	ReceivedAt  string          `json:"received_at"`
	EventID     string          `json:"event_id,omitempty"`
	EventType   string          `json:"event_type,omitempty"`
	TeamID      string          `json:"team_id,omitempty"`
	ChannelID   string          `json:"channel_id,omitempty"`
	ChannelType string          `json:"channel_type,omitempty"`
	MessageID   string          `json:"message_id,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	UserID      string          `json:"user_id,omitempty"`
	UserName    string          `json:"user_name,omitempty"`
	BotID       string          `json:"bot_id,omitempty"`
	MessageType string          `json:"message_type,omitempty"`
	MessageText string          `json:"message_text,omitempty"`
	RawContent  string          `json:"raw_content,omitempty"`
	RawEvent    json.RawMessage `json:"raw_event,omitempty"`
}
