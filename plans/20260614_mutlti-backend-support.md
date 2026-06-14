# Multi Backend Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the local chat gateway configurable so inbound Lark/Slack tasks can run through Codex today and Antigravity CLI (`agy`) next, without hard-coding Codex-specific command construction into the shared runner.

**Architecture:** Split `internal/agent` into a transport runner and backend adapters. The runner keeps message filtering, ack/final replies, observers, prompt construction, timeout, and trimming; backend adapters own command-line contracts for `codex`, `agy`, and later `claude`/`pi`. Config remains backward compatible with existing `codex_binary` fields while adding neutral `backend`, `binary`, and `args` fields.

**Tech Stack:** Go 1.24, Cobra, Viper YAML/env config, existing `internal/agent`, `internal/gateway`, `internal/slack`, Docker `golang:1.24` for formatting/tests/build.

---

## Context

The current backend is embedded in `internal/agent/codex.go`:

- `agent.Config` exposes `CodexBinary`, `Workspace`, `Model`, `AckText`, `ResultMaxChars`, and `Timeout`.
- `Runner.execute()` always creates a temp `last-message.txt`, builds a Codex-specific prompt, and invokes:

```bash
codex -a never -s workspace-write exec \
  -C "$WORKSPACE" \
  --skip-git-repo-check \
  --output-last-message "$OUTPUT_FILE" \
  "$PROMPT"
```

- Lark and Slack gateway config builders both populate `agent.Config` with Codex-specific fields.
- `config.example.yaml`, CLI flag text, README, and user guide all describe `codex exec`.

Antigravity CLI is a reasonable first non-Codex target because its public README describes a terminal agent CLI named `agy`, official installation commands, local auth, and the same class of repository-level coding agent behavior. Public docs and recent Google Cloud material also position Antigravity CLI for terminal and headless/remote workflows, which matches this gateway's unattended process-per-message model. The exact `agy --prompt`/output contract should still be validated locally during implementation because the CLI is new and changing.

References checked on 2026-06-14:

- https://github.com/google-antigravity/antigravity-cli
- https://antigravity.google/product/antigravity-cli
- https://cloud.google.com/blog/topics/developers-practitioners/choosing-your-surface-antigravity-20-antigravity-cli-antigravity-ide-or-antigravity-sdk

## Scope

Implement backend configurability and the first `agy` adapter.

In scope:

- Preserve current Codex behavior as the default.
- Add neutral config fields for Lark and Slack agent backends.
- Add a backend registry inside `internal/agent`.
- Move Codex command construction into a Codex adapter.
- Add an Antigravity adapter that invokes `agy` in non-interactive mode after a local command-contract validation step.
- Add tests for backend selection, compatibility, command construction, prompt wording, and gateway config wiring.
- Update config examples and user-facing docs.

Out of scope for this first pass:

- Persistent interactive sessions per chat thread.
- Mixing multiple backends in one gateway process by channel/user routing.
- GUI Antigravity 2.0 orchestration.
- Claude Code and Pi adapters beyond documenting the extension point.
- Provider-specific prompt customization beyond replacing hard-coded "Codex" wording with the selected backend label.

## Design Decisions

### Backend Interface

Create a small backend interface in `internal/agent/backend.go`:

```go
type Backend interface {
	Name() string
	DefaultBinary() string
	Execute(ctx context.Context, req BackendRequest) (string, error)
}

type BackendRequest struct {
	Entry          inbound.LoggedEvent
	Prompt         string
	Workspace      string
	Model          string
	Binary         string
	Args           []string
	ResultMaxChars int
	TempDir        string
}
```

`Runner.execute()` will:

1. Resolve the backend once from config.
2. Build provider-neutral prompt text.
3. Create the temp directory.
4. Call `backend.Execute(ctx, req)`.
5. Trim the returned final answer for chat.

This keeps subprocess details out of reply/observer logic and avoids duplicating chat handling for every backend.

### Config Shape

Add neutral fields while retaining old names:

```yaml
agent:
  enabled: false
  backend: "codex"   # codex, agy
  binary: ""         # empty means backend default
  args: []           # appended backend-specific extra args
  codex_binary: "codex" # deprecated compatibility alias
  workspace: "~/WorkSpace"
  model: ""
  ack_text: "收到，开始处理。"
  result_max_chars: 1800
  timeout_minutes: 20

slack:
  agent:
    enabled: false
    backend: "codex"
    binary: ""
    args: []
    codex_binary: "codex"
    workspace: "~/WorkSpace"
    model: ""
    ack_text: "Received. Working on it."
    result_max_chars: 3500
    timeout_minutes: 20
```

