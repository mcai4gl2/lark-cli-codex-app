package cmd

import (
	"strings"
	"testing"

	"github.com/yjwong/lark-cli/internal/platform"
	"github.com/yjwong/lark-cli/internal/slackmemory"
)

func TestSlackGatewayServeCommandIsRegistered(t *testing.T) {
	slackCommand, _, err := rootCmd.Find([]string{"slack", "gateway", "serve"})
	if err != nil {
		t.Fatalf("Find(slack gateway serve) error = %v", err)
	}
	if slackCommand == nil || slackCommand.Name() != "serve" {
		t.Fatalf("slack gateway serve command not found")
	}
}

func TestGatewayServeHasAgentBackendFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"gateway", "serve"})
	if err != nil {
		t.Fatalf("Find(gateway serve) error = %v", err)
	}
	if cmd.Flags().Lookup("agent-backend") == nil {
		t.Fatal("agent-backend flag is missing")
	}
	if cmd.Flags().Lookup("agent-binary") == nil {
		t.Fatal("agent-binary flag is missing")
	}
}

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

func TestSlackGatewayServeHasAgentBackendFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"slack", "gateway", "serve"})
	if err != nil {
		t.Fatalf("Find(slack gateway serve) error = %v", err)
	}
	if cmd.Flags().Lookup("agent-backend") == nil {
		t.Fatal("agent-backend flag is missing")
	}
	if cmd.Flags().Lookup("agent-binary") == nil {
		t.Fatal("agent-binary flag is missing")
	}
}

func TestSlackMessageCommandsAreRegistered(t *testing.T) {
	for _, args := range [][]string{
		{"slack", "msg", "send"},
		{"slack", "msg", "history"},
		{"slack", "msg", "thread"},
		{"slack", "msg", "react"},
		{"slack", "msg", "react", "list"},
		{"slack", "msg", "react", "remove"},
		{"slack", "memory", "path"},
		{"slack", "memory", "show"},
		{"slack", "memory", "append"},
	} {
		command, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatalf("Find(%v) error = %v", args, err)
		}
		if command == nil || command.Name() != args[len(args)-1] {
			t.Fatalf("%v command not found", args)
		}
	}
}

func TestSlackMemoryPathForScope(t *testing.T) {
	store := slackmemory.NewStore(slackmemory.Config{Root: "/tmp/slack-memory"})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadID:  "1710000000.000100",
		MessageID: "1710000000.000100",
	}

	for _, tc := range []struct {
		name  string
		scope string
		want  string
	}{
		{name: "channel", scope: "channel", want: "/tmp/slack-memory/T123/C123/memory.md"},
		{name: "empty", scope: "", want: "/tmp/slack-memory/T123/C123/memory.md"},
		{name: "thread", scope: "thread", want: "/tmp/slack-memory/T123/C123/threads/1710000000.000100/memory.md"},
		{name: "summary", scope: "summary", want: "/tmp/slack-memory/T123/C123/threads/1710000000.000100/summary.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := slackMemoryPathForScope(store, event, tc.scope)
			if err != nil {
				t.Fatalf("slackMemoryPathForScope() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("slackMemoryPathForScope() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSlackMemoryPathForScopeRejectsInvalidScope(t *testing.T) {
	store := slackmemory.NewStore(slackmemory.Config{Root: "/tmp/slack-memory"})
	_, err := slackMemoryPathForScope(store, platform.MessageEvent{}, "workspace")
	if err == nil {
		t.Fatal("slackMemoryPathForScope() error = nil")
	}
	if !strings.Contains(err.Error(), "--scope") {
		t.Fatalf("slackMemoryPathForScope() error = %v", err)
	}
}

func TestSlackMemoryPathForScopeRequiresThreadTSForThreadScopes(t *testing.T) {
	store := slackmemory.NewStore(slackmemory.Config{Root: "/tmp/slack-memory"})

	for _, scope := range []string{"thread", "summary"} {
		_, err := slackMemoryPathForScope(store, platform.MessageEvent{}, scope)
		if err == nil {
			t.Fatalf("slackMemoryPathForScope(%q) error = nil", scope)
		}
		if !strings.Contains(err.Error(), "--thread-ts") {
			t.Fatalf("slackMemoryPathForScope(%q) error = %v", scope, err)
		}
	}
}
