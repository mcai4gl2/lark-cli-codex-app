//go:build integration

package slack

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/platform"
)

// This is a local-only integration test. It drives the real gateway -> agent ->
// `codex exec` subprocess code path with a synthesized Slack event and a mocked
// Slack transport, against the real host `codex` CLI and its real `~/.codex`
// auth. It never mutates `~/.codex` and never runs any login/config command.
//
// It is gated twice: the `integration` build tag (so CI's `go test ./...` never
// compiles it) and the CODEX_INTEGRATION_TEST=1 env var (so `-tags integration`
// alone still skips). See plans/20260623_codex-integration-test.md.

const defaultCodexIntegrationTimeout = 180 * time.Second

// capturedReply is one reply pushed by the fake messenger.
type capturedReply struct {
	event platform.MessageEvent
	text  string
}

// fakeMessenger implements platform.Messenger, capturing outbound replies on a
// channel instead of talking to Slack.
type fakeMessenger struct {
	replies chan capturedReply
}

func newFakeMessenger() *fakeMessenger {
	return &fakeMessenger{replies: make(chan capturedReply, 8)}
}

func (m *fakeMessenger) Reply(_ context.Context, event platform.MessageEvent, text string) error {
	m.replies <- capturedReply{event: event, text: text}
	return nil
}

func (m *fakeMessenger) Send(_ context.Context, _ platform.MessageTarget, text string) error {
	m.replies <- capturedReply{text: text}
	return nil
}

// waitForReply blocks until a reply is captured or the deadline elapses.
func (m *fakeMessenger) waitForReply(t *testing.T, d time.Duration) string {
	t.Helper()
	select {
	case r := <-m.replies:
		return r.text
	case <-time.After(d):
		t.Fatalf("timed out after %s waiting for codex reply", d)
		return ""
	}
}

// codexIntegrationTimeout resolves the per-codex-call timeout from the env knob.
func codexIntegrationTimeout(t *testing.T) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("CODEX_INTEGRATION_TIMEOUT"))
	if raw == "" {
		return defaultCodexIntegrationTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("invalid CODEX_INTEGRATION_TIMEOUT %q: %v", raw, err)
	}
	return d
}

// newCodexGateway builds a gateway wired to the real codex backend with a fake
// messenger and an isolated, session-resume-enabled memory root. The returned
// SessionStore points at the same on-disk file the gateway auto-wires, so test
// assertions observe what the gateway persists.
func newCodexGateway(t *testing.T) (*Gateway, *fakeMessenger, *agent.SessionStore, string) {
	t.Helper()
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	memoryRoot := filepath.Join(tmp, "memory")
	for _, dir := range []string{workspace, memoryRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	fake := newFakeMessenger()
	cfg := Config{
		BotUserID:    "U_BOT",
		Messenger:    fake,
		EventLogPath: filepath.Join(tmp, "events.jsonl"),
		MemoryRoot:   memoryRoot,
		Agent: agent.Config{
			Enabled:        true,
			Backend:        "codex",
			Workspace:      workspace,
			Model:          os.Getenv("CODEX_INTEGRATION_MODEL"), // "" => machine default
			SessionResume:  true,
			AckText:        "", // suppress the ack message so waitForReply sees the result
			ResultMaxChars: 200,
			Timeout:        codexIntegrationTimeout(t),
		},
	}

	g := NewGateway(cfg)
	store := agent.NewSessionStore(filepath.Join(memoryRoot, ".state", "sessions.json"))
	return g, fake, store, memoryRoot
}

// sendSlackMessage synthesizes a raw Slack `message` event payload (as the
// gateway receives over Socket Mode) and feeds it through handleEvent. The
// `channel_type` is "im" because NormalizeEvent only accepts message events from
// DMs (see events.go); `user` differs from BotUserID so the event is not
// ignored as self-authored.
func sendSlackMessage(t *testing.T, g *Gateway, channelID, threadTS, ts, text string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"team_id":  "T_TEST",
		"event_id": "Ev_" + ts,
		"event": map[string]any{
			"type":         "message",
			"channel":      channelID,
			"channel_type": "im",
			"user":         "U_HUMAN",
			"text":         text,
			"ts":           ts,
			"thread_ts":    threadTS,
		},
	})
	if err != nil {
		t.Fatalf("marshal slack event: %v", err)
	}
	if err := g.handleEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
}