Environment variables:

- Lark: `LARK_AGENT_BACKEND`, `LARK_AGENT_BINARY`, `LARK_AGENT_ARGS`
- Slack: `SLACK_AGENT_BACKEND`, `SLACK_AGENT_BINARY`, `SLACK_AGENT_ARGS`
- Existing `LARK_AGENT_CODEX_BINARY` and `SLACK_AGENT_CODEX_BINARY` continue to work for Codex.

Compatibility rule:

```text
backend defaults to "codex".
binary defaults to agent.binary.
if binary is empty and backend is codex, codex_binary is used.
if still empty, backend.DefaultBinary() is used.
```

For `args`, use comma-separated env values in config accessors, with whitespace trimming and empty-item removal.

### Codex Adapter

Move the existing command construction to `internal/agent/codex.go` or `internal/agent/backend_codex.go`:

```bash
<binary> [-m "$MODEL"] -a never -s workspace-write exec \
  -C "$WORKSPACE" \
  --skip-git-repo-check \
  --output-last-message "$TEMP_DIR/last-message.txt" \
  <extra args...> \
  "$PROMPT"
```

The adapter reads `last-message.txt` and returns that text. Existing tests with fake Codex binaries should keep passing after being renamed where useful from Codex-runner tests to backend tests.

### Antigravity (`agy`) Adapter

Create `internal/agent/backend_agy.go` with default binary `agy`.

Initial command contract:

```bash
<binary> "$WORKSPACE" --prompt "$PROMPT" [--model "$MODEL"] <extra args...>
```

The adapter captures combined stdout/stderr through `exec.CommandContext`. On success it returns trimmed stdout. On failure it returns trimmed combined output as the error message.

Before implementing this task, run a local contract check on the target machine:

```bash
agy --help
agy --version
agy "$PWD" --prompt "Reply with exactly: agy-ok"
```

If `agy --help` shows a different non-interactive prompt syntax, update only `backend_agy.go` and `backend_agy_test.go` to match the installed CLI's documented syntax. Keep the interface and config shape unchanged.

Reasoning: unlike Codex, the current repo has no existing `agy` test fixture. The backend abstraction protects the rest of the gateway from CLI churn.

### Prompt Wording

Rename the prompt builder to be backend-neutral:

```go
func buildPrompt(entry inbound.LoggedEvent, resultMaxChars int, backendLabel string) string
func buildPromptWithContext(entry inbound.LoggedEvent, resultMaxChars int, backendLabel string, memoryContext string) string
```

Use labels:

- `codex` -> `本地 Codex 执行代理`
- `agy` -> `本地 Antigravity/agy 执行代理`
- unknown fallback -> `本地执行代理`

Keep the current safety semantics:

- current user request has priority over memory context
- default reply language remains Chinese for Lark/Feishu and suitable chat text for Slack
- final reply should fit `result_max_chars`

## File Structure

- Modify `internal/agent/codex.go`
  - Keep `Runner`, `Config`, prompt helpers, trim helpers.
  - Replace inline Codex execution with backend dispatch.
  - Move Codex command details into a `CodexBackend`.
- Create `internal/agent/backend.go`
  - Define `Backend`, `BackendRequest`, backend registry, backend normalization, binary resolution.
- Create `internal/agent/backend_agy.go`
  - Implement `AgyBackend`.
- Modify `internal/agent/codex_test.go`
  - Keep runner tests.
  - Add backend selection and prompt label tests.
  - Rename fake command helpers only where needed.
- Create `internal/agent/backend_agy_test.go`
  - Test `agy` command argv, model handling, extra args, stdout final output, failure output.
- Modify `internal/config/config.go`
  - Add config structs, defaults, env bindings, and getters for backend/binary/args.
- Modify `internal/gateway/service.go`
  - Use neutral config getters in `DefaultAgentConfig()`.
- Modify `internal/slack/gateway.go`
  - Accept neutral fields in `DefaultAgentConfig(...)`.
- Modify `internal/cmd/gateway.go`
  - Add `--agent-backend` and `--agent-binary` flags.
  - Keep `--agent-workspace`.
  - Include `agent_backend` and `agent_binary` in startup JSON.
