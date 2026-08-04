# Multi-Backend Per-Message Selection + Grok Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden `internal/agent` into a backend registry, add `GrokBackend`, and let chat messages pick a backend via a `/name` prefix that sticks for the rest of the thread.

**Architecture:** Replace the three parallel backend `switch` statements with a registry map. Parse an optional leading `/<backend>` directive per message; store the chosen backend on the existing `SessionRecord` so the pin and session lifecycle stay coupled. Resolve effective backend as directive → sticky pin → config default. Add `GrokBackend` as a combined-output CLI adapter like `AgyBackend`.

**Tech Stack:** Go 1.24, existing `internal/agent` / `internal/config` / gateway packages, Docker `golang:1.24` for `gofmt` / `go test` / `go build` per `CLAUDE.md`.

**Design source:** [`plans/20260804_multi-backend-design.md`](./20260804_multi-backend-design.md) (approved).

## Global Constraints

- Use Docker `golang:1.24` for all `gofmt`, `go test`, and `go build` (never host `go`).
- Focused package tests first: `./internal/agent` (plus config/gateway/slack when touched); full `go test ./...` before claiming done.
- Existing Codex- and Agy-only deployments with no directive and no config changes must behave identically.
- Unrecognized `/word` in a message is ordinary text (not stripped, not an error).
- Unregistered configured default backend is a **startup/config error**, not a silent fallback to `codex`.
- Empty configured default backend still means `codex`.
- Sticky pin and session share one `SessionRecord`; switching backends discards `SessionID` and builds a full-context prompt.
- `Put` must persist `Backend` even when `SessionID` is empty (required for sticky pin on agy/grok).
- Grok has no session-resume in this pass (`BackendResult.SessionID` stays empty).
- Directive prefix character is `/` only (no `!` aliases).
- No native Slack slash-command registration; no per-user/channel allowlist.

### Resolved open question: Grok CLI contract

Validated locally on 2026-08-04 against `grok 0.2.118`:

```bash
grok -p "reply with exactly: OK" --cwd /tmp --output-format plain --always-approve
# → prints OK, exit 0
```

