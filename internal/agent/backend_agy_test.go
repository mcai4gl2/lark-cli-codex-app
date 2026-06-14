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
		Args:           []string{"--dangerously-skip-permissions"},
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
	for _, want := range []string{"--add-dir", workspace, "--prompt", "prompt text", "--model", "gemini-test", "--dangerously-skip-permissions"} {
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
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(fake agy): %v", err)
	}
	return path
}