- Modify `internal/cmd/slack.go`
  - Add `--agent-backend` and `--agent-binary` flags.
  - Include `agent_backend` and `agent_binary` in startup JSON.
- Modify `config.example.yaml`
  - Document `backend`, `binary`, `args`, and deprecated `codex_binary`.
- Modify `README.md`, `USER_GUIDE.md`, and `USAGE.md`
  - Replace Codex-only language where the gateway behavior is now backend-neutral.
  - Add `agy` setup notes and known command-contract validation.

## Task 1: Add Backend Types and Selection

**Files:**

- Create: `internal/agent/backend.go`
- Modify: `internal/agent/codex.go`
- Modify: `internal/agent/codex_test.go`

- [ ] **Step 1: Write failing backend normalization tests**

Add tests in `internal/agent/codex_test.go`:

```go
func TestNormalizeBackendName(t *testing.T) {
	tests := map[string]string{
		"":              "codex",
		"codex":         "codex",
		" CODEX ":       "codex",
		"agy":           "agy",
		"antigravity":   "agy",
		"unknown-value": "codex",
	}
	for input, want := range tests {
		if got := normalizeBackendName(input); got != want {
			t.Fatalf("normalizeBackendName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveBackendBinary(t *testing.T) {
	cfg := Config{Backend: "codex", Binary: "", CodexBinary: "custom-codex"}
	backend := testBackend{name: "codex", defaultBinary: "codex"}
	if got := resolveBackendBinary(cfg, backend); got != "custom-codex" {
		t.Fatalf("binary = %q, want custom-codex", got)
	}

	cfg = Config{Backend: "agy"}
	backend = testBackend{name: "agy", defaultBinary: "agy"}
	if got := resolveBackendBinary(cfg, backend); got != "agy" {
		t.Fatalf("binary = %q, want agy", got)
	}
}

type testBackend struct {
	name          string
	defaultBinary string
}

func (b testBackend) Name() string { return b.name }
func (b testBackend) DefaultBinary() string { return b.defaultBinary }
func (b testBackend) Execute(context.Context, BackendRequest) (string, error) {
	return "", nil
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestNormalizeBackendName|TestResolveBackendBinary' -count=1
```

Expected: FAIL because backend selection helpers do not exist.

- [ ] **Step 3: Implement backend primitives**

Create `internal/agent/backend.go`:

```go
package agent

import (
	"context"
	"strings"

	"github.com/yjwong/lark-cli/internal/inbound"
)

type Backend interface {
	Name() string
	DefaultBinary() string
	Execute(ctx context.Context, req BackendRequest) (string, error)
}

type BackendRequest struct {
	Entry          inbound.LoggedEvent
	Prompt         string
	Workspace      string
	Model          string
	Binary         string
	Args           []string
	ResultMaxChars int
	TempDir        string
}

func normalizeBackendName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "codex":
		return "codex"
	case "agy", "antigravity", "antigravity-cli":
		return "agy"
	default:
		return "codex"
	}
}

func resolveBackendBinary(cfg Config, backend Backend) string {
	if strings.TrimSpace(cfg.Binary) != "" {
		return strings.TrimSpace(cfg.Binary)
	}
	if backend.Name() == "codex" && strings.TrimSpace(cfg.CodexBinary) != "" {
		return strings.TrimSpace(cfg.CodexBinary)
	}
	return backend.DefaultBinary()
}

func splitArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
```

Update `Config` in `internal/agent/codex.go`:

```go
type Config struct {
	Enabled            bool
	Backend            string
	Binary             string
	Args               []string
	CodexBinary        string
	Workspace          string
	Model              string
	AckText            string
	ResultMaxChars     int
	Timeout            time.Duration
	ContextProvider    PromptContextProvider
	ReplyObserver      ReplyObserver
	ProcessingObserver ProcessingObserver
}
```

Keep `NewRunnerWithMessenger` behavior unchanged in this task; it will call concrete backends after Task 2 creates `CodexBackend`.

