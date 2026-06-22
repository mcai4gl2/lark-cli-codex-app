# Codex Integration Test

**Date:** 2026-06-23
**Status:** Design approved, ready to implement
**Scope:** A locally-run integration test that drives the real code path with a mocked Slack
message and a **real** local `codex` CLI, verifying codex interaction behavior (single-shot
exec, session resume, resume fallback). Slack transport itself is mocked; we trust the public
Slack API.

---

## 1. Goals & Constraints

1. **Real codex, mocked Slack.** Synthesize a Slack message event, feed it through the real
   gateway → agent → `codex exec` subprocess, and capture the outbound reply.
2. **Careful token use.** Prompts are tiny, deterministic, and language-neutral. ~5 model
   calls total. No model override (use the machine's configured default; "codex as it is").
3. **Local-only, never on CI.** Gated by both a build tag and an opt-in env var.
4. **Cover all codex interactions**, especially the recently-built session resume and its
   fallback path.
5. **Prerequisite probe.** Verify the env works first; if not, print the error and skip the
   behavioral tests. **Never** mutate `~/.codex` or run any login/config command.

---

## 2. The Docker-vs-host conflict (and resolution)

`AGENTS.md`/`CLAUDE.md` require running all Go tests inside the `golang:1.24` container. These
tests cannot run fully in that container: they invoke the **host** `codex` binary
(`~/.npm-global/bin/codex`, a Node CLI) using the **host** `~/.codex` subscription auth, neither
of which exists in the container.

**Resolution (documented exception):** compile the test binary *in* Docker (honoring the
toolchain rule), then *run* it on the host where codex + auth live. Linux host + Linux
container → the binary is compatible.

```bash
# 1) compile the integration test binary in Docker (no host go toolchain used)
docker run --rm -v "$PWD:/work" -w /work golang:1.24 \
  go test -c -tags integration -o codex_integration.test ./internal/slack

# 2) run it on the host against the real codex CLI + real auth
CODEX_INTEGRATION_TEST=1 ./codex_integration.test -test.v -test.run TestCodexIntegration

# cleanup
rm -f codex_integration.test
```

Everything else (unit tests, `gofmt`, builds) continues to run in Docker per the existing rule.
`codex_integration.test` must be added to `.gitignore`.

---

## 3. Architecture under test (reference)

```
Slack event JSON
  └─ Gateway.handleEvent(ctx, payload)        // internal/slack/gateway.go
       └─ NormalizeEvent(payload, botUserID)  // → platform.MessageEvent
            └─ Gateway.processEntry
                 └─ agent.Runner.Dispatch(entry)   // ASYNC: go r.run(entry)
                      └─ Runner.execute
                           ├─ SessionStore.Lookup(key)          // resume id, if any
                           ├─ CodexBackend.Execute(...)         // spawns `codex exec [...]`
                           │     └─ on resume error → fallback to fresh session
                           ├─ SessionStore.Put(key, newID)      // persist returned thread id
                           └─ Messenger.Reply(ctx, entry, text) // captured by fake messenger
```

Key facts the test relies on:

- `SessionKey = {Provider, ChannelID, ThreadTS}`; `ThreadID` falls back to `MessageID` when empty
  (`sessionKeyFromEntry`, `internal/agent/codex.go`).
- `SessionStore` auto-wires to `MemoryRoot/.state/sessions.json` when
  `Agent.SessionResume && MemoryRoot != ""` (`NewGateway`, `internal/slack/gateway.go`).
- A non-nil `cfg.Messenger` lets us construct/use the gateway without a bot token.
- `agent.Dispatch` runs the work in a goroutine, so the test must wait for the reply via the
  fake messenger.
- The prompt template (`buildPromptWithContext`) tells codex to reply in Chinese, so assertions
  must key on **language-neutral tokens** (numbers, an invented codeword), never English words
  like "ready"/"ok".

---

## 4. Files

| File | Change |
|---|---|
| `internal/slack/codex_integration_test.go` | **New.** `//go:build integration`, `package slack`. All harness + scenarios. |
| `.gitignore` | Add `codex_integration.test` (compiled binary artifact). |
| `AGENTS.md` (optional) | Note the documented compile-in-Docker / run-on-host exception for integration tests. |

No production code changes. If a needed seam turns out to be unreachable from `package slack`,
prefer adding a minimal test-only helper over widening production APIs — but `handleEvent`,
`NewGateway`, `agent.SessionStore`, and `agent.CodexBackend` are all reachable from
`package slack` already.

---

## 5. Test structure

Single top-level `TestCodexIntegration(t)` with ordered subtests run **sequentially** (no
`t.Parallel`) for readable logs and bounded concurrency.

### 5.1 Gating (top of `TestCodexIntegration`)

```go
if os.Getenv("CODEX_INTEGRATION_TEST") != "1" {
    t.Skip("set CODEX_INTEGRATION_TEST=1 to run the codex integration test")
}
```
Combined with `//go:build integration`, this is the double safety: CI's `go test ./...` lacks
the tag; `-tags integration` alone still skips without the env var.

### 5.2 Env knobs

| Env var | Default | Purpose |
|---|---|---|
| `CODEX_INTEGRATION_TEST` | unset | Must be `1` to run anything. |
| `CODEX_INTEGRATION_TIMEOUT` | `180s` | Per-codex-call timeout (`time.ParseDuration`). |
| `CODEX_INTEGRATION_MODEL` | unset | Optional model override for budget runs; default = machine default (no `-m`). |

### 5.3 Harness helpers

- **`fakeMessenger`** — implements `platform.Messenger`:
  - `replies chan capturedReply` (buffered, e.g. cap 8), where `capturedReply{event, text}`.
  - `Reply` and `Send` push onto the channel and return nil.
  - `waitForReply(t, d) string` — select on channel vs `time.After(d)`; `t.Fatal` on timeout.
- **`newCodexGateway(t) (*Gateway, *fakeMessenger, *agent.SessionStore, memoryRoot string)`**:
  - `tmp := t.TempDir()`; `workspace := tmp/workspace`, `memoryRoot := tmp/memory` (created).
  - Build `slack.Config`:
    ```
    BotUserID:    "U_BOT"
    Messenger:    fake
    EventLogPath: tmp/events.jsonl
    MemoryRoot:   memoryRoot
    Agent: agent.Config{
        Enabled:        true,
        Backend:        "codex",
        Workspace:      workspace,
        Model:          os.Getenv("CODEX_INTEGRATION_MODEL"), // "" => default
        SessionResume:  true,
        AckText:        "",                                   // suppress ack message
        ResultMaxChars: 200,
        Timeout:        <parsed timeout>,
    }
    ```
  - `g := NewGateway(cfg)`. Return `g`, fake, and the auto-wired `SessionStore`
    (`agent.NewSessionStore(filepath.Join(memoryRoot, ".state", "sessions.json"))`) plus
    `memoryRoot` for assertions.
- **`sendSlackMessage(t, g, channelID, threadTS, text)`** — build a raw Slack
  `message` event payload (JSON `json.RawMessage`) matching what `NormalizeEvent` expects:
  - Verify the exact shape against `internal/slack/events.go` / `NormalizeEvent` during
    implementation. Minimum fields: a `message` event with `channel`, `user` (≠ `U_BOT`),
    `text`, `ts`, and `thread_ts` (set to establish the thread; for turn 1 use the message `ts`
    as the thread root).
  - Call `g.handleEvent(context.Background(), payload)`; assert no error returned.

### 5.4 Subtest: `prerequisite`

Independent of the prompt-builder so an env failure is unambiguous:

```go
backend := agent.CodexBackend{}
ws := t.TempDir()
out, err := backend.Execute(ctx, agent.BackendRequest{
    Prompt:         "Reply with only the number 7 and nothing else.",
    Workspace:      ws,
    Model:          os.Getenv("CODEX_INTEGRATION_MODEL"),
    Binary:         "codex",
    TempDir:        t.TempDir(),
    ResultMaxChars: 200,
})
```
- On `err`: `t.Logf` the full error (includes codex stderr) and set a package-scoped
  `prereqOK=false`; then `t.Skip` here. Subsequent subtests check `prereqOK` and `t.Skip` if
  false. (Implementation detail: gate via a guard variable or by making prerequisite a hard
  precondition that, on failure, calls `t.Skip` and the parent stops scheduling — simplest is a
  bool guarded by the subtest running first.)
- On success: assert `out.Text` contains `"7"`. This confirms binary present, authenticated,
  and JSON/output parsing all work — without touching `~/.codex`.

### 5.5 Subtest: `basic_single_shot`

- Fresh gateway. `channel := "C_BASIC"`, thread = the message ts.
- `sendSlackMessage(... "用一个数字回答：2 + 2 等于几？只回复数字。")`.
- `reply := fake.waitForReply(t, timeout)`.
- Assert `strings.Contains(reply, "4")`. Proves end-to-end happy path + output parsing.

### 5.6 Subtest: `session_resume`

- Fresh gateway. `channel := "C_RESUME"`, `threadTS := "1750000000.000100"` (fixed root so both
  turns share a `SessionKey`).
- Turn 1: `sendSlackMessage(g, channel, threadTS, "记住这个暗号：BANANA47。只回复 OK。")`;
  `waitForReply`. Assert reply non-empty. Assert
  `store.Lookup({Provider:"slack", ChannelID:channel, ThreadTS:threadTS})` returns a non-empty
  session id (proves the thread.started id was persisted).
- Turn 2: `sendSlackMessage(g, channel, threadTS, "刚才的暗号是什么？只回复暗号本身。")`;
  `waitForReply`. Assert `strings.Contains(reply, "BANANA47")`. A fresh (non-resumed) session
  could not know the codeword, so a pass proves resume carried the context.

### 5.7 Subtest: `resume_fallback`

- Fresh gateway. `channel := "C_FALLBACK"`, `threadTS := "1750000000.000200"`.
- Pre-seed the store with a bogus id:
  `store.Put({"slack", channel, threadTS}, "00000000-dead-beef-0000-000000000000")`.
- `sendSlackMessage(g, channel, threadTS, "回复数字 9。")`; `waitForReply`.
- Assertions:
  - `strings.Contains(reply, "9")` — fallback produced a valid answer.
  - `store.Lookup(key)` returns a **new, non-empty id different** from the bogus one — the store
    was repaired with the fresh session's id.
- Proves `Runner.execute`'s resume-error → fresh-session fallback path
  (`internal/agent/codex.go` lines ~174–191).

---

## 6. Token / cost budget

| Subtest | codex model calls |
|---|---|
| prerequisite | 1 |
| basic_single_shot | 1 |
| session_resume | 2 |
| resume_fallback | 1 (the bogus-id resume fails fast, locally, before a model call) |
| **Total** | **~5** |

All prompts are <40 chars of payload, answers are single tokens, `ResultMaxChars=200`. No model
override means default model; budget-conscious runs can set `CODEX_INTEGRATION_MODEL`.

---

## 7. Implementation order (TDD-friendly)

1. Add `codex_integration.test` to `.gitignore`.
2. Create the file with build tag, gating, env knobs, and `fakeMessenger`.
3. Implement `newCodexGateway` + `sendSlackMessage`; verify the payload shape against
   `NormalizeEvent` (write a quick non-codex assertion that `handleEvent` yields a captured
   reply path — can stub by asserting the entry reaches dispatch; or just rely on the codex
   subtests). Confirm the JSON keys by reading `internal/slack/events.go`.
4. Implement `prerequisite` subtest; run it alone first:
   `CODEX_INTEGRATION_TEST=1 ./codex_integration.test -test.v -test.run TestCodexIntegration/prerequisite`.
5. Add `basic_single_shot`, then `session_resume`, then `resume_fallback`.
6. Full run; tune timeout if codex is slow on this machine.

---

## 8. Verification

- **Compiles in Docker, excluded from normal builds:**
  `docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test -c -tags integration -o codex_integration.test ./internal/slack`
- **Excluded from CI / default test run:**
  `docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...` must NOT run these
  (no tag) — confirm by output / timing.
- **Runs against real codex on host:**
  `CODEX_INTEGRATION_TEST=1 ./codex_integration.test -test.v -test.run TestCodexIntegration`
  — all subtests pass (or `prerequisite` skips with a printed error if the env is broken).
- `gofmt` via Docker on the new file.

---

## 9. Out of scope (YAGNI)

- The `agy` / multi-backend path (codex only).
- Real Slack HTTP / Socket Mode transport.
- Any CI wiring — explicitly local-only.
- A workspace-write file-creation scenario (heavier, less deterministic; revisit only if codex
  tool-use coverage is later desired).
