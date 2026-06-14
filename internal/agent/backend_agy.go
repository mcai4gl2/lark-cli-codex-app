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
	args := []string{"--add-dir", req.Workspace, "--prompt", req.Prompt}
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