- [ ] **Step 4: Format and verify**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/backend.go internal/agent/codex.go internal/agent/codex_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestNormalizeBackendName|TestResolveBackendBinary' -count=1
```

Expected: PASS.

## Task 2: Move Codex Command Into an Adapter

**Files:**

- Modify: `internal/agent/codex.go`
- Modify: `internal/agent/codex_test.go`

- [ ] **Step 1: Write a focused Codex adapter test**

Add a test that executes `CodexBackend` directly with `fakeCodexExecutable(t)` and asserts it writes/reads the output file:

```go
func TestCodexBackendExecuteReturnsLastMessageOutput(t *testing.T) {
	tempDir := t.TempDir()
	backend := CodexBackend{}
	result, err := backend.Execute(context.Background(), BackendRequest{
		Entry: inbound.LoggedEvent{
			Provider:    "slack",
			ChannelID:   "C123",
			MessageID:   "1712345678.000100",
			MessageText: "do the thing",
		},
		Prompt:         "prompt text",
		Workspace:      t.TempDir(),
		Binary:         fakeCodexExecutable(t),
		ResultMaxChars: 100,
		TempDir:        tempDir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result != "codex final output" {
		t.Fatalf("result = %q", result)
	}
}
```

- [ ] **Step 2: Run test to verify current behavior is not adapterized**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run TestCodexBackendExecuteReturnsLastMessageOutput -count=1
```

Expected: FAIL until `CodexBackend` exists.

- [ ] **Step 3: Implement `CodexBackend` and update `Runner.execute()`**

Add this type in `internal/agent/codex.go` or a new `backend_codex.go`:

```go
type CodexBackend struct{}

func (CodexBackend) Name() string { return "codex" }

func (CodexBackend) DefaultBinary() string { return "codex" }

func resolveBackend(cfg Config) Backend {
	return CodexBackend{}
}
```

Move the current subprocess code into `CodexBackend.Execute(ctx, req)`, using `req.Binary`, `req.Workspace`, `req.Model`, `req.Args`, `req.Prompt`, `req.TempDir`, and `req.ResultMaxChars`.

Update `Runner.execute()` to call:

```go
backend := resolveBackend(r.cfg)
prompt := buildPromptWithContext(entry, r.cfg.ResultMaxChars, backendLabel(backend.Name()), promptContext)
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
})
```

Keep timeout handling in `Runner.execute()` so all backends return the same timeout error wording.

- [ ] **Step 4: Run existing agent tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/codex.go internal/agent/codex_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -count=1
```

Expected: PASS.

## Task 3: Add the Antigravity Backend

**Files:**

- Create: `internal/agent/backend_agy.go`
- Create: `internal/agent/backend_agy_test.go`

- [ ] **Step 1: Validate local `agy` command contract**

Run on the implementation machine:

```bash
agy --help
agy --version
agy "$PWD" --prompt "Reply with exactly: agy-ok"
```

Expected: help/version succeed and the prompt command prints a final response containing `agy-ok`. If the installed CLI uses a different documented prompt flag, capture that exact syntax and use it in the next test and implementation.

- [ ] **Step 2: Write fake `agy` command tests**

Create `internal/agent/backend_agy_test.go`:

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

func TestAgyBackendExecutePassesWorkspacePromptModelAndArgs(t *testing.T) {
	path := fakeAgyExecutable(t, 0, "agy final output")
	workspace := t.TempDir()
	backend := AgyBackend{}

	result, err := backend.Execute(context.Background(), BackendRequest{
		Entry:          inbound.LoggedEvent{MessageText: "inspect"},
		Prompt:         "prompt text",
		Workspace:      workspace,
		Model:          "gemini-test",
		Binary:         path,
		Args:           []string{"--approval-mode", "auto"},
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result != "agy final output" {
		t.Fatalf("result = %q", result)
	}

	argsData, err := os.ReadFile(filepath.Join(filepath.Dir(path), "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(argsData)
	for _, want := range []string{workspace, "--prompt", "prompt text", "--model", "gemini-test", "--approval-mode", "auto"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q missing %q", args, want)
		}
	}
}

func TestAgyBackendExecuteReturnsFailureOutput(t *testing.T) {
	backend := AgyBackend{}
	_, err := backend.Execute(context.Background(), BackendRequest{
		Prompt:         "prompt text",
		Workspace:      t.TempDir(),
		Binary:         fakeAgyExecutable(t, 7, "agy failed"),
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "agy failed") {
		t.Fatalf("error = %v, want agy failed", err)
	}
}

func fakeAgyExecutable(t *testing.T, exitCode int, output string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-agy")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%s' %q\nexit %d\n", filepath.Join(dir, "args.txt"), output, exitCode)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatalf("WriteFile(fake agy): %v", err)
	}
	return path
}
```

Add selection coverage to `internal/agent/codex_test.go`:

```go
func TestResolveBackendSelectsAgy(t *testing.T) {
	backend := resolveBackend(Config{Backend: "agy"})
	if backend.Name() != "agy" {
		t.Fatalf("backend = %q, want agy", backend.Name())
	}
}
```

Use the repository's existing fake executable style if the helper above is adjusted during implementation. Keep the assertion intent the same.

- [ ] **Step 3: Run test to verify it fails**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestAgyBackend' -count=1
```

Expected: FAIL because `AgyBackend` does not exist.

- [ ] **Step 4: Implement `AgyBackend`**

Create `internal/agent/backend_agy.go`:

```go
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type AgyBackend struct{}

func (AgyBackend) Name() string { return "agy" }

func (AgyBackend) DefaultBinary() string { return "agy" }

func (AgyBackend) Execute(ctx context.Context, req BackendRequest) (string, error) {
	args := []string{req.Workspace, "--prompt", req.Prompt}
	if strings.TrimSpace(req.Model) != "" {
		args = append(args, "--model", strings.TrimSpace(req.Model))
	}
	args = append(args, splitArgs(req.Args)...)

	cmd := exec.CommandContext(ctx, req.Binary, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("%s", trimForChat(text, req.ResultMaxChars))
	}
	if text == "" {
		return "", fmt.Errorf("agy did not return output")
	}
	return trimForChat(text, req.ResultMaxChars), nil
}
```

Update the existing `resolveBackend` function from Task 2:

```go
func resolveBackend(cfg Config) Backend {
	switch normalizeBackendName(cfg.Backend) {
	case "agy":
		return AgyBackend{}
	default:
		return CodexBackend{}
	}
}
```

If Step 1 proved a different syntax, adjust only `args := ...` and the argv test to match the installed `agy` help text.

- [ ] **Step 5: Format and verify**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/backend_agy.go internal/agent/backend_agy_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/agent -run 'TestAgyBackend|TestNormalizeBackendName|TestResolveBackendBinary' -count=1
```

Expected: PASS.

## Task 4: Wire Backend Config Through Viper

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/config/slack_config_test.go`

- [ ] **Step 1: Add config tests**

Add tests that initialize config from temp YAML/env and assert:

- `agent.backend: agy` returns `GetAgentBackend() == "agy"`.
- `agent.binary: /opt/bin/agy` returns `GetAgentBinary() == "/opt/bin/agy"`.
- `agent.args: ["--approval-mode", "auto"]` returns both args.
- `slack.agent.backend: agy` returns `GetSlackAgentBackend() == "agy"`.
- `SLACK_AGENT_BACKEND=agy` overrides file config.
- Existing `SLACK_AGENT_CODEX_BINARY` still returns through `GetSlackAgentCodexBinary()`.

- [ ] **Step 2: Run test to verify it fails**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/config -run 'AgentBackend|SlackAgentBackend' -count=1
```

Expected: FAIL until getters and bindings exist.

- [ ] **Step 3: Add struct fields, defaults, env bindings, and getters**

Extend both `Agent` structs with:

```go
Backend string   `mapstructure:"backend"`
Binary  string   `mapstructure:"binary"`
Args    []string `mapstructure:"args"`
```

Set defaults:

```go
viper.SetDefault("agent.backend", "codex")
viper.SetDefault("agent.binary", "")
viper.SetDefault("agent.args", []string{})
viper.SetDefault("slack.agent.backend", "codex")
viper.SetDefault("slack.agent.binary", "")
viper.SetDefault("slack.agent.args", []string{})
```

Bind env:

```go
viper.BindEnv("agent.backend", "LARK_AGENT_BACKEND")
viper.BindEnv("agent.binary", "LARK_AGENT_BINARY")
viper.BindEnv("agent.args", "LARK_AGENT_ARGS")
viper.BindEnv("slack.agent.backend", "SLACK_AGENT_BACKEND")
viper.BindEnv("slack.agent.binary", "SLACK_AGENT_BINARY")
viper.BindEnv("slack.agent.args", "SLACK_AGENT_ARGS")
```

Add getters:

```go
func GetAgentBackend() string { return strings.TrimSpace(viper.GetString("agent.backend")) }
func GetAgentBinary() string { return strings.TrimSpace(viper.GetString("agent.binary")) }
func GetAgentArgs() []string { return cleanStringSlice(viper.GetStringSlice("agent.args"), viper.GetString("agent.args")) }
func GetSlackAgentBackend() string { return strings.TrimSpace(viper.GetString("slack.agent.backend")) }
func GetSlackAgentBinary() string { return strings.TrimSpace(viper.GetString("slack.agent.binary")) }
func GetSlackAgentArgs() []string { return cleanStringSlice(viper.GetStringSlice("slack.agent.args"), viper.GetString("slack.agent.args")) }
```

Add a helper that supports YAML lists and comma-separated env values:

```go
func cleanStringSlice(values []string, raw string) []string {
	if len(values) == 0 && strings.TrimSpace(raw) != "" {
		values = strings.Split(raw, ",")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
```

- [ ] **Step 4: Format and verify**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/config/config.go internal/config/slack_config_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/config -count=1
```

Expected: PASS.

## Task 5: Wire Gateways and CLI Flags

**Files:**

- Modify: `internal/gateway/service.go`
- Modify: `internal/slack/gateway.go`
- Modify: `internal/cmd/gateway.go`
- Modify: `internal/cmd/slack.go`
- Modify: `internal/cmd/slack_test.go`
- Modify: `internal/gateway/service_test.go`
- Modify: `internal/slack/gateway_test.go`

- [ ] **Step 1: Add gateway config tests**

Add assertions that default config builders populate `agent.Config.Backend`, `Binary`, and `Args` from config getters and direct Slack constructor arguments.

- [ ] **Step 2: Run focused gateway/cmd tests to verify failure**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/gateway ./internal/slack ./internal/cmd -run 'Agent|Gateway|Slack' -count=1
```

Expected: FAIL until fields and flags are wired.

- [ ] **Step 3: Update config builders**

In `internal/gateway/service.go`, add:

```go
Backend: config.GetAgentBackend(),
Binary:  config.GetAgentBinary(),
Args:    config.GetAgentArgs(),
```

In `internal/slack/gateway.go`, extend `DefaultAgentConfig` parameters or replace them with a small input struct. Prefer an input struct if the signature becomes hard to read:

```go
type DefaultAgentConfigInput struct {
	Enabled        bool
	Backend        string
	Binary         string
	CodexBinary    string
	Workspace      string
	Model          string
	Args           []string
	AckText        string
	ResultMaxChars int
	TimeoutMinutes int
}
```

Return `agent.Config` with the neutral fields populated.

- [ ] **Step 4: Add CLI flags**

For Lark gateway:

```go
gatewayServeCmd.Flags().StringVar(&gatewayAgentBackend, "agent-backend", "", "agent backend: codex or agy")
gatewayServeCmd.Flags().StringVar(&gatewayAgentBinary, "agent-binary", "", "agent backend binary path or command name")
```

For Slack gateway:

```go
slackGatewayServeCmd.Flags().StringVar(&slackGatewayAgentBackend, "agent-backend", "", "agent backend: codex or agy")
slackGatewayServeCmd.Flags().StringVar(&slackGatewayAgentBinary, "agent-binary", "", "agent backend binary path or command name")
```

When flags are changed, override the config-derived fields.

- [ ] **Step 5: Update startup JSON**

Include:

```go
"agent_backend": cfg.Agent.Backend,
"agent_binary":  resolve display value without leaking secrets,
```

Binary paths are not secrets, but keep the value exactly as configured for diagnosability.

- [ ] **Step 6: Format and verify focused packages**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/gateway/service.go internal/slack/gateway.go internal/cmd/gateway.go internal/cmd/slack.go internal/cmd/slack_test.go internal/gateway/service_test.go internal/slack/gateway_test.go
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/gateway ./internal/slack ./internal/cmd -count=1
```

Expected: PASS.

## Task 6: Update Documentation and Examples

**Files:**

- Modify: `config.example.yaml`
- Modify: `README.md`
- Modify: `USER_GUIDE.md`
- Modify: `USAGE.md`

- [ ] **Step 1: Update `config.example.yaml`**

Document `backend`, `binary`, `args`, and compatibility:

```yaml
agent:
  enabled: false
  # Agent backend: codex or agy. Defaults to codex.
  backend: "codex"
  # Backend binary path or command name. Empty uses backend default.
  binary: ""
  # Extra backend-specific CLI args appended to the generated command.
  args: []
  # Deprecated compatibility alias for older configs. Prefer binary.
  codex_binary: "codex"
```

Mirror the same fields under `slack.agent`.

- [ ] **Step 2: Update user docs**

Add examples:

```bash
lark gateway serve --agent --agent-backend agy --agent-workspace ~/WorkSpace/project
lark slack gateway serve --agent --agent-backend agy --agent-workspace ~/WorkSpace/project
```

Add config examples:

```yaml
slack:
  agent:
    enabled: true
    backend: "agy"
    binary: "agy"
    workspace: "~/WorkSpace/project"
    model: ""
```

Document validation:

```bash
agy --help
agy --version
agy "$PWD" --prompt "Reply with exactly: agy-ok"
```

State that `codex_binary` remains supported for old Codex-only configs but new configs should use `backend` and `binary`.

- [ ] **Step 3: Run docs grep**

```bash
rg -n "codex exec|Codex agent|codex_binary|agent-backend|agy" README.md USER_GUIDE.md USAGE.md config.example.yaml
```

Expected: remaining Codex-only wording is either intentionally about the Codex backend or paired with backend-neutral wording.

## Task 7: Full Verification

**Files:**

- All touched Go/docs/config files.

- [ ] **Step 1: Format all edited Go files**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 gofmt -w internal/agent/*.go internal/config/config.go internal/gateway/service.go internal/slack/gateway.go internal/cmd/gateway.go internal/cmd/slack.go
```

- [ ] **Step 2: Run focused tests**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./internal/inbound ./internal/agent ./internal/desktop ./internal/gateway ./internal/webhook ./internal/slack ./internal/config ./internal/cmd
```

Expected: PASS.

- [ ] **Step 3: Run full verification**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go test ./...
```

Expected: PASS.

- [ ] **Step 4: Build the CLI**

```bash
docker run --rm -v "$PWD:/work" -w /work golang:1.24 go build -ldflags "-s -w" -o ./lark ./cmd/lark
```

Expected: PASS and `./lark` is produced.

## Future Backends

Claude and Pi should be added as small adapters after `agy` lands:

- `ClaudeBackend`
  - Default binary: `claude`
  - Expected shape to validate: `claude -p "$PROMPT"` or the installed Claude Code non-interactive equivalent.
  - Output mode: stdout.

- `PiBackend`
  - Default binary: `pi`
  - Expected shape to validate against `pi --help` and pi.dev docs.
  - Output mode: stdout or explicit transcript export if provided by the CLI.

Do not add backend-specific config trees unless a backend requires durable settings that cannot be represented by `backend`, `binary`, `model`, `workspace`, and `args`.

## Risks and Mitigations

- **AGY CLI contract drift:** Contain all argv construction inside `AgyBackend` and require a local `agy --help` validation before implementation.
- **Output capture mismatch:** Codex uses `--output-last-message`; AGY initially uses stdout. Keep output handling backend-specific.
- **Breaking existing deployments:** Keep `backend` defaulting to `codex` and keep `codex_binary` as a compatibility alias.
- **Prompt wording regression:** Test backend labels and avoid hard-coded "Codex" in provider-neutral prompt text.
- **Unbounded backend-specific flags:** Support `args` as a narrow escape hatch instead of adding new first-class config for every CLI option.

## Open Questions To Resolve During Implementation

- Does the installed `agy` version support `agy "$WORKSPACE" --prompt "$PROMPT"` exactly, or does it require a different order or subcommand?
- Does `agy` have an explicit final-answer output file or JSON mode that is more reliable than stdout?
- Should gateway startup fail fast when `backend=agy` but the binary is missing, or should it fail only when the first message dispatches? The recommended first behavior is dispatch-time failure to match today's Codex behavior.

## Acceptance Criteria

- Existing Codex Lark and Slack gateway behavior works without config changes.
- `backend: agy` selects `AgyBackend`.
- `binary` overrides backend default binary.
- Existing `codex_binary` still controls Codex when `binary` is empty.
- Lark and Slack startup JSON includes backend information.
- Focused and full Docker Go tests pass.
- Docs explain Codex and AGY usage without implying only Codex is supported.
