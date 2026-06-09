package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/yjwong/lark-cli/internal/inbound"
	"github.com/yjwong/lark-cli/internal/platform"
)

type fakeMessenger struct {
	event platform.MessageEvent
	text  string
}

func (f *fakeMessenger) Reply(_ context.Context, event platform.MessageEvent, text string) error {
	f.event = event
	f.text = text
	return nil
}

func (f *fakeMessenger) Send(_ context.Context, _ platform.MessageTarget, _ string) error {
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

	if err := runner.reply(entry, "abcdef"); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if messenger.event.ChannelID != "C123" {
		t.Fatalf("unexpected reply event: %#v", messenger.event)
	}
	if !strings.Contains(messenger.text, "[已截断]") {
		t.Fatalf("expected trimmed reply, got %q", messenger.text)
	}
}