// TestCodexIntegration exercises the real codex code path. Subtests run
// sequentially (no t.Parallel) for readable logs and bounded concurrency.
func TestCodexIntegration(t *testing.T) {
	if os.Getenv("CODEX_INTEGRATION_TEST") != "1" {
		t.Skip("set CODEX_INTEGRATION_TEST=1 to run the codex integration test")
	}

	timeout := codexIntegrationTimeout(t)
	prereqOK := false

	// prerequisite probes the environment directly through the backend, so an
	// env failure is unambiguous (and prints codex stderr). On failure the
	// behavioral subtests skip rather than producing confusing failures.
	t.Run("prerequisite", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		backend := agent.CodexBackend{}
		out, err := backend.Execute(ctx, agent.BackendRequest{
			Prompt:         "Reply with only the number 7 and nothing else.",
			Workspace:      t.TempDir(),
			Model:          os.Getenv("CODEX_INTEGRATION_MODEL"),
			Binary:         "codex",
			TempDir:        t.TempDir(),
			ResultMaxChars: 200,
		})
		if err != nil {
			t.Logf("codex prerequisite failed (environment not ready): %v", err)
			t.Skip("codex environment not ready; skipping behavioral subtests")
		}
		if !strings.Contains(out.Text, "7") {
			t.Fatalf("codex prerequisite returned unexpected output: %q", out.Text)
		}
		prereqOK = true
	})

	requirePrereq := func(t *testing.T) {
		t.Helper()
		if !prereqOK {
			t.Skip("codex prerequisite did not pass; skipping")
		}
	}

	// basic_single_shot: end-to-end happy path through a fresh session.
	t.Run("basic_single_shot", func(t *testing.T) {
		requirePrereq(t)
		g, fake, _, _ := newCodexGateway(t)
		channel := "C_BASIC"
		ts := "1750000000.000001"
		sendSlackMessage(t, g, channel, ts, ts, "用一个数字回答：2 + 2 等于几？只回复数字。")
		reply := fake.waitForReply(t, timeout)
		if !strings.Contains(reply, "4") {
			t.Fatalf("expected reply to contain 4, got %q", reply)
		}
	})

	// session_resume: turn 2 must recall a codeword that only the resumed
	// session from turn 1 could know.
	t.Run("session_resume", func(t *testing.T) {
		requirePrereq(t)
		g, fake, store, _ := newCodexGateway(t)
		channel := "C_RESUME"
		threadTS := "1750000000.000100"
		key := agent.SessionKey{Provider: "slack", ChannelID: channel, ThreadTS: threadTS}

		// Turn 1 establishes the codeword and the persisted session id.
		sendSlackMessage(t, g, channel, threadTS, threadTS, "记住这个暗号：BANANA47。只回复 OK。")
		reply1 := fake.waitForReply(t, timeout)
		if strings.TrimSpace(reply1) == "" {
			t.Fatalf("turn 1 reply was empty")
		}
		id, err := store.Lookup(key)
		if err != nil {
			t.Fatalf("session lookup after turn 1: %v", err)
		}
		if strings.TrimSpace(id) == "" {
			t.Fatalf("expected a persisted session id after turn 1, got empty")
		}

		// Turn 2 (same thread => same SessionKey) should resume and recall it.
		sendSlackMessage(t, g, channel, threadTS, "1750000000.000101", "刚才的暗号是什么？只回复暗号本身。")
		reply2 := fake.waitForReply(t, timeout)
		if !strings.Contains(reply2, "BANANA47") {
			t.Fatalf("expected resumed session to recall codeword BANANA47, got %q", reply2)
		}
	})

	// resume_fallback: a pre-seeded bogus session id must fail resume and fall
	// back to a fresh session, repairing the store with the new id.
	t.Run("resume_fallback", func(t *testing.T) {
		requirePrereq(t)
		g, fake, store, _ := newCodexGateway(t)
		channel := "C_FALLBACK"
		threadTS := "1750000000.000200"
		key := agent.SessionKey{Provider: "slack", ChannelID: channel, ThreadTS: threadTS}
		const bogus = "00000000-dead-beef-0000-000000000000"

		if err := store.Put(key, "codex", bogus); err != nil {
			t.Fatalf("seed bogus session id: %v", err)
		}

		sendSlackMessage(t, g, channel, threadTS, threadTS, "回复数字 9。")
		reply := fake.waitForReply(t, timeout)
		if !strings.Contains(reply, "9") {
			t.Fatalf("expected fallback reply to contain 9, got %q", reply)
		}

		id, err := store.Lookup(key)
		if err != nil {
			t.Fatalf("session lookup after fallback: %v", err)
		}
		if strings.TrimSpace(id) == "" || id == bogus {
			t.Fatalf("expected store repaired with a new session id, got %q", id)
		}
	})
}
