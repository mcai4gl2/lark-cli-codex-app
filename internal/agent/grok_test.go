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