Final non-interactive argv for `GrokBackend` (not the design doc's placeholder `--add-dir`/`--prompt`):

```text
<binary> --cwd <workspace> --output-format plain --always-approve [-m <model>] -p <prompt> [extra args...]
```

### Resolved open question: SessionStore availability for pins

Today Slack only creates `SessionStore` when `SessionResume && MemoryRoot != ""`. Sticky pins need the store even when resume is off (agy/grok never produce a session ID). Wire `SessionStore` whenever `MemoryRoot` is non-empty (Slack) or a config-dir state path is available (Lark), independent of `SessionResume`. `SessionResume` continues to gate only whether `SessionID` is passed into `Backend.Execute` / stored for resume.

---

## File Map

| File | Role |
|------|------|
| `internal/agent/backend.go` | Registry (`backends`, `backendAliases`, `Resolve`, `RegisteredBackendNames`, `backendLabel`, binary resolution with `GrokBinary`) |
| `internal/agent/backend_test.go` | Registry unit tests (new file) |
| `internal/agent/directive.go` | `ParseBackendDirective` (new) |
| `internal/agent/directive_test.go` | Directive table tests (new) |
| `internal/agent/grok.go` | `GrokBackend` (new) |
| `internal/agent/grok_test.go` | Fake-binary argv/output tests (new) |
| `internal/agent/session.go` | `Backend` on `SessionRecord`; `LookupRecord`; `Put(key, backend, sessionID)` |
| `internal/agent/session_test.go` | Backend pin round-trip + legacy JSON decode |
| `internal/agent/codex.go` | Runner: directive + pin + effective backend; `Config.GrokBinary`; validate default |
| `internal/agent/codex_test.go` | Runner-level directive/pin/switch tests; update registry-related tests |
| `internal/config/config.go` | `grok_binary` fields, defaults, env, getters |
| `internal/config/*_test.go` | Config getter coverage for `grok_binary` |
| `internal/gateway/service.go` | Wire `GrokBinary`, SessionStore path, default-backend validation |
| `internal/slack/gateway.go` | Wire `GrokBinary`; SessionStore when MemoryRoot set (not only SessionResume) |
| `internal/cmd/gateway.go`, `internal/cmd/slack.go` | Flag help text; pass `GrokBinary`; fail-fast on bad default |
| `config.example.yaml`, `USER_GUIDE.md`, `USAGE.md`, `README.md` | Document `/grok` directive, `grok` backend, `grok_binary` |

---

### Task 1: Backend registry

**Files:**
- Modify: `internal/agent/backend.go`
- Create: `internal/agent/backend_test.go`
- Modify: `internal/agent/codex_test.go` (replace `TestNormalizeBackendName` / `TestResolveBackendSelectsAgy` callers)

**Interfaces:**
- Produces: `Resolve(name string) (Backend, bool)`, `RegisteredBackendNames() []string`, `backendLabel(name string) string`, updated `resolveBackend(cfg Config) (Backend, error)`, updated `resolveBackendBinary` with `cfg.GrokBinary`

- [ ] **Step 1: Write failing registry tests**

Create `internal/agent/backend_test.go`:

```go
package agent

import (
	"reflect"
	"testing"
)

func TestResolveRegisteredBackendsAndAliases(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"codex", "codex", true},
		{" CODEX ", "codex", true},
		{"agy", "agy", true},
		{"antigravity", "agy", true},
		{"antigravity-cli", "agy", true},
		{"grok", "grok", true},
		{"Grok", "grok", true},
		{"", "", false},
		{"unknown-value", "", false},
		{"claude", "", false},
	}
	for _, tc := range cases {
		b, ok := Resolve(tc.in)
		if ok != tc.ok {
			t.Fatalf("Resolve(%q) ok=%v, want %v", tc.in, ok, tc.ok)
		}
		if !tc.ok {
			continue
		}
		if b.Name() != tc.want {
			t.Fatalf("Resolve(%q).Name() = %q, want %q", tc.in, b.Name(), tc.want)
		}
	}
}

func TestRegisteredBackendNamesSorted(t *testing.T) {
	got := RegisteredBackendNames()
	want := []string{"agy", "codex", "grok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisteredBackendNames() = %#v, want %#v", got, want)
	}
}

func TestBackendLabel(t *testing.T) {
	if got := backendLabel("codex"); got != "本地 Codex 执行代理" {
		t.Fatalf("codex label = %q", got)
	}
	if got := backendLabel("agy"); got != "本地 Antigravity/agy 执行代理" {
		t.Fatalf("agy label = %q", got)
	}
	if got := backendLabel("grok"); got != "本地 Grok 执行代理" {
		t.Fatalf("grok label = %q", got)
	}
	if got := backendLabel("nope"); got != "本地执行代理" {
		t.Fatalf("unknown label = %q", got)
	}
}

func TestResolveBackendBinaryGrok(t *testing.T) {
	cfg := Config{GrokBinary: "custom-grok"}
	backend := testBackend{name: "grok", defaultBinary: "grok"}
	if got := resolveBackendBinary(cfg, backend); got != "custom-grok" {
		t.Fatalf("binary = %q, want custom-grok", got)
	}
	cfg = Config{Binary: "/opt/grok", GrokBinary: "custom-grok"}
	if got := resolveBackendBinary(cfg, backend); got != "/opt/grok" {
		t.Fatalf("neutral binary should win, got %q", got)
	}
}

func TestResolveBackendFromConfig(t *testing.T) {
	b, err := resolveBackend(Config{Backend: "agy"})
	if err != nil || b.Name() != "agy" {
		t.Fatalf("agy: b=%v err=%v", b, err)
	}
	b, err = resolveBackend(Config{Backend: ""})
	if err != nil || b.Name() != "codex" {
		t.Fatalf("empty default: b=%v err=%v", b, err)
	}
	_, err = resolveBackend(Config{Backend: "typo-name"})
	if err == nil {
		t.Fatal("expected error for unregistered default backend")
	}
}
```

Also update `TestNormalizeBackendName` in `codex_test.go`: delete it (behavior moves to `TestResolveRegisteredBackendsAndAliases`). Update `TestResolveBackendSelectsAgy` to use `resolveBackend` returning `(Backend, error)`. Keep `testBackend` helper in `codex_test.go` (or move to `backend_test.go` if only used there after edits — either is fine; avoid duplicate definitions).

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestResolveRegistered|TestRegisteredBackend|TestBackendLabel|TestResolveBackendBinaryGrok|TestResolveBackendFromConfig' -count=1
```

Expected: FAIL (missing `Resolve`, `GrokBackend` may not compile yet — implement registry first with a stub `GrokBackend` if needed, or register only codex/agy until Task 3; prefer registering all three names and stub `GrokBackend` in `backend.go` as empty struct with Name/DefaultBinary/Execute panic-or-not-implemented only if Task 3 is immediate next — **better: register only codex+agy in Task 1, add grok in Task 3**. Adjust tests above: for Task 1, `RegisteredBackendNames` want `[]string{"agy","codex"}` and drop `"grok"` cases; Task 3 extends them.)

**Task 1 final test expectations (before Grok lands):**

- Resolve: codex/agy/aliases ok; empty/unknown not ok
- `RegisteredBackendNames()` = `["agy","codex"]`
- `backendLabel("grok")` can return generic `"本地执行代理"` until Task 3
- `TestResolveBackendBinaryGrok` and grok Resolve cases land in **Task 3**

Use this Task-1-only test body for Resolve/Registered:

```go
// want names: agy, codex only
// Resolve("grok") → ok=false until Task 3
```

- [ ] **Step 3: Implement the registry**

Replace `normalizeBackendName` / `resolveBackend` / `backendLabel` switch bodies in `internal/agent/backend.go` with:

```go
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yjwong/lark-cli/internal/inbound"
)

// ... BackendResult, Backend, BackendRequest unchanged ...

var backends = map[string]Backend{
	"codex": CodexBackend{},
	"agy":   AgyBackend{},
	// "grok" added in Task 3
}

var backendAliases = map[string]string{
	"antigravity":     "agy",
	"antigravity-cli": "agy",
}

// Resolve returns a registered backend by name or alias.
// Empty and unknown names return ok=false (callers treat empty config as "codex" via resolveBackend).
func Resolve(name string) (Backend, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return nil, false
	}
	if real, ok := backendAliases[key]; ok {
		key = real
	}
	b, ok := backends[key]
	return b, ok
}

func RegisteredBackendNames() []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveBackend(cfg Config) (Backend, error) {
	name := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if name == "" {
		name = "codex"
	}
	b, ok := Resolve(name)
	if !ok {
		return nil, fmt.Errorf("未知后端: %s，可用: %s", name, strings.Join(RegisteredBackendNames(), ", "))
	}
	return b, nil
}

func resolveBackendBinary(cfg Config, backend Backend) string {
	if strings.TrimSpace(cfg.Binary) != "" {
		return strings.TrimSpace(cfg.Binary)
	}
	switch backend.Name() {
	case "codex":
		if strings.TrimSpace(cfg.CodexBinary) != "" {
			return strings.TrimSpace(cfg.CodexBinary)
		}
	case "grok":
		if strings.TrimSpace(cfg.GrokBinary) != "" {
			return strings.TrimSpace(cfg.GrokBinary)
		}
	}
	return backend.DefaultBinary()
}

func backendLabel(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if real, ok := backendAliases[key]; ok {
		key = real
	}
	switch key {
	case "codex":
		return "本地 Codex 执行代理"
	case "agy":
		return "本地 Antigravity/agy 执行代理"
	case "grok":
		return "本地 Grok 执行代理"
	default:
		return "本地执行代理"
	}
}

