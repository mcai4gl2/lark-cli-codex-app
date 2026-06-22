# Long-Running Codex Sessions for Project Mode

Status: **Phase 1 implementation plan, 2026-06-23**

Goal: Replace the fire-and-forget `codex exec` model with persistent Codex sessions that map 1:1 to Slack threads. Each follow-up message in a thread should resume the same Codex session rather than spawning a new process with reconstructed context. This applies to both project mode and chat mode — letting Codex manage its own conversation state instead of our homebrew transcript-replay.

Motivation: The current transcript-replay approach works for short exchanges but degrades as conversations grow. Context gets truncated, Codex loses tool-call history and file state from prior turns, and long outbound replies must be summarized lossy. A persistent session preserves the full conversation context natively within Codex, including tool results, file diffs, and reasoning state.

## Current Architecture

```
Slack message → Gateway.processEntry()
  → agent.Dispatch() → goroutine → agent.run()
    → build prompt with memory/transcript context
    → codex exec ... "$PROMPT"   (one-shot subprocess, exits after reply)
    → read output file → post reply to Slack
```

Every message spawns a new Codex process. Conversation continuity is faked by injecting up to 30 recent transcript records (8000 chars) into the prompt. Codex has no memory of prior tool calls, file reads, or intermediate reasoning.

## Feasibility Validation (completed)

Key unknowns tested on 2026-06-23:

1. **Session ID extraction** — `codex exec --json` emits `{"type":"thread.started","thread_id":"<uuid>"}` as the first JSONL event. Confirmed working.
2. **Resume + output-last-message** — `codex exec resume <id> --output-last-message <file> "<prompt>"` works correctly. The resumed session has full context from prior turns. Confirmed working.
3. **Resume remembers prior turns** — Asking "what was my previous message?" after resume correctly recalls the first turn. Confirmed.

## Phase 1 Design: `codex exec resume`

### Architecture Overview

```
Slack message → Gateway.processEntry()
  → agent.Dispatch() → goroutine → agent.run()
    → SessionStore.Lookup(threadKey) → sessionID (or "")
    → if sessionID != "":
        codex exec resume <sessionID> --json -o <file> "$USER_MESSAGE"
      else:
        codex exec --json -o <file> "$FULL_PROMPT_WITH_CONTEXT"
    → parse --json output: extract thread_id from thread.started event
    → SessionStore.Put(threadKey, thread_id)
    → read output file → post reply to Slack
```

### Component Design

#### 1. `internal/agent/session.go` — Session Store

A thread-safe JSON file store mapping thread keys to Codex session IDs. Same file/locking pattern as `RecoveryStore` in `internal/slack/recover.go`.

```go
package agent

type SessionKey struct {
    Provider  string `json:"provider"`
    ChannelID string `json:"channel_id"`
    ThreadTS  string `json:"thread_ts"`
}

type SessionRecord struct {
    Key       SessionKey `json:"key"`
    SessionID string     `json:"session_id"`
    CreatedAt string     `json:"created_at"`
    UpdatedAt string     `json:"updated_at"`
}

type SessionStore struct {
    path string
    mu   sync.Mutex
}

func NewSessionStore(path string) *SessionStore
func (s *SessionStore) Lookup(key SessionKey) (string, error)
func (s *SessionStore) Put(key SessionKey, sessionID string) error
func (s *SessionStore) Remove(key SessionKey) (bool, error)
```

Storage location: `<memory-root>/.state/sessions.json` (alongside the existing `recover-state.json`).

File format:
```json
{
  "sessions": [
    {
      "key": {"provider": "slack", "channel_id": "C123", "thread_ts": "1719100000.000100"},
      "session_id": "019ef16e-3115-7731-831f-0b6f843f12e7",
      "created_at": "2026-06-23T10:00:00Z",
      "updated_at": "2026-06-23T10:05:00Z"
    }
  ]
}
```

#### 2. `internal/agent/backend.go` — Backend Interface Changes

The `Backend` interface return type changes from `(string, error)` to `(BackendResult, error)`:

```go
type BackendResult struct {
    Text      string
    SessionID string  // Codex thread_id extracted from --json output; empty for non-Codex backends
}

type Backend interface {
    Name() string
    DefaultBinary() string
    Execute(ctx context.Context, req BackendRequest) (BackendResult, error)
}
```

`BackendRequest` gains a new field:

```go
type BackendRequest struct {
    // ... existing fields ...
    SessionID string  // If non-empty, resume this session instead of starting new
}
```

`AgyBackend.Execute()` returns `BackendResult{Text: ..., SessionID: ""}` — no session support.

#### 3. `internal/agent/codex.go` — CodexBackend Changes

The `CodexBackend.Execute()` method changes to:

**New session (SessionID empty):**
```
codex -a never -s workspace-write exec \
  --json \
  -C "$WORKSPACE" \
  --skip-git-repo-check \
  --output-last-message "$OUTPUT_FILE" \
  [extra args...] \
  "$PROMPT"
```

Parse the `--json` JSONL output line-by-line. Extract `thread_id` from the `{"type":"thread.started","thread_id":"..."}` event.

**Resume session (SessionID non-empty):**
```
codex -a never -s workspace-write exec resume \
  --json \
  --skip-git-repo-check \
  --output-last-message "$OUTPUT_FILE" \
  [extra args...] \
  "$SESSION_ID" \
  "$USER_MESSAGE"
```

Note: on resume, we pass the raw user message text, NOT the full prompt with memory context. Codex already has the full conversation history.

**Fallback:** If resume fails (session not found, corrupt, etc.), log the error, clear the session mapping, and retry as a new session with full prompt context.

**JSONL parsing:** The `--json` flag causes Codex to write JSONL events to stdout instead of the TUI. We only need to parse one field:
```go
type codexJSONEvent struct {
    Type     string `json:"type"`
    ThreadID string `json:"thread_id,omitempty"`
}
```
Read stdout line-by-line, find the `thread.started` event, capture `thread_id`.

**Implementation detail:** Currently `CodexBackend.Execute()` uses `cmd.CombinedOutput()` which buffers all output. With `--json`, we need to stream stdout to parse events while the process runs. Change to `cmd.StdoutPipe()` + scanner, with stderr captured separately.

#### 4. `internal/agent/codex.go` — Runner Changes

`Runner.execute()` orchestrates session lookup and storage:

```go
func (r *Runner) execute(entry inbound.LoggedEvent) (string, error) {
    backend := resolveBackend(r.cfg)

    // Session lookup
    sessionID := ""
    if r.cfg.SessionStore != nil {
        key := sessionKeyFromEntry(entry)
        if id, err := r.cfg.SessionStore.Lookup(key); err == nil {
            sessionID = id
        }
    }

    // Build prompt — full context for new sessions, just user text for resume
    var prompt string
    if sessionID != "" {
        prompt = entry.MessageText
    } else {
        // existing prompt-building logic with memory context
        prompt = buildPromptWithContext(...)
    }

    result, err := backend.Execute(ctx, BackendRequest{
        ...
        SessionID: sessionID,
        Prompt:    prompt,  // raw message for resume, full prompt for new
    })

    // Fallback: if resume failed, retry as new session
    if err != nil && sessionID != "" {
        r.logger.Printf("session resume failed for %s, falling back to new session: %v", sessionID, err)
        if r.cfg.SessionStore != nil {
            r.cfg.SessionStore.Remove(sessionKeyFromEntry(entry))
        }
        prompt = buildPromptWithContext(...)  // rebuild full prompt
        result, err = backend.Execute(ctx, BackendRequest{
            ...
            SessionID: "",
            Prompt:    prompt,
        })
    }

    if err != nil {
        return "", err
    }

    // Store session mapping
    if result.SessionID != "" && r.cfg.SessionStore != nil {
        r.cfg.SessionStore.Put(sessionKeyFromEntry(entry), result.SessionID)
    }

    return trimForChat(result.Text, r.cfg.ResultMaxChars), nil
}
```

#### 5. `agent.Config` — New Fields

```go
type Config struct {
    // ... existing fields ...
    SessionResume bool           // Enable session resume (opt-in)
    SessionStore  *SessionStore  // Injected by gateway; nil disables session tracking
}
```

#### 6. `internal/config/config.go` — Config Addition

New config field under both `agent` and `slack.agent`:

```yaml
slack:
  agent:
    session_resume: true
```

```go
// In Config struct
Agent struct {
    // ... existing ...
    SessionResume bool `mapstructure:"session_resume"`
}
// Same under Slack.Agent

// Defaults
viper.SetDefault("agent.session_resume", false)
viper.SetDefault("slack.agent.session_resume", false)

// Env bindings
viper.BindEnv("agent.session_resume", "LARK_AGENT_SESSION_RESUME")
viper.BindEnv("slack.agent.session_resume", "SLACK_AGENT_SESSION_RESUME")

// Getter
func GetSlackAgentSessionResume() bool {
    return viper.GetBool("slack.agent.session_resume")
}
```

#### 7. `internal/slack/gateway.go` — Wiring

In `NewGateway()`, create the session store and pass it into the agent config:

```go
if cfg.Agent.SessionResume && strings.TrimSpace(cfg.MemoryRoot) != "" {
    cfg.Agent.SessionStore = agent.NewSessionStore(
        filepath.Join(cfg.MemoryRoot, ".state", "sessions.json"),
    )
}
```

