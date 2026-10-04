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

func TestPiBackendExecutePassesWorkspacePromptModelSessionAndArgs(t *testing.T) {
	path := fakePiExecutable(t, 0, "pi final output")
	workspace := t.TempDir()
	backend := PiBackend{}

	result, err := backend.Execute(context.Background(), BackendRequest{
		Entry:          inbound.LoggedEvent{MessageText: "inspect"},
		Prompt:         "prompt text",
		Workspace:      workspace,
		Model:          "google/gemini",
		Binary:         path,
		Args:           []string{"--thinking", "low"},
		SessionID:      "thread-session-1",
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Text != "pi final output" {
		t.Fatalf("result.Text = %q", result.Text)
	}
	if result.SessionID != "thread-session-1" {
		t.Fatalf("SessionID = %q", result.SessionID)
	}

	args := readPiArgs(t, path)
	cwd, err := os.ReadFile(filepath.Join(filepath.Dir(path), "cwd.txt"))
	if err != nil {
		t.Fatalf("read cwd: %v", err)
	}
	if strings.TrimSpace(string(cwd)) != workspace {
		t.Fatalf("cwd = %q, want %q", strings.TrimSpace(string(cwd)), workspace)
	}
	want := []string{
		"--print", "--mode", "text", "--approve",
		"--model", "google/gemini",
		"--session-id", "thread-session-1",
		"--thinking", "low",
		"--", "prompt text",
	}
	if args != strings.Join(want, "\n") {
		t.Fatalf("args =\n%s\nwant\n%s", args, strings.Join(want, "\n"))
	}
}

func TestPiBackendExecuteCreatesSessionID(t *testing.T) {
	path := fakePiExecutable(t, 0, "fresh")
	backend := PiBackend{}
	result, err := backend.Execute(context.Background(), BackendRequest{
		Prompt:         "hello",
		Workspace:      t.TempDir(),
		Binary:         path,
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.SessionID == "" || strings.Contains(result.SessionID, " ") {
		t.Fatalf("SessionID = %q", result.SessionID)
	}
	args := readPiArgs(t, path)
	if !strings.Contains(args, "--session-id\n"+result.SessionID+"\n") {
		t.Fatalf("args missing generated session id:\n%s", args)
	}
}

func TestPiBackendExecuteReturnsFailureOutput(t *testing.T) {
	backend := PiBackend{}
	_, err := backend.Execute(context.Background(), BackendRequest{
		Prompt:         "prompt text",
		Workspace:      t.TempDir(),
		Binary:         fakePiExecutable(t, 7, "pi failed"),
		ResultMaxChars: 100,
		TempDir:        t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "pi failed") {
		t.Fatalf("error = %v, want pi failed", err)
	}
}

func TestResolvePi(t *testing.T) {
	b, ok := Resolve("pi")
	if !ok || b.Name() != "pi" || b.DefaultBinary() != "pi" {
		t.Fatalf("Resolve(pi) = %#v ok=%v", b, ok)
	}
	if got := backendLabel("pi"); got != "本地 Pi 执行代理" {
		t.Fatalf("label = %q", got)
	}
}

func readPiArgs(t *testing.T, binary string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(binary), "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	return strings.TrimSuffix(string(data), "\n")
}

func fakePiExecutable(t *testing.T, exitCode int, output string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-pi")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\npwd > %q\nif [ %d -ne 0 ]; then\n  printf '%%s' %q >&2\n  exit %d\nfi\nprintf '%%s' %q\nexit 0\n",
		filepath.Join(dir, "args.txt"), filepath.Join(dir, "cwd.txt"), exitCode, output, exitCode, output)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(fake pi): %v", err)
	}
	return path
}