func splitArgs(args []string) []string { /* unchanged */ }
```

Add `GrokBinary string` to `Config` in `codex.go` (field only; no behavior yet).

Update every `resolveBackend(r.cfg)` call site to handle error — for Task 1, only `Runner.execute` uses it; temporary:

```go
backend, err := resolveBackend(r.cfg)
if err != nil {
	return "", err
}
```

Update `TestResolveBackendSelectsAgy` and any other callers.

Delete `normalizeBackendName` entirely; fix all references.

- [ ] **Step 4: Format and run agent tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/*.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/backend.go internal/agent/backend_test.go internal/agent/codex.go internal/agent/codex_test.go
git commit -m "$(cat <<'EOF'
feat(agent): replace backend switches with a registry

Centralize backend resolution in Resolve/RegisteredBackendNames so new
backends are one map entry. Unknown configured defaults now error instead
of silently falling back to codex.
EOF
)"
```

---

### Task 2: SessionRecord backend pin + store API

**Files:**
- Modify: `internal/agent/session.go`
- Modify: `internal/agent/session_test.go`
- Modify: `internal/agent/codex.go` (call sites of `Put` / `Lookup`)
- Modify: `internal/agent/codex_test.go` (Put signature)
- Modify: `internal/slack/gateway.go` if it calls Put/Lookup/Remove only — Remove signature stays; Put call sites only in agent package today

**Interfaces:**
- Consumes: existing `SessionStore`
- Produces:
  - `SessionRecord.Backend string \`json:"backend,omitempty"\``
  - `LookupRecord(key SessionKey) (SessionRecord, bool, error)`
  - `Put(key SessionKey, backend, sessionID string) error` — persists when **either** backend or sessionID is non-empty
  - `Lookup(key) (string, error)` remains as thin wrapper returning `SessionID` only (keeps tests simpler)

- [ ] **Step 1: Write failing session tests**

Append to `session_test.go`:

```go
func TestSessionStorePutAndLookupRecordWithBackend(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	key := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"}

	if err := store.Put(key, "grok", ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	rec, ok, err := store.LookupRecord(key)
	if err != nil || !ok {
		t.Fatalf("LookupRecord: ok=%v err=%v", ok, err)
	}
	if rec.Backend != "grok" || rec.SessionID != "" {
		t.Fatalf("record = %+v", rec)
	}

	if err := store.Put(key, "codex", "sess-1"); err != nil {
		t.Fatalf("Put update: %v", err)
	}
	rec, ok, err = store.LookupRecord(key)
	if err != nil || !ok {
		t.Fatalf("LookupRecord after update: ok=%v err=%v", ok, err)
	}
	if rec.Backend != "codex" || rec.SessionID != "sess-1" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestSessionStoreLegacyJSONWithoutBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	legacy := `{
  "sessions": [
    {
      "key": {"provider": "slack", "channel_id": "C1", "thread_ts": "1.0"},
      "session_id": "legacy-sess",
      "created_at": "2026-01-01T00:00:00Z",
      "updated_at": "2026-01-01T00:00:00Z"
    }
  ]
}
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	store := NewSessionStore(path)
	rec, ok, err := store.LookupRecord(SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"})
	if err != nil || !ok {
		t.Fatalf("LookupRecord: ok=%v err=%v", ok, err)
	}
	if rec.Backend != "" || rec.SessionID != "legacy-sess" {
		t.Fatalf("legacy record = %+v (Backend should be empty/unpinned)", rec)
	}
}
```

Update existing `store.Put(key, "id")` calls in `session_test.go` and `codex_test.go` to `store.Put(key, "", "id")` or `store.Put(key, "codex", "id")` as appropriate (empty backend is fine for old session-only tests).

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestSessionStorePutAndLookupRecord|TestSessionStoreLegacy' -count=1
```

Expected: FAIL (wrong Put signature / missing LookupRecord).

- [ ] **Step 3: Implement session API**

```go
type SessionRecord struct {
	Key       SessionKey `json:"key"`
	Backend   string     `json:"backend,omitempty"`
	SessionID string     `json:"session_id"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
}

func (s *SessionStore) LookupRecord(key SessionKey) (SessionRecord, bool, error) {
	if !s.Enabled() {
		return SessionRecord{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return SessionRecord{}, false, err
	}
	idx := findSession(state.Sessions, key)
	if idx < 0 {
		return SessionRecord{}, false, nil
	}
	return state.Sessions[idx], true, nil
}

func (s *SessionStore) Lookup(key SessionKey) (string, error) {
	rec, ok, err := s.LookupRecord(key)
	if err != nil || !ok {
		return "", err
	}
	return rec.SessionID, nil
}

// Put upserts the record. Persists when backend or sessionID is non-empty after trim.
// Empty sessionID clears any previous session id while keeping/updating backend.
func (s *SessionStore) Put(key SessionKey, backend, sessionID string) error {
	if !s.Enabled() {
		return nil
	}
	backend = strings.TrimSpace(backend)
	sessionID = strings.TrimSpace(sessionID)
	if backend == "" && sessionID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339Nano)
	idx := findSession(state.Sessions, key)
	if idx < 0 {
		state.Sessions = append(state.Sessions, SessionRecord{
			Key:       key,
			Backend:   backend,
			SessionID: sessionID,
			CreatedAt: now,
			UpdatedAt: now,
		})
	} else {
		if backend != "" {
			state.Sessions[idx].Backend = backend
		}
		// Always write sessionID (may clear on backend switch).
		state.Sessions[idx].SessionID = sessionID
		state.Sessions[idx].UpdatedAt = now
	}
	return s.saveLocked(state)
}
```

Update `Runner.execute` Put call to three-arg form temporarily still using empty backend until Task 4:

```go
_ = r.cfg.SessionStore.Put(sessionKeyFromEntry(entry), "", result.SessionID)
```

(or pass `backend.Name()` early if already available).

- [ ] **Step 4: Format and run agent tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/*.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/session.go internal/agent/session_test.go internal/agent/codex.go internal/agent/codex_test.go
git commit -m "$(cat <<'EOF'
feat(agent): persist sticky backend pin on SessionRecord

Extend SessionStore with LookupRecord and a Put that keeps backend even
when session ID is empty, so agy/grok threads can pin without resume.
EOF
)"
```

