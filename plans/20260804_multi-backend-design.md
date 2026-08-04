# Multi-Backend Design: Per-Message Backend Selection + Grok

**Date:** 2026-08-04
**Status:** Implemented (see `plans/20260804_multi-backend-implementation.md`)
**Scope:** Harden the existing `internal/agent` backend abstraction into a registry, add a
`GrokBackend`, and let a single chat message pick which backend handles it — with the pick
sticking for the rest of that thread until changed again.

---

## 1. Prior Work

[`plans/20260614_mutlti-backend-support.md`](./20260614_mutlti-backend-support.md) (fully
implemented) introduced the `Backend` interface, `CodexBackend`, and `AgyBackend`, plus the
`agent.backend` / `slack.agent.backend` config knobs that pick one backend for an entire
gateway process at startup. That design explicitly left "mixing multiple backends in one
gateway process by channel/user routing" out of scope. This document is the follow-up that
adds that routing, plus a third backend (Grok).

Current state (`internal/agent/backend.go`, `internal/agent/codex.go`,
`internal/agent/backend_agy.go`):

- `Backend` interface: `Name()`, `DefaultBinary()`, `Execute(ctx, BackendRequest) (BackendResult, error)`.
- Two implementations: `CodexBackend` (JSON-streamed subprocess, session resume via
  `codex ... resume <id>`), `AgyBackend` (combined-output subprocess, no session resume).
- Backend choice is resolved once per config via three parallel `switch` statements
  (`normalizeBackendName`, `resolveBackend`, `backendLabel`) — an unrecognized name silently
  falls back to `codex` rather than erroring.
- `SessionStore` (`internal/agent/session.go`) persists a `SessionID` per
  `(Provider, ChannelID, ThreadTS)` key, used only for Codex's resume flow today.
- Backend is fixed per gateway process (config file / CLI flag / env var); no per-message
  or per-thread override exists.

## 2. Goals

1. Let a user pick a backend for a specific message by prefixing it, e.g. `/grok explain this`.
2. That pick should stick for the rest of the thread (subsequent messages without a prefix
   keep using the picked backend) until the user prefixes a different backend name.
3. Add `GrokBackend` as a third CLI-subprocess backend, following the same pattern as `agy`.
4. Make adding a future fourth backend a one-line registration instead of touching three
   `switch` statements.
5. Keep the existing config-level default (`agent.backend`, `slack.agent.backend`) as the
   fallback when no thread pin and no per-message directive apply — "some default" per the
   original ask.

Out of scope for this pass:

- A native Slack slash command (e.g. registering `/agent-backend` with Slack's API). The
  in-message prefix works uniformly across Slack, Lark, and webhook without any
  platform-specific registration, which covers the same need.
- A config-level allowlist restricting which users/channels may pick which backend. Anyone
  who can message the gateway can already pick among all configured backends via
  `agent.backend`; per-message selection doesn't change that trust boundary.
- Any backend beyond Codex, Agy, and Grok (Claude/Pi remain noted as future work per the
  prior plan).

## 3. Design Decisions

### 3.1 Backend registry replaces the three switch statements

`internal/agent/backend.go` becomes a small registry:

```go
var backends = map[string]Backend{
    "codex": CodexBackend{},
    "agy":   AgyBackend{},
    "grok":  GrokBackend{},
}

var backendAliases = map[string]string{
    "antigravity":     "agy",
    "antigravity-cli": "agy",
}

func Resolve(name string) (Backend, bool) {
    key := strings.ToLower(strings.TrimSpace(name))
    if real, ok := backendAliases[key]; ok {
        key = real
    }
    b, ok := backends[key]
    return b, ok
}

func RegisteredBackendNames() []string // sorted, for error messages
```

This replaces `normalizeBackendName`, `resolveBackend`, and the `backendLabel` switch with
registry lookups. Adding Grok (or any future backend) is: implement `Backend`, add one map
entry, done.

Unlike today, an **unrecognized configured default** (`agent.backend: typo-name`) is a real
error, not a silent fallback to `codex` — validated once at config load / gateway startup so
misconfiguration is caught immediately instead of quietly running the wrong backend.

### 3.2 Per-message directive parsing

New `internal/agent/directive.go`:

```go
func ParseBackendDirective(text string) (backend string, rest string, ok bool)
```

Recognizes a message that *starts* with `/<name>` where `<name>` resolves via `Resolve()`
(case-insensitive, aliases included). Strips the token and leading whitespace, returns the
remainder as the real prompt text.

**An unrecognized `/word` is left completely untouched** — it is not stripped and does not
error. It's treated as ordinary message text. This is deliberate: free text legitimately
starting with `/` (a path, a note, an unrelated slash-looking phrase) must not be silently
eaten or rejected just because it isn't a known backend name. Only a token that actually
matches a registered backend name/alias triggers a switch.

### 3.3 Sticky per-thread pin, coupled to the session record

`SessionRecord` (`internal/agent/session.go`) gains a `Backend string` field:

```go
type SessionRecord struct {
    Key       SessionKey `json:"key"`
    Backend   string     `json:"backend,omitempty"`
    SessionID string     `json:"session_id"`
    CreatedAt string     `json:"created_at"`
    UpdatedAt string     `json:"updated_at"`
}
```

`SessionStore` gets `LookupRecord(key) (SessionRecord, bool, error)` and
`Put(key, backend, sessionID string) error`, replacing the session-ID-only versions. Existing
JSON session files with no `"backend"` key decode with `Backend == ""` (treated as unpinned) —
no migration step needed.

The pin and the session share one record because they share one lifecycle: a Codex session ID
is meaningless to Grok's CLI, so switching a thread's backend must invalidate any live session
for that thread anyway. Keeping them in the same record avoids a second store that could drift
out of sync with the first.

**Effective backend resolution order**, per message:

1. Directive parsed from this message, if present and recognized.
2. Else, `Backend` on the thread's existing `SessionRecord`, if any.
3. Else, the configured default (`agent.backend` / `slack.agent.backend`).

If the resolved backend differs from what's on the record, the stored `SessionID` is discarded
(it belongs to a dead backend) and a fresh, full-context prompt is built — the same code path
already used today when a session-resume attempt errors out. Otherwise, today's resume flow
runs unchanged (send only the new message text).

### 3.4 GrokBackend

New `internal/agent/grok.go`, structurally identical to `AgyBackend`: shell out to a local
`grok` binary, capture combined stdout/stderr.

```go
type GrokBackend struct{}

func (GrokBackend) Name() string { return "grok" }

func (GrokBackend) DefaultBinary() string { return "grok" }

func (GrokBackend) Execute(ctx context.Context, req BackendRequest) (BackendResult, error) {
    args := []string{"--add-dir", req.Workspace, "--prompt", req.Prompt}
    if strings.TrimSpace(req.Model) != "" {
        args = append(args, "--model", strings.TrimSpace(req.Model))
    }
    args = append(args, splitArgs(req.Args)...)
    // exec.CommandContext(ctx, req.Binary, args...), same output/error handling as AgyBackend.
}
```

The exact flag names are a placeholder pending a local contract check (`grok --help`,
`grok --version`, a smoke prompt) the same way the prior plan validated `agy`'s CLI before
committing to `agy --add-dir ... --prompt ...`. Like Agy, Grok has no session-resume support
in this pass — `BackendResult.SessionID` stays empty, so a thread pinned to `grok` always runs
fresh-context prompts (still fine: the "no resume" behavior already exists for Agy today).

Config gets one new field mirroring `codex_binary`: `agent.grok_binary` / `slack.agent.grok_binary`
(default `"grok"`), consumed the same way `resolveBackendBinary` already special-cases
`codex_binary` today.

## 4. Data Flow

```
inbound message (Slack/Lark/webhook)
        │
        ▼
inbound.LoggedEvent  (unchanged)
        │
        ▼
Runner.execute()                                   internal/agent/codex.go
   │
   ├─ ParseBackendDirective(entry.MessageText)      internal/agent/directive.go   [new]
   │    → directiveBackend, cleanPrompt, hasDirective
   │
   ├─ SessionStore.LookupRecord(threadKey)          internal/agent/session.go     [extended]
   │    → record{Backend, SessionID}
   │
   ├─ effective := directiveBackend, else record.Backend, else cfg default
   ├─ backend, ok := Resolve(effective)             internal/agent/backend.go     [reworked]
   │    not ok → reply "未知后端: ...", stop (only reachable via bad config default)
   │
   ├─ if effective != record.Backend:
   │      discard record.SessionID, build full-context prompt (backendLabel(effective))
   │   else:
   │      reuse record.SessionID, send cleanPrompt as-is (resume flow, unchanged)
   │
   ▼
backend.Execute(ctx, req)   →  CodexBackend | AgyBackend | GrokBackend
        │
        ▼
on success: SessionStore.Put(threadKey, effective, result.SessionID)
        │
        ▼
reply + observers (unchanged)
```

## 5. Error Handling

- **Unrecognized `/word` in a message** → not an error; passed through as literal text (§3.2).
- **Configured default backend name not registered** (`agent.backend: typo`) → fail fast at
  config load / gateway startup with a clear error listing `RegisteredBackendNames()`, rather
  than today's silent fallback to `codex`.
- **`Resolve()` fails at dispatch time** (only reachable if the above check is somehow
  bypassed) → reply "未知后端: %s，可用: codex, agy, grok"; no subprocess is spawned.
- **Backend subprocess fails** (missing binary, non-zero exit, timeout) → unchanged from
  today: `Backend.Execute` returns an error, Runner replies "处理失败：...".
- **Mid-thread backend switch discards a live session** → not an error; one info-level log
  line ("thread %s switching backend %s → %s, starting fresh session").

## 6. Testing Plan

- `internal/agent/backend_test.go`: registry `Resolve()` incl. aliases, case-insensitivity,
  unknown-name behavior, `RegisteredBackendNames()` sorting.
- `internal/agent/grok_test.go`: mirrors `backend_agy_test.go` — fake `grok` executable,
  assert argv construction (workspace, prompt, model, passthrough args), success and failure
  output handling.
- `internal/agent/directive_test.go`: table tests — recognized backend name at message start,
  unrecognized `/word` passthrough (verifies text is untouched), no leading slash, alias
  resolution, case-insensitivity, directive with no trailing text.
- `internal/agent/session_test.go`: extend for `Backend` field round-trip through
  `LookupRecord`/`Put`; decode an old fixture file with no `"backend"` key and confirm it's
  treated as unpinned.
- `internal/agent/codex_test.go` (Runner-level): directive overrides default backend for one
  message; sticky pin carries to the next message without a directive; switching backends
  mid-thread discards the old session and starts fresh; unregistered configured default
  produces a startup error instead of silently running codex.
- Per `CLAUDE.md`: focused run first —
  `docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent ./internal/gateway ./internal/webhook ./internal/desktop ./internal/inbound`,
  then full `go test ./...` before calling the implementation done.

## 7. Config Changes

Additive only, no breaking changes to existing deployments:

```yaml
agent:
  backend: "codex"        # unchanged: process-level default when no pin/directive applies
  grok_binary: "grok"     # new, mirrors codex_binary
slack:
  agent:
    backend: "codex"
    grok_binary: "grok"
```

Env vars: `LARK_AGENT_GROK_BINARY`, `SLACK_AGENT_GROK_BINARY`, mirroring the existing
`*_CODEX_BINARY` pattern.

## 8. Acceptance Criteria

- Existing Codex- and Agy-only deployments behave identically with no config changes
  (default backend, no directive ever sent → same as today).
- A message starting with `/grok ...` (or `/agy ...`, `/codex ...`) in a thread with no prior
  pin runs on that backend and pins the thread to it.
- A following message in the same thread with no directive continues on the pinned backend,
  resuming its session where the backend supports resume.
- A message with a different recognized directive switches the thread's pin and starts a
  fresh session under the new backend.
- A message starting with `/something-unrelated` is treated as plain text, not a directive.
- An unregistered `agent.backend` / `slack.agent.backend` value fails fast at startup with a
  listing of valid backend names.
- Docker `gofmt` clean; focused and full `go test ./...` pass.

## 9. Open Questions To Resolve During Implementation

- Exact `grok` CLI non-interactive contract (flags for workspace/prompt/model) — needs a
  local `grok --help` / `grok --version` / smoke-prompt validation pass, same as the prior
  plan did for `agy`, before `grok.go`'s argv construction is finalized.
- Should the directive prefix character be `/` specifically, or should common typos/case
  variants (e.g. leading `!`) also be recognized? Current design fixes on `/` only, matching
  the natural "slash command" mental model without registering anything with Slack.
