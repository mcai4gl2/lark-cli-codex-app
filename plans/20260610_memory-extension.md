# Slack Memory Extension Implementation Plan

Status: **Implemented and verified on 2026-06-10**

Goal: Add Slack memory/audit support that stores channel/thread folders, records inbound and outbound conversation events, persists explicit memory files, and injects compact thread/channel memory into Codex prompts.

Architecture: A focused `internal/slackmemory` package owns filesystem layout, event persistence, Markdown memory files, and prompt context loading. Slack gateway wiring records inbound events before dispatch and records final Codex replies through an agent reply observer. The first implementation is deterministic and explicit: raw JSONL audit always works, summaries/memory are Markdown files maintained manually or by Codex when asked, and automatic LLM summarization remains deferred.

Tech stack: Go 1.24, Cobra CLI, existing `platform.MessageEvent`, JSONL files, Markdown memory/summary files, Docker-based `gofmt`, `go test`, and `go build`.

## Scope

Implemented:

- [x] Store normalized Slack conversation data under `.slack/conversations/<team>/<channel>/...`.
- [x] Store per-thread inbound events in `threads/<thread_ts>/events.jsonl`.
- [x] Store per-channel daily inbound events in `daily/YYYY-MM-DD.jsonl`.
- [x] Store final outbound Codex replies in the same thread `events.jsonl`.
- [x] Support thread-level `summary.md` and `memory.md`.
- [x] Support channel-level `memory.md` for durable channel or generic chat memory.
- [x] Inject channel memory, thread memory, and thread summary Markdown into Codex prompts when files exist.
- [x] Add CLI commands for inspecting memory paths and writing memory notes.
- [x] Document Slack memory setup and usage.

Deferred:

- [ ] Automatic LLM summarization of long threads.
- [ ] Automatic promotion of facts from raw audit logs into `memory.md`.
- [ ] Cross-process file locking for multiple gateway processes writing the same memory root.

## Storage Layout

Default root:

```text
<repo-or-config-root>/.slack/conversations/
  <team_id_or_no-team>/
    <channel_id>/
      memory.md
      daily/
        YYYY-MM-DD.jsonl
      threads/
        <thread_ts>/
          events.jsonl
          summary.md
          memory.md
```

Each `events.jsonl` line uses:

```go
type ConversationRecord struct {
	Direction  string                `json:"direction"`
	RecordedAt string                `json:"recorded_at"`
	Event      platform.MessageEvent `json:"event"`
	Text       string                `json:"text,omitempty"`
}
```

`Direction` is `inbound` or `outbound`.

## Implementation Checklist

### Task 1: Slack Memory Store Filesystem Layout

Files:

- `internal/slackmemory/store.go`
- `internal/slackmemory/store_test.go`

Status: **Complete**

- [x] Added `Store`, `Config`, and `ConversationRecord`.
- [x] Added nil-safe and disabled-store no-op behavior.
- [x] Added inbound recording to thread and daily JSONL logs.
- [x] Added outbound recording to thread JSONL logs only.
- [x] Added channel/thread path helpers for `memory.md` and `summary.md`.
- [x] Added Markdown read/append helpers.
- [x] Added path sanitization for missing and unsafe Slack IDs.
- [x] Added focused tests for layout, JSONL records, nil/disabled stores, sanitization, fallback dates, and Markdown helpers.

### Task 2: Prompt Memory Context Loader

Files:

- `internal/slackmemory/context.go`
- `internal/slackmemory/context_test.go`

Status: **Complete**

- [x] Added `ContextOptions`.
- [x] Added `BuildPromptContext`.
- [x] Loads sections in this order: channel memory, thread memory, thread summary.
- [x] Skips missing or empty Markdown files.
- [x] Applies per-section truncation with a default of 2000 characters.
- [x] Returns contextual read errors.
- [x] Added tests for ordering, missing files, disabled stores, default limits, and read errors.

### Task 3: Agent Prompt Context And Outbound Observer Hooks

Files:

- `internal/agent/codex.go`
- `internal/agent/codex_test.go`

Status: **Complete**

- [x] Added `PromptContextProvider`.
- [x] Added `ReplyObserver`.
- [x] Added optional config fields for prompt context and reply observation.
- [x] Loads prompt context before invoking `codex exec`.
- [x] Logs prompt-context errors and continues without memory.
- [x] Injects memory as background context that cannot override current user requests or system/task requirements.
- [x] Observes final replies only.
- [x] Does not observe acknowledgement replies.
- [x] Added tests for prompt injection, provider error fallback, final reply observation, trimmed replies, and ack exclusion.

### Task 4: Slack Gateway Memory Wiring

Files:

- `internal/slack/gateway.go`
- `internal/slack/gateway_test.go`

