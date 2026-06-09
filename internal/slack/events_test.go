package slack

import "testing"

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
