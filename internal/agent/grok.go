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
