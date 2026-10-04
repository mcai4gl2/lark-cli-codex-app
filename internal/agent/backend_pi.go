package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
)

type PiBackend struct{}

func (PiBackend) Name() string { return "pi" }

func (PiBackend) DefaultBinary() string { return "pi" }

func (PiBackend) Execute(ctx context.Context, req BackendRequest) (BackendResult, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		var err error
		sessionID, err = newPiSessionID()
		if err != nil {
			return BackendResult{}, err
		}
	}

	args := []string{"--print", "--mode", "text", "--approve"}
	if model := strings.TrimSpace(req.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "--session-id", sessionID)
	args = append(args, splitArgs(req.Args)...)
	args = append(args, "--", req.Prompt)

	cmd := exec.CommandContext(ctx, req.Binary, args...)
	if workspace := strings.TrimSpace(req.Workspace); workspace != "" {
		cmd.Dir = workspace
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	text := strings.TrimSpace(stdout.String())
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = text
		}
		if msg == "" {
			msg = err.Error()
		}
		return BackendResult{}, fmt.Errorf("%s", trimForChat(msg, req.ResultMaxChars))
	}
	if text == "" {
		return BackendResult{}, fmt.Errorf("pi did not return output")
	}
	return BackendResult{
		Text:      trimForChat(text, req.ResultMaxChars),
		SessionID: sessionID,
	}, nil
}

func newPiSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate pi session id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
