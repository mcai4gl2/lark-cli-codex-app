package weixin

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/platform"
)

func newCommandContext(t *testing.T) (CommandContext, *[]string) {
	t.Helper()
	replies := &[]string{}
	event := platform.MessageEvent{
		Provider:  Provider,
		ChannelID: "u1",
		ThreadID:  "s-1",
		UserID:    "u1",
		MessageID: "1001",
	}
	return CommandContext{
		Event: event,
		Reply: func(ctx context.Context, text string) error {
			*replies = append(*replies, text)
			return nil
		},
		SessionStore:   agent.NewSessionStore(filepath.Join(t.TempDir(), "sessions.json")),
		DefaultBackend: "codex",
		Workspace:      "/work",
		AgentEnabled:   true,
	}, replies
}

func TestHandleCommandIgnoresNonCommands(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)
	for _, text := range []string{"hello", "  ", "not /a command"} {
		handled, err := HandleCommand(context.Background(), text, cmdCtx)
		if err != nil {
			t.Fatalf("HandleCommand(%q) error = %v", text, err)
		}
		if handled {
			t.Fatalf("HandleCommand(%q) claimed the message", text)
		}
	}
	if len(*replies) != 0 {
		t.Fatalf("replies = %v", *replies)
	}
}

func TestHandleCommandLeavesBackendDirectivesToTheAgent(t *testing.T) {
	cmdCtx, _ := newCommandContext(t)
	// The agent runner parses these itself; the channel must not eat them.
	for _, text := range []string{"/codex fix the build", "/agy do it", "/grok explain", "/pi inspect"} {
		handled, err := HandleCommand(context.Background(), text, cmdCtx)
		if err != nil {
			t.Fatalf("HandleCommand(%q) error = %v", text, err)
		}
		if handled {
			t.Fatalf("HandleCommand(%q) should defer to the agent", text)
		}
		if backend, _, ok := agent.ParseBackendDirective(text); !ok {
			t.Fatalf("%q is no longer a backend directive", text)
		} else if backend == "" {
			t.Fatalf("%q resolved to an empty backend", text)
		}
	}
}

func TestHandleCommandLeavesDesktopRequestsAlone(t *testing.T) {
	cmdCtx, _ := newCommandContext(t)
	handled, err := HandleCommand(context.Background(), "/gui open the settings app", cmdCtx)
	if err != nil {
		t.Fatalf("HandleCommand() error = %v", err)
	}
	if handled {
		t.Fatalf("/gui must reach the desktop queue")
	}
}

func TestHandleCommandEcho(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)

	handled, err := HandleCommand(context.Background(), "/echo hello there", cmdCtx)
	if err != nil || !handled {
		t.Fatalf("HandleCommand() = %v, %v", handled, err)
	}
	if len(*replies) != 1 || (*replies)[0] != "hello there" {
		t.Fatalf("replies = %v", *replies)
	}

	*replies = nil
	if handled, _ := HandleCommand(context.Background(), "/echo", cmdCtx); !handled {
		t.Fatalf("bare /echo should still be handled")
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0], "用法") {
		t.Fatalf("replies = %v", *replies)
	}
}

func TestHandleCommandResetDropsTheStoredSession(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)
	key := SessionKeyFromEvent(cmdCtx.Event)
	if err := cmdCtx.SessionStore.Put(key, "codex", "session-abc"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	handled, err := HandleCommand(context.Background(), "/reset", cmdCtx)
	if err != nil || !handled {
		t.Fatalf("HandleCommand() = %v, %v", handled, err)
	}
	if _, ok, _ := cmdCtx.SessionStore.LookupRecord(key); ok {
		t.Fatalf("the session record survived /reset")
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0], "已重置") {
		t.Fatalf("replies = %v", *replies)
	}

	// A second /reset reports that there was nothing to drop.
	*replies = nil
	if _, err := HandleCommand(context.Background(), "/reset", cmdCtx); err != nil {
		t.Fatalf("HandleCommand() error = %v", err)
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0], "没有找到") {
		t.Fatalf("replies = %v", *replies)
	}
}

func TestHandleCommandResetWithoutASessionStore(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)
	cmdCtx.SessionStore = nil

	handled, err := HandleCommand(context.Background(), "/reset", cmdCtx)
	if err != nil || !handled {
		t.Fatalf("HandleCommand() = %v, %v", handled, err)
	}
	if len(*replies) != 1 || !strings.Contains((*replies)[0], "未启用") {
		t.Fatalf("replies = %v", *replies)
	}
}

func TestHandleCommandStatusReportsThePinnedBackend(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)
	if err := cmdCtx.SessionStore.Put(SessionKeyFromEvent(cmdCtx.Event), "grok", "session-xyz"); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	handled, err := HandleCommand(context.Background(), "/status", cmdCtx)
	if err != nil || !handled {
		t.Fatalf("HandleCommand() = %v, %v", handled, err)
	}
	status := (*replies)[0]
	for _, want := range []string{"grok", "session-xyz", "/work", "已启用"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q is missing %q", status, want)
		}
	}
}

func TestHandleCommandStatusWithoutASession(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)
	cmdCtx.AgentEnabled = false
	cmdCtx.Workspace = ""

	if _, err := HandleCommand(context.Background(), "/status", cmdCtx); err != nil {
		t.Fatalf("HandleCommand() error = %v", err)
	}
	status := (*replies)[0]
	for _, want := range []string{"codex", "（无）", "（未设置）", "未启用"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status %q is missing %q", status, want)
		}
	}
}

func TestHandleCommandIsCaseInsensitive(t *testing.T) {
	cmdCtx, replies := newCommandContext(t)
	if handled, _ := HandleCommand(context.Background(), "/ECHO hi", cmdCtx); !handled {
		t.Fatalf("/ECHO should be handled")
	}
	if len(*replies) != 1 || (*replies)[0] != "hi" {
		t.Fatalf("replies = %v", *replies)
	}
}
