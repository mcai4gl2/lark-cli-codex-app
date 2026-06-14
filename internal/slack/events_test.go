package slack

import (
	"testing"
	"time"
)

func TestNormalizeAppMentionStripsBotMentionAndSetsThread(t *testing.T) {
	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev123",
		"event":{
			"type":"app_mention",
			"user":"U234",
			"channel":"C345",
			"text":"please <@U999> run tests",
			"ts":"1710000000.000100"
		}
	}`)

	event, ok, err := NormalizeEvent(payload, "U999")
	if err != nil {
		t.Fatalf("NormalizeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("NormalizeEvent() ok = false")
	}
	if event.Provider != "slack" || event.TeamID != "T123" || event.EventID != "Ev123" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.EventType != "app_mention" || event.ChannelID != "C345" || event.ChannelType != "channel" {
		t.Fatalf("event routing = %+v", event)
	}
	if event.MessageID != "1710000000.000100" || event.ThreadID != "1710000000.000100" {
		t.Fatalf("thread selection = %+v", event)
	}
	if event.UserID != "U234" || event.BotID != "U999" {
		t.Fatalf("users = %+v", event)
	}
	if event.MessageText != "please run tests" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
}

func TestNormalizeDirectMessagePreservesThreadTS(t *testing.T) {
	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev124",
		"event":{
			"type":"message",
			"channel_type":"im",
			"user":"U234",
			"channel":"D345",
			"text":"continue",
			"ts":"1710000000.000200",
			"thread_ts":"1710000000.000100"
		}
	}`)

	event, ok, err := NormalizeEvent(payload, "U999")
	if err != nil {
		t.Fatalf("NormalizeEvent() error = %v", err)
	}
	if !ok {
		t.Fatalf("NormalizeEvent() ok = false")
	}
	if event.EventType != "message" || event.ChannelType != "im" {
		t.Fatalf("event type = %+v", event)
	}
	if event.MessageText != "continue" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
	if event.ThreadID != "1710000000.000100" {
		t.Fatalf("ThreadID = %q", event.ThreadID)
	}
}

func TestNormalizeEventIgnoresBotsSubtypesAndUnsupportedMessages(t *testing.T) {
	tests := map[string]string{
		"bot user":                `{"event":{"type":"message","channel_type":"im","user":"U999","text":"self","ts":"1"}}`,
		"bot id":                  `{"event":{"type":"message","channel_type":"im","bot_id":"B123","text":"bot","ts":"1"}}`,
		"subtype":                 `{"event":{"type":"message","channel_type":"im","subtype":"message_changed","user":"U1","text":"edit","ts":"1"}}`,
		"channel without mention": `{"event":{"type":"message","channel_type":"channel","user":"U1","text":"hello","ts":"1"}}`,
	}

	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, ok, err := NormalizeEvent([]byte(payload), "U999")
			if err != nil {
				t.Fatalf("NormalizeEvent() error = %v", err)
			}
			if ok {
				t.Fatalf("NormalizeEvent() ok = true")
			}
		})
	}
}

func TestNormalizeEventRequiresText(t *testing.T) {
	payload := []byte(`{"event":{"type":"message","channel_type":"im","user":"U1","text":"   ","ts":"1"}}`)
	_, ok, err := NormalizeEvent(payload, "U999")
	if err != nil {
		t.Fatalf("NormalizeEvent() error = %v", err)
	}
	if ok {
		t.Fatalf("NormalizeEvent() ok = true")
	}
}

func TestNormalizeThreadMessageForCatchUpParticipatingThreadPlainReply(t *testing.T) {
	event, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "", "1710000000.000100", Message{
		Type:     "message",
		User:     "U234",
		Text:     "plain reply",
		TS:       "1710000000.000200",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeThread)
	if !ok {
		t.Fatalf("NormalizeThreadMessageForCatchUp() ok = false")
	}

	if event.Provider != "slack" || event.TeamID != "T123" || event.ChannelID != "C345" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.EventType != "message" || event.ChannelType != "channel" {
		t.Fatalf("event routing = %+v", event)
	}
	if event.ThreadID != "1710000000.000100" || event.MessageID != "1710000000.000200" {
		t.Fatalf("thread selection = %+v", event)
	}
	if event.UserID != "U234" || event.BotID != "U999" {
		t.Fatalf("users = %+v", event)
	}
	if event.MessageType != "text" || event.MessageText != "plain reply" || event.RawContent != "plain reply" {
		t.Fatalf("message content = %+v", event)
	}
	if _, err := time.Parse(time.RFC3339Nano, event.ReceivedAt); err != nil {
		t.Fatalf("ReceivedAt = %q, parse error = %v", event.ReceivedAt, err)
	}
}

func TestNormalizeThreadMessageForCatchUpRecoverModeOffSkips(t *testing.T) {
	_, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", "1710000000.000100", Message{
		Type:     "message",
		User:     "U234",
		Text:     "plain reply",
		TS:       "1710000000.000200",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeOff)
	if ok {
		t.Fatalf("NormalizeThreadMessageForCatchUp() ok = true")
	}
}