---

### Task 3: GrokBackend

**Files:**
- Create: `internal/agent/grok.go`
- Create: `internal/agent/grok_test.go`
- Modify: `internal/agent/backend.go` (register `"grok": GrokBackend{}`)
- Modify: `internal/agent/backend_test.go` (add grok Resolve / names / binary cases)

**Interfaces:**
- Produces: `GrokBackend` implementing `Backend`
- CLI: `binary --cwd workspace --output-format plain --always-approve [-m model] -p prompt [args...]`

- [ ] **Step 1: Write failing Grok tests**

```go
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yjwong/lark-cli/internal/inbound"
)

func TestGrokBackendExecutePassesWorkspacePromptModelAndArgs(t *testing.T) {
	path := fakeGrokExecutable(t, 0, "grok final output")
	workspace := t.TempDir()
	backend := GrokBackend{}

	result, err := backend.Execute(context.Background(), BackendRequest{
		Entry:          inbound.LoggedEvent{MessageText: "inspect"},
		Prompt:         "prompt text",
		Workspace:      workspace,
		Model:          "grok-test-model",
		Binary:         path,
		Args:           []string{"--no-memory"},
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Text != "grok final output" {
		t.Fatalf("result.Text = %q", result.Text)
	}
	if result.SessionID != "" {
		t.Fatalf("SessionID should be empty for grok, got %q", result.SessionID)
	}

	argsData, err := os.ReadFile(filepath.Join(filepath.Dir(path), "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(argsData)
	for _, want := range []string{
		"--cwd", workspace,
		"--output-format", "plain",
		"--always-approve",
		"-m", "grok-test-model",
		"-p", "prompt text",
		"--no-memory",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
}

func TestGrokBackendExecuteReturnsFailureOutput(t *testing.T) {
	backend := GrokBackend{}
	_, err := backend.Execute(context.Background(), BackendRequest{
		Prompt:         "prompt text",
		Workspace:      t.TempDir(),
		Binary:         fakeGrokExecutable(t, 7, "grok failed"),
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "grok failed") {
		t.Fatalf("error = %v, want grok failed", err)
	}
}

func TestResolveGrok(t *testing.T) {
	b, ok := Resolve("grok")
	if !ok || b.Name() != "grok" {
		t.Fatalf("Resolve(grok) = %v ok=%v", b, ok)
	}
	names := RegisteredBackendNames()
	if len(names) != 3 || names[0] != "agy" || names[1] != "codex" || names[2] != "grok" {
		t.Fatalf("names = %#v", names)
	}
}

func fakeGrokExecutable(t *testing.T, exitCode int, output string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-grok")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%s' %q\nexit %d\n",
		filepath.Join(dir, "args.txt"), output, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(fake grok): %v", err)
	}
	return path
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestGrokBackend|TestResolveGrok' -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement GrokBackend and register it**

`internal/agent/grok.go`:

```go
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type GrokBackend struct{}

func (GrokBackend) Name() string { return "grok" }

func (GrokBackend) DefaultBinary() string { return "grok" }

func (GrokBackend) Execute(ctx context.Context, req BackendRequest) (BackendResult, error) {
	args := []string{
		"--cwd", req.Workspace,
		"--output-format", "plain",
		"--always-approve",
	}
	if strings.TrimSpace(req.Model) != "" {
		args = append(args, "-m", strings.TrimSpace(req.Model))
	}
	args = append(args, "-p", req.Prompt)
	args = append(args, splitArgs(req.Args)...)

	cmd := exec.CommandContext(ctx, req.Binary, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return BackendResult{}, fmt.Errorf("%s", trimForChat(text, req.ResultMaxChars))
	}
	if text == "" {
		return BackendResult{}, fmt.Errorf("grok did not return output")
	}
	return BackendResult{Text: trimForChat(text, req.ResultMaxChars)}, nil
}
```

Register in `backends` map: `"grok": GrokBackend{}`.

- [ ] **Step 4: Format and run agent tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/*.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/grok.go internal/agent/grok_test.go internal/agent/backend.go internal/agent/backend_test.go
git commit -m "$(cat <<'EOF'
feat(agent): add GrokBackend for non-interactive grok CLI

Invoke grok with --cwd, -p, optional -m, plain output, and
--always-approve for unattended gateway use. No session resume yet.
EOF
)"
```

---

### Task 4: Directive parser

**Files:**
- Create: `internal/agent/directive.go`
- Create: `internal/agent/directive_test.go`

**Interfaces:**
- Produces: `ParseBackendDirective(text string) (backend string, rest string, ok bool)`
- Uses: `Resolve` from Task 1

- [ ] **Step 1: Write failing directive tests**