Session cleanup: when `RecoveryStore.RemoveThread()` is called (thread expired or closed by tick reaction), also call `SessionStore.Remove()` for the same thread key.

#### 8. `internal/cmd/slack.go` — CLI Wiring

Pass `SessionResume` through `DefaultAgentConfig`:

```go
agentCfg := slackgateway.DefaultAgentConfig(slackgateway.DefaultAgentConfigInput{
    // ... existing ...
    SessionResume: config.GetSlackAgentSessionResume(),
})
```

Add to startup JSON output:
```go
"agent_session_resume": cfg.Agent.SessionResume,
```

#### 9. Memory Context Behavior

| Scenario | Memory injection | Transcript replay | Prompt |
|----------|-----------------|-------------------|--------|
| New session (no session ID) | Channel + thread memory | Full transcript | Full prompt with all context |
| Resume session | Channel + thread memory | **Skipped** | Raw user message only |
| Resume fallback (after failure) | Channel + thread memory | Full transcript | Full prompt (same as new) |

On resume, we skip transcript replay because Codex already has the full conversation including tool calls and file diffs — richer than our text transcript. We still inject channel memory and thread memory on new sessions since those contain durable facts the user explicitly saved.

The prompt for a resumed session is just the raw `entry.MessageText` — no wrapper template, no system instructions. Codex already has the system prompt and context from the original session.

### File Change Summary

| File | Change |
|------|--------|
| `internal/agent/session.go` | **New** — SessionStore, SessionKey, SessionRecord |
| `internal/agent/session_test.go` | **New** — unit tests for SessionStore |
| `internal/agent/backend.go` | Modify — BackendResult type, Backend interface, BackendRequest.SessionID field |
| `internal/agent/backend_agy.go` | Modify — return BackendResult instead of string |
| `internal/agent/codex.go` | Modify — Execute uses --json, parses thread_id, supports resume; Runner.execute does session lookup/store/fallback |
| `internal/agent/codex_test.go` | Modify — update tests for BackendResult, add resume and fallback tests |
| `internal/config/config.go` | Modify — add SessionResume field, default, env binding, getter |
| `internal/slack/gateway.go` | Modify — create SessionStore, pass to agent config, wire cleanup |
| `internal/cmd/slack.go` | Modify — pass SessionResume in DefaultAgentConfigInput |
| `config.example.yaml` | Modify — add session_resume example |

### Concurrency Considerations

- `SessionStore` uses a mutex (same as `RecoveryStore`). Safe for concurrent goroutines within one gateway process.
- Two messages for the same thread arriving simultaneously: the first to call `backend.Execute()` wins. The second will try to resume the session that may be in-use by the first Codex process. Codex may lock the session file — if so, the second call will fail and fall back to a new session. Acceptable for now; could add a per-thread mutex later if needed.

### Testing Plan

1. **Unit tests** — SessionStore CRUD, CodexBackend --json parsing, resume command building, fallback on resume failure
2. **Integration test** — fake-codex script that emits `thread.started` JSONL + writes output file, verifies resume path is used on second call
3. **Manual test** — real Slack thread with 5+ back-and-forth messages, verify Codex remembers prior turns

---

## Future: Phase 2 — `codex app-server` WebSocket Backend

Deferred until the app-server protocol stabilizes. See the original feasibility analysis above for details.

### Option B: `codex app-server` (WebSocket JSON-RPC protocol)

Codex ships an experimental app-server that exposes a JSON-RPC protocol over WebSocket or Unix socket. This is the same protocol used by VS Code and the Codex desktop app.

**Key protocol operations:**
- `thread/start` → creates a new thread, returns `thread.id`
- `thread/resume` → reloads a thread by ID (supports rejoining a *running* thread)
- `turn/start` → sends user input to a thread, starts agent processing
- `turn/completed` notification → signals turn is done
- `agentMessage/delta` notifications → streaming output
- `thread/list`, `threadLoaded/list` → enumerate sessions

**Thread lifecycle:**
- Threads have statuses: `notLoaded`, `idle`, `active`, `systemError`
- A thread can be `idle` (loaded in memory, waiting for input) — this is the persistent session state we want
- Multiple clients can connect to the same app-server daemon and resume the same thread
- The daemon listens on `~/.codex/app-server-control/app-server-control.sock` (Unix socket)

**Pros:**
- True persistent sessions: no cold-start, no reload overhead between messages
- Streaming output: can post partial replies or "typing" indicators
- Rich input types: text, images, local files, context entries — not just a prompt string
- Thread remains loaded in memory; Codex manages its own context compaction

**Cons:**
- Marked `[experimental]` — API may change between Codex versions
- Requires running and managing a daemon process
- No official Go client library
- Approval request handling complexity