func TestNormalizeThreadMessageForCatchUpMentionDMSkipsPlainChannelReply(t *testing.T) {
	_, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", "1710000000.000100", Message{
		Type:     "message",
		User:     "U234",
		Text:     "plain reply",
		TS:       "1710000000.000200",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeMentionDM)
	if ok {
		t.Fatalf("NormalizeThreadMessageForCatchUp() ok = true")
	}
}

func TestNormalizeThreadMessageForCatchUpMentionDMProcessesChannelMention(t *testing.T) {
	event, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", "1710000000.000100", Message{
		Type:     "message",
		User:     "U234",
		Text:     "<@U999> catch up please",
		TS:       "1710000000.000200",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeMentionDM)
	if !ok {
		t.Fatalf("NormalizeThreadMessageForCatchUp() ok = false")
	}
	if event.MessageText != "catch up please" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
	if event.RawContent != "<@U999> catch up please" {
		t.Fatalf("RawContent = %q", event.RawContent)
	}
}

func TestNormalizeThreadMessageForCatchUpMentionDMProcessesDirectMessageWithoutMention(t *testing.T) {
	event, ok := NormalizeThreadMessageForCatchUp("T123", "D345", "", "1710000000.000100", Message{
		User:     "U234",
		Text:     "dm catch up",
		TS:       "1710000000.000200",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeMentionDM)
	if !ok {
		t.Fatalf("NormalizeThreadMessageForCatchUp() ok = false")
	}
	if event.ChannelType != "im" {
		t.Fatalf("ChannelType = %q", event.ChannelType)
	}
	if event.MessageText != "dm catch up" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
}

func TestNormalizeThreadMessageForCatchUpMentionDMStripsDirectMessageMention(t *testing.T) {
	event, ok := NormalizeThreadMessageForCatchUp("T123", "D345", "", "1710000000.000100", Message{
		User:     "U234",
		Text:     "<@U999> dm catch up",
		TS:       "1710000000.000200",
		ThreadTS: "1710000000.000100",
	}, "U999", RecoverModeMentionDM)
	if !ok {
		t.Fatalf("NormalizeThreadMessageForCatchUp() ok = false")
	}
	if event.ChannelType != "im" {
		t.Fatalf("ChannelType = %q", event.ChannelType)
	}
	if event.MessageText != "dm catch up" {
		t.Fatalf("MessageText = %q", event.MessageText)
	}
	if event.RawContent != "<@U999> dm catch up" {
		t.Fatalf("RawContent = %q", event.RawContent)
	}
}

func TestNormalizeThreadMessageForCatchUpThreadIDFallbacks(t *testing.T) {
	tests := map[string]struct {
		threadTS string
		message  Message
		want     string
	}{
		"provided thread ts": {
			threadTS: "1710000000.000100",
			message: Message{
				User: "U234",
				Text: "root message from requested thread",
				TS:   "1710000000.000100",
			},
			want: "1710000000.000100",
		},
		"message ts": {
			message: Message{
				User: "U234",
				Text: "root message without requested thread",
				TS:   "1710000000.000300",
			},
			want: "1710000000.000300",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			event, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", tt.threadTS, tt.message, "U999", RecoverModeThread)
			if !ok {
				t.Fatalf("NormalizeThreadMessageForCatchUp() ok = false")
			}
			if event.ThreadID != tt.want {
				t.Fatalf("ThreadID = %q, want %q", event.ThreadID, tt.want)
			}
		})
	}
}

func TestNormalizeThreadMessageForCatchUpRequiresRequestedThreadMembership(t *testing.T) {
	tests := map[string]Message{
		"reply from different thread": {
			User:     "U234",
			Text:     "wrong thread reply",
			TS:       "1710000000.000200",
			ThreadTS: "1710000000.000999",
		},
		"root from different thread": {
			User: "U234",
			Text: "wrong root",
			TS:   "1710000000.000999",
		},
	}

	for name, message := range tests {
		t.Run(name, func(t *testing.T) {
			_, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", "1710000000.000100", message, "U999", RecoverModeThread)
			if ok {
				t.Fatalf("NormalizeThreadMessageForCatchUp() ok = true")
			}
		})
	}
}

func TestNormalizeThreadMessageForCatchUpSkipsBotsSubtypesAndBlankText(t *testing.T) {
	tests := map[string]Message{
		"bot id":           {Type: "message", BotID: "B123", User: "U234", Text: "bot", TS: "1710000000.000200"},
		"bot user":         {Type: "message", User: "U999", Text: "self", TS: "1710000000.000200"},
		"subtype":          {Type: "message", Subtype: "message_changed", User: "U234", Text: "edit", TS: "1710000000.000200"},
		"unsupported type": {Type: "file_share", User: "U234", Text: "file", TS: "1710000000.000200"},
		"blank text":       {Type: "message", User: "U234", Text: "   ", TS: "1710000000.000200"},
		"blank strip":      {Type: "message", User: "U234", Text: "<@U999>", TS: "1710000000.000200"},
	}

	for name, message := range tests {
		t.Run(name, func(t *testing.T) {
			_, ok := NormalizeThreadMessageForCatchUp("T123", "C345", "channel", "1710000000.000100", message, "U999", RecoverModeMentionDM)
			if ok {
				t.Fatalf("NormalizeThreadMessageForCatchUp() ok = true")
			}
		})
	}
}