```go
package agent

import "testing"

func TestParseBackendDirective(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantBackend string
		wantRest    string
		wantOK      bool
	}{
		{"grok with rest", "/grok explain this", "grok", "explain this", true},
		{"codex", "/codex fix the bug", "codex", "fix the bug", true},
		{"agy alias", "/antigravity do it", "agy", "do it", true},
		{"case insensitive", "/Grok Hello", "grok", "Hello", true},
		{"directive only", "/grok", "grok", "", true},
		{"directive only whitespace rest", "/grok   ", "grok", "", true},
		{"unrecognized slash word", "/something-unrelated please", "", "/something-unrelated please", false},
		{"path like text", "/tmp/file.txt", "", "/tmp/file.txt", false},
		{"no slash", "grok explain", "", "grok explain", false},
		{"slash mid message", "please /grok this", "", "please /grok this", false},
		{"empty", "", "", "", false},
		{"leading spaces then directive", "  /agy hello", "agy", "hello", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend, rest, ok := ParseBackendDirective(tc.in)
			if ok != tc.wantOK || backend != tc.wantBackend || rest != tc.wantRest {
				t.Fatalf("ParseBackendDirective(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, backend, rest, ok, tc.wantBackend, tc.wantRest, tc.wantOK)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run TestParseBackendDirective -count=1
```

Expected: FAIL.

- [ ] **Step 3: Implement parser**

```go
package agent

import (
	"strings"
	"unicode"
)

// ParseBackendDirective recognizes a leading "/<backend>" token where <backend>
// resolves via Resolve (names + aliases). Unrecognized /word is left untouched.
func ParseBackendDirective(text string) (backend string, rest string, ok bool) {
	trimmedLeft := strings.TrimLeftFunc(text, unicode.IsSpace)
	if !strings.HasPrefix(trimmedLeft, "/") {
		return "", text, false
	}
	body := trimmedLeft[1:]
	if body == "" {
		return "", text, false
	}
	tokenEnd := strings.IndexFunc(body, unicode.IsSpace)
	token := body
	remainder := ""
	if tokenEnd >= 0 {
		token = body[:tokenEnd]
		remainder = strings.TrimSpace(body[tokenEnd+1:])
	}
	if token == "" {
		return "", text, false
	}
	b, found := Resolve(token)
	if !found {
		return "", text, false
	}
	return b.Name(), remainder, true
}
```

Note: return `b.Name()` (canonical), not the alias, so pins store `"agy"` not `"antigravity"`.

- [ ] **Step 4: Format and run agent tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/directive.go internal/agent/directive_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/directive.go internal/agent/directive_test.go
git commit -m "$(cat <<'EOF'
feat(agent): parse leading /backend directive from message text

