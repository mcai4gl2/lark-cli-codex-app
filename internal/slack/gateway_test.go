package slack

import (
	"context"
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