Status: **Complete**

- [x] Added `MemoryEnabled`, `MemoryRoot`, and `MemoryMaxSectionChars` to Slack gateway config.
- [x] Creates `slackmemory.Store` when memory is enabled and a root is configured.
- [x] Wires store into the agent prompt context provider.
- [x] Wires store into the agent final reply observer.
- [x] Records inbound events after normal inbound processing and before desktop/agent routing.
- [x] Keeps `memoryReplyObserver` nil-safe.
- [x] Added gateway tests for inbound persistence, prompt context wiring, outbound recording, and nil-safe observer behavior.

### Task 5: Config And Gateway CLI Flags

Files:

- `internal/config/config.go`
- `internal/config/slack_config_test.go`
- `internal/cmd/slack.go`

Status: **Complete**

- [x] Added `slack.memory.enabled`.
- [x] Added `slack.memory.root`.
- [x] Added `slack.memory.max_section_chars`.
- [x] Added env bindings: `SLACK_MEMORY_ENABLED`, `SLACK_MEMORY_ROOT`, `SLACK_MEMORY_MAX_SECTION_CHARS`.
- [x] Added config accessors with relative path handling and safe defaults.
- [x] Added gateway flags: `--memory`, `--memory-root`, `--memory-max-section-chars`.
- [x] Added memory fields to gateway startup JSON output.
- [x] Added config tests for defaults and env-provided memory values.

### Task 6: Slack Memory CLI Commands

Files:

- `internal/cmd/slack.go`
- `internal/cmd/slack_test.go`
- `internal/slackmemory/store.go`

Status: **Complete**

- [x] Added `lark slack memory path`.
- [x] Added `lark slack memory show`.
- [x] Added `lark slack memory append`.
- [x] Added `Store.Root()`.
- [x] Added flags: `--team`, `--channel`, `--thread-ts`, `--scope`, `--text`.
- [x] Added validation for required channel, required text, invalid scope, and required `--thread-ts` for thread/summary scopes.
- [x] Added command registration and scope path tests.

### Task 7: Docs And Example Config

Files:

- `config.example.yaml`
- `USER_GUIDE.md`
- `slack-migration.md`

Status: **Complete**

- [x] Documented `slack.memory` config in `config.example.yaml`.
- [x] Replaced the future-memory section in `USER_GUIDE.md` with current behavior.
- [x] Added recommended generic-chat and project-specific memory configurations.
- [x] Documented `slack memory path/show/append`.
- [x] Added memory extension status to `slack-migration.md`.

### Task 8: Verification

Status: **Complete**

Commands run successfully:

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/slackmemory ./internal/slack ./internal/agent ./internal/cmd ./internal/config
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...
docker run --rm -v "$PWD:/work" -w /work golang:1.24 sh -c 'git config --global --add safe.directory /work && go build -ldflags "-s -w" -o ./lark ./cmd/lark'
git diff --check
```

Smoke test run successfully:

```bash
tmp=$(mktemp -d)
LARK_CONFIG_DIR="$tmp/.lark" ./lark slack memory append --channel D123 --scope channel --text "- User prefers concise implementation plans."
LARK_CONFIG_DIR="$tmp/.lark" ./lark slack memory show --channel D123 --scope channel
LARK_CONFIG_DIR="$tmp/.lark" ./lark slack memory path --channel D123 --thread-ts 1710000000.000100
```

The generated `./lark` binary was removed after verification.

## Usage Summary

Enable memory for a gateway:

```bash
./lark slack gateway serve \
  --agent \
  --memory \
  --memory-root "$HOME/CodexChat/.slack/conversations" \
  --agent-workspace "$HOME/CodexChat"
```

Append durable channel memory:

```bash
./lark slack memory append \
  --channel D123 \
  --scope channel \
  --text "- User prefers concise implementation plans."
```

Show memory:

```bash
./lark slack memory show --channel D123 --scope channel
```

Inspect paths:

```bash
./lark slack memory path --channel D123 --thread-ts 1710000000.000100
```

## Risks And Follow-Ups

- **Automatic summarization:** Deferred. Add later as a separate plan that invokes Codex or another summarizer after a thread exceeds a size threshold.
- **Prompt bloat:** Controlled by `slack.memory.max_section_chars`; keep the default conservative.
- **Concurrent writes:** Store uses one process-local mutex. Multiple gateway processes writing the same memory root are not guaranteed to be cross-process transactional.
- **Privacy:** Memory files may contain sensitive Slack content. Users should place memory roots in private local folders and avoid committing `.slack/conversations`.
- **Multiple Slack apps:** Recommended. Each app can use a separate `LARK_CONFIG_DIR`, token set, event log, memory root, and workspace.