Only registered backend names and aliases trigger a switch; any other
leading /word is left as ordinary message text.
EOF
)"
```

---

### Task 5: Runner effective-backend resolution + sticky pin

**Files:**
- Modify: `internal/agent/codex.go` (`Runner.execute`)
- Modify: `internal/agent/codex_test.go`

**Interfaces:**
- Consumes: `ParseBackendDirective`, `LookupRecord`, `Put`, `Resolve`, `resolveBackend`
- Behavior order: directive → record.Backend → cfg default
- On backend change vs record: drop SessionID, full prompt, info log
- Same backend + SessionResume + non-empty SessionID: resume path (unchanged)
- Always `Put(key, effectiveBackend, result.SessionID)` on success when store enabled (even if SessionID empty)
- SessionStore lookup for pin happens whenever store is enabled (not gated on SessionResume); SessionID use remains gated on SessionResume

- [ ] **Step 1: Write failing runner tests**

Add a small injectable/fake backend mechanism for runner tests. Prefer a package-level test hook **only if unavoidable**; cleaner approach: use real `AgyBackend`/`GrokBackend`/`CodexBackend` with fake binaries selected via `cfg.Binary` / `cfg.CodexBinary` / `cfg.GrokBinary` and `cfg.Backend` default.

Because per-message selection must pick different backends, `cfg.Binary` (single override) is awkward. Use empty neutral Binary and per-backend binary fields:

```go
// Fake executables write their backend identity into the reply text.
```

Implement three fake binaries (or one script that inspects argv[0] basename). Simplest: three scripts `fake-codex`, `fake-agy`, `fake-grok` each printing a distinct fixed string, and assert which one ran.

```go
func TestRunnerDirectiveSelectsBackendAndPins(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	messenger := &fakeMessenger{}
	agyBin := fakeAgyExecutable(t, 0, "from-agy")
	// ensure codex would produce different text if wrongly selected
	codexBin := fakeCodexExecutable(t)

	runner := NewRunnerWithMessenger(Config{
		Enabled:        true,
		Backend:        "codex",
		CodexBinary:    codexBin,
		Binary:         "", // must not force one binary for all backends
		Args:           nil,
		Workspace:      t.TempDir(),
		ResultMaxChars: 200,
		Timeout:        5 * time.Second,
		SessionResume:  true,
		SessionStore:   store,
	}, nil, messenger)
	// Problem: resolveBackendBinary for agy uses DefaultBinary "agy" not our fake.
	// Fix for test: set cfg.Binary only when testing single backend OR
	// temporarily put fake agy on PATH. Better fix used in tests:
	// t.Setenv("PATH", dirWithFakes+":"+os.Getenv("PATH")) and name scripts "agy"/"grok"/"codex".
}
```

**Concrete PATH-based approach (use this):**

```go
func installFakeBackendBins(t *testing.T, outputs map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, out := range outputs {
		// For codex, script must satisfy fakeCodexExecutable contract (JSON + output file).
		// Prefer reusing existing fakeCodexExecutable for codex path by wrapping:
		//   ln/cp is messy. Instead: only assert agy vs grok for directive tests,
		//   and use CodexBackend only when SessionResume path is needed.
	}
}
```

Pragmatic test matrix:

1. **Directive overrides default (agy over codex):** PATH inject `agy` fake; message `/agy hello`; default Backend `codex` with real-shaped codex fake that would fail if invoked; assert result text is agy output; assert `LookupRecord.Backend == "agy"`.

2. **Sticky pin without directive:** After (1), second message `"continue"` with no prefix uses agy again (not codex).

3. **Switch discards session:** First message establishes codex session via existing resume fake; second message `/agy next` uses agy and `LookupRecord.SessionID == ""` with Backend `agy`.

4. **Unrecognized slash passthrough:** Message `/not-a-backend x` with default codex still runs codex (full prompt includes the literal text).

5. **Bad configured default:** `resolveBackend(Config{Backend:"nope"})` already covered; optional `NewRunner` validation deferred to Task 6.

Skeleton for (1)+(2):

```go
func TestRunnerDirectivePinsBackendForThread(t *testing.T) {
	dir := t.TempDir()
	// fake agy
	agyPath := filepath.Join(dir, "agy")
	script := "#!/bin/sh\nprintf '%s' 'from-agy'\n"
	os.WriteFile(agyPath, []byte(script), 0o755)
	// fake codex that fails hard if called
	codexPath := filepath.Join(dir, "codex")
	os.WriteFile(codexPath, []byte("#!/bin/sh\necho 'codex-should-not-run' >&2\nexit 99\n"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	runner := NewRunnerWithMessenger(Config{
		Enabled: true, Backend: "codex", CodexBinary: codexPath,
		Workspace: t.TempDir(), ResultMaxChars: 200, Timeout: 5 * time.Second,
		SessionResume: true, SessionStore: store,
	}, nil, &fakeMessenger{})

	entry := inbound.LoggedEvent{
		Provider: "slack", ChannelID: "C1", ThreadID: "1.0",
		MessageID: "1.0", UserID: "U1", MessageText: "/agy first task",
	}
	out, err := runner.execute(entry)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "from-agy" {
		t.Fatalf("out = %q", out)
	}
	rec, ok, _ := store.LookupRecord(sessionKeyFromEntry(entry))
	if !ok || rec.Backend != "agy" {
		t.Fatalf("pin = %+v ok=%v", rec, ok)
	}

	entry2 := entry
	entry2.MessageID = "2.0"
	entry2.MessageText = "second without directive"
	out2, err := runner.execute(entry2)
	if err != nil {
		t.Fatalf("execute2: %v", err)
	}
	if out2 != "from-agy" {
		t.Fatalf("sticky out = %q", out2)
	}
}
```

Add `TestRunnerBackendSwitchClearsSessionID` using the existing resume fake codex then `/agy` switch.

Add `TestRunnerUnrecognizedDirectivePassthrough` with codex fake succeeding and prompt capture containing `/not-a-backend`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestRunnerDirective|TestRunnerBackendSwitch|TestRunnerUnrecognized' -count=1
```

Expected: FAIL (runner still uses fixed config backend).

- [ ] **Step 3: Implement Runner.execute resolution**

Replace the start of `execute` with:

```go
func (r *Runner) execute(entry inbound.LoggedEvent) (string, error) {
	tempDir, err := os.MkdirTemp("", "lark-agent-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	messageText := entry.MessageText
	directiveBackend, cleanText, hasDirective := ParseBackendDirective(messageText)
	if hasDirective {
		messageText = cleanText
		entry.MessageText = cleanText // prompt builders and backends see stripped text
	}

	var record SessionRecord
	var hasRecord bool
	if r.cfg.SessionStore != nil && r.cfg.SessionStore.Enabled() {
		key := sessionKeyFromEntry(entry)
		rec, ok, lookupErr := r.cfg.SessionStore.LookupRecord(key)
		if lookupErr != nil {
			r.logger.Printf("session lookup failed for message_id=%s: %v", entry.MessageID, lookupErr)
		} else {
			record, hasRecord = rec, ok
		}
	}

	effectiveName := strings.TrimSpace(r.cfg.Backend)
	if effectiveName == "" {
		effectiveName = "codex"
	}
	if hasRecord && strings.TrimSpace(record.Backend) != "" {
		effectiveName = record.Backend
	}
	if hasDirective {
		effectiveName = directiveBackend
	}

	backend, ok := Resolve(effectiveName)
	if !ok {
		// Only reachable if config default was not validated and no pin/directive.
		return "", fmt.Errorf("未知后端: %s，可用: %s", effectiveName, strings.Join(RegisteredBackendNames(), ", "))
	}

	backendChanged := hasRecord && strings.TrimSpace(record.Backend) != "" && record.Backend != backend.Name()
	if hasDirective && hasRecord && record.Backend != "" && record.Backend != backend.Name() {
		backendChanged = true
	}
	// Also treat first pin from unpinned record as not a "switch" of session:
	sessionID := ""
	if r.cfg.SessionResume && hasRecord && !backendChanged && record.Backend == backend.Name() {
		sessionID = record.SessionID
	}
	// If record has session but empty backend (legacy), allow resume with config default backend:
	if r.cfg.SessionResume && hasRecord && strings.TrimSpace(record.Backend) == "" && !hasDirective {
		sessionID = record.SessionID
	}
	if backendChanged {
		r.logger.Printf("thread %s switching backend %s → %s, starting fresh session",
			sessionKeyFromEntry(entry).ThreadTS, record.Backend, backend.Name())
		sessionID = ""
	}

	var prompt string
	if sessionID != "" {
		prompt = messageText
	} else {
		// buildFullPrompt uses entry.MessageText (already cleaned)
		prompt = r.buildFullPrompt(entry, backend)
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.Timeout)
	defer cancel()

	result, err := backend.Execute(ctx, BackendRequest{
		Entry:          entry,
		Prompt:         prompt,
		Workspace:      r.cfg.Workspace,
		Model:          r.cfg.Model,
		Binary:         resolveBackendBinary(r.cfg, backend),
		Args:           splitArgs(r.cfg.Args),
		ResultMaxChars: r.cfg.ResultMaxChars,
		TempDir:        tempDir,
		SessionID:      sessionID,
	})

	// existing resume-fallback block: on error with sessionID, Remove is wrong if we only want to clear id —
	// keep Remove for full record clear on failed resume, then retry full prompt with same backend.
	// ...

	if err != nil {
		// timeout handling unchanged
		return "", err
	}

	if r.cfg.SessionStore != nil && r.cfg.SessionStore.Enabled() {
		// Always pin effective backend; store session id only when non-empty (Put writes both).
		sid := result.SessionID
		if !r.cfg.SessionResume {
			sid = "" // do not persist resume ids when feature off; still pin backend
		}
		if putErr := r.cfg.SessionStore.Put(sessionKeyFromEntry(entry), backend.Name(), sid); putErr != nil {
			r.logger.Printf("failed to store session for message_id=%s: %v", entry.MessageID, putErr)
		}
	}

	return trimForChat(result.Text, r.cfg.ResultMaxChars), nil
}
```

**Careful details implementers must get right:**

1. Mutating `entry.MessageText` for the cleaned prompt is intentional so `buildFullPrompt` and observers of the request see the user prompt without the directive token.
2. Resume fallback on Execute error: still clear session and retry with full prompt on the **same** effective backend (do not drop the pin). Prefer `Put(key, backend.Name(), "")` over `Remove` so the pin survives a failed resume — update existing fallback test if behavior changes from Remove to clear-id-only.
3. When `SessionResume` is false, never pass `sessionID` into Execute; still read/write Backend pin.
4. Keep existing tests for pure codex resume green.

Recommended clean sessionID selection logic (prefer this over the pseudo-code above):

```go
effective := configDefaultBackendName(r.cfg.Backend) // "" → "codex"
if hasRecord && record.Backend != "" {
	effective = record.Backend
}
if hasDirective {
	effective = directiveBackend
}
backend, ok := Resolve(effective)
// ...
useSessionID := ""
if r.cfg.SessionResume && hasRecord && record.SessionID != "" {
	// Resume only if the record's backend is empty (legacy) or matches effective.
	if record.Backend == "" || record.Backend == backend.Name() {
		useSessionID = record.SessionID
	}
}
if hasDirective && hasRecord && record.Backend != "" && record.Backend != backend.Name() {
	r.logger.Printf("thread %s switching backend %s → %s, starting fresh session",
		sessionKeyFromEntry(entry).ThreadTS, record.Backend, backend.Name())
	useSessionID = ""
}
```

- [ ] **Step 4: Format and run agent tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/*.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -count=1
```

Expected: PASS (including old resume tests).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/codex.go internal/agent/codex_test.go
git commit -m "$(cat <<'EOF'
feat(agent): resolve backend per message with sticky thread pin

Honor a leading /backend directive, remember the choice on the session
record, and discard incompatible session IDs when the pin changes.
EOF
)"
```

---

### Task 6: Config, gateway wiring, fail-fast validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/slack_config_test.go` (and/or agent config tests)
- Modify: `internal/agent/codex.go` (`Config.GrokBinary` already added)
- Modify: `internal/gateway/service.go`, `internal/gateway/service_test.go`
- Modify: `internal/slack/gateway.go`, `internal/slack/gateway_test.go`
- Modify: `internal/cmd/gateway.go`, `internal/cmd/slack.go`

**Interfaces:**
- Produces: `GetAgentGrokBinary()`, `GetSlackAgentGrokBinary()`
- Env: `LARK_AGENT_GROK_BINARY`, `SLACK_AGENT_GROK_BINARY`
- YAML: `agent.grok_binary`, `slack.agent.grok_binary` default `"grok"`
- SessionStore wiring: Slack when `MemoryRoot != ""` (drop SessionResume requirement)
- Lark: if no MemoryRoot, wire SessionStore under `filepath.Join(config.GetConfigDir(), "agent-sessions.json")` when agent enabled + session_resume **or always when agent enabled** — choose **always when agent enabled** so pins work without resume flag
- Fail-fast: validate `resolveBackend` / `Resolve` of configured default at gateway serve start (cmd Run) and/or `NewRunner` — prefer both cmd paths print clear error and `os.Exit(1)` / return error from Serve setup

- [ ] **Step 1: Write failing config/gateway tests**

Extend `TestAgentBackendConfigFromYAML` (or add sibling) to assert `GetAgentGrokBinary()` / `GetSlackAgentGrokBinary()` from YAML and env override.

Extend `TestDefaultAgentConfigIncludesBackendFields` (gateway + slack) to pass through `GrokBinary`.

Add test that Slack `NewGateway` creates SessionStore when MemoryRoot set even if SessionResume is false (inspect via executing a directive pin if store is unexported — alternatively export nothing and test via agent execute with pin). Simplest unit test: extract store path decision into a small helper `sessionStorePath(memoryRoot string) string` and test that `New` sets `cfg.Agent.SessionStore` when MemoryRoot non-empty. If `SessionStore` is on agent.Config, tests can check `gw` fields — Gateway may not export agent; use package-level test in `slack`:

```go
func TestNewGatewayWiresSessionStoreWithoutResume(t *testing.T) {
	// construct Config with Agent.Enabled, MemoryRoot=temp, SessionResume=false
	// call NewGateway...
	// assert g.cfg.Agent.SessionStore.Enabled()
}
```

(Requires test in `package slack` accessing unexported fields — existing tests already in that package.)

- [ ] **Step 2: Run tests to verify they fail**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/config ./internal/gateway ./internal/slack -count=1 -run 'Grok|SessionStore|Backend'
```

Expected: FAIL.

- [ ] **Step 3: Implement config + wiring**

In `config.go`:

```go
// both Agent structs:
GrokBinary string `mapstructure:"grok_binary"`

// defaults:
viper.SetDefault("agent.grok_binary", "grok")
viper.SetDefault("slack.agent.grok_binary", "grok")

// env:
viper.BindEnv("agent.grok_binary", "LARK_AGENT_GROK_BINARY")
viper.BindEnv("slack.agent.grok_binary", "SLACK_AGENT_GROK_BINARY")

func GetAgentGrokBinary() string {
	return strings.TrimSpace(viper.GetString("agent.grok_binary"))
}
func GetSlackAgentGrokBinary() string {
	return strings.TrimSpace(viper.GetString("slack.agent.grok_binary"))
}
```

Wire into `DefaultAgentConfig` / `DefaultAgentConfigInput` / cmd flags optional (`--agent-grok-binary` not required if env/yaml suffice; mirror codex: codex has field but not always a dedicated flag — check: only `codex_binary` via config, neutral `--agent-binary`. **No new CLI flag required**; config/env is enough. Update `--agent-backend` help to `codex, agy, or grok`.

Slack gateway SessionStore:

```go
if strings.TrimSpace(cfg.MemoryRoot) != "" {
	cfg.Agent.SessionStore = agent.NewSessionStore(
		filepath.Join(cfg.MemoryRoot, ".state", "sessions.json"),
	)
}
```

Lark gateway `New` or `DefaultAgentConfig` path in `cmd/gateway.go`:

```go
agentCfg := gateway.DefaultAgentConfig()
agentCfg.GrokBinary = config.GetAgentGrokBinary()
agentCfg.SessionResume = config.GetAgentSessionResume()
if agentCfg.Enabled {
	agentCfg.SessionStore = agent.NewSessionStore(filepath.Join(config.GetConfigDir(), "agent-sessions.json"))
}
if _, err := agent.ValidateDefaultBackend(agentCfg.Backend); err != nil {
	// fail fast — implement ValidateDefaultBackend as thin wrapper around resolveBackend
}
```

Export:

```go
func ValidateDefaultBackend(name string) error {
	_, err := resolveBackend(Config{Backend: name})
	return err
}
```

Call from both `gateway serve` and `slack gateway serve` before starting.

- [ ] **Step 4: Format and run focused tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/config/*.go internal/gateway/*.go internal/slack/*.go internal/cmd/*.go internal/agent/*.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent ./internal/config ./internal/gateway ./internal/slack ./internal/cmd -count=1
```

Expected: PASS (skip `./internal/cmd` if no tests).

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/gateway internal/slack internal/cmd internal/agent
git commit -m "$(cat <<'EOF'
feat: wire grok_binary, session pins, and backend validation

Add config/env for grok binary, create SessionStore for sticky pins even
when resume is off, and fail fast on an unknown default backend name.
EOF
)"
```

---

### Task 7: Docs + example config

**Files:**
- Modify: `config.example.yaml`
- Modify: `USER_GUIDE.md`
- Modify: `USAGE.md`
- Modify: `README.md` (only if it documents backend list)

- [ ] **Step 1: Update config.example.yaml**

```yaml
agent:
  backend: "codex"        # codex, agy, or grok; overridable per thread via /codex /agy /grok
  grok_binary: "grok"
slack:
  agent:
    backend: "codex"
    grok_binary: "grok"
```

- [ ] **Step 2: Document per-message selection**

In USER_GUIDE / USAGE near backend sections:

```markdown
### Per-thread backend selection

Prefix a message with a registered backend to pin the rest of the thread:

- `/grok explain this failure`
- `/agy refactor the helper`
- `/codex resume with codex`

An unrecognized `/word` is treated as normal text. The process-level
`agent.backend` default applies only when a thread has no pin yet.
```

- [ ] **Step 3: Commit**

```bash
git add config.example.yaml USER_GUIDE.md USAGE.md README.md
git commit -m "$(cat <<'EOF'
docs: document multi-backend directives and grok_binary

EOF
)"
```

---

### Task 8: Final verification

- [ ] **Step 1: Full test suite**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/*.go internal/config/*.go internal/gateway/*.go internal/slack/*.go internal/cmd/gateway.go internal/cmd/slack.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go build -ldflags "-s -w" -o ./lark ./cmd/lark
```

Expected: all PASS; binary builds.

- [ ] **Step 2: Mark design + implementation plan status**

Update `plans/20260804_multi-backend-design.md` status line to `Implemented` (or leave design immutable and only mark this plan's checkboxes). Prefer marking checkboxes in this file only.

- [ ] **Step 3: Final commit if any leftover**

Only if formatting or status edits remain.

---

## Acceptance Criteria Checklist

- [ ] Codex/Agy default path unchanged with no directive and no new config
- [ ] `/grok ...` / `/agy ...` / `/codex ...` select backend and pin thread
- [ ] Follow-up without directive uses pin; Codex resumes when SessionResume on
- [ ] Different directive switches pin and starts fresh session
- [ ] `/something-unrelated` is plain text
- [ ] Bad `agent.backend` fails at startup with registered names listed
- [ ] `gofmt` clean; `go test ./...` pass in Docker

## Spec Coverage (self-review)

| Design section | Task |
|----------------|------|
| 3.1 Registry | Task 1 |
| 3.2 Directive | Task 4 |
| 3.3 Sticky pin + session | Tasks 2, 5 |
| 3.4 GrokBackend | Task 3 |
| §4 Data flow | Task 5 |
| §5 Error handling | Tasks 1, 5, 6 |
| §6 Tests | Tasks 1–5 |
| §7 Config | Task 6–7 |
| §8 Acceptance | Task 8 |
| Open Q: grok CLI | Resolved in Global Constraints + Task 3 |
| Open Q: `/` only | Confirmed in Task 4 |

## Risks

- **Put semantics change** may break callers that relied on “empty sessionID no-ops”; all in-repo callers are updated in Task 2.
- **SessionStore without SessionResume** writes pin files where none existed before — same path as resume state; acceptable.
- **Grok flags** (`--always-approve`) auto-approve tools; same trust model as unattended agy/codex gateway use. Extra args remain available for operators who want different permission modes.
- **Neutral `cfg.Binary`** still overrides every backend; document that per-backend binaries (`codex_binary` / `grok_binary` / default names) should be preferred when mixing backends in one process.
