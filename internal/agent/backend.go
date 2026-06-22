package agent

import (
	"context"
	"strings"

	"github.com/yjwong/lark-cli/internal/inbound"
)

type BackendResult struct {
	Text      string
	SessionID string
}

type Backend interface {
	Name() string
	DefaultBinary() string
	Execute(ctx context.Context, req BackendRequest) (BackendResult, error)
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
	SessionID      string
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

func resolveBackend(cfg Config) Backend {
	switch normalizeBackendName(cfg.Backend) {
	case "agy":
		return AgyBackend{}
	default:
		return CodexBackend{}
	}
}

func backendLabel(name string) string {
	switch normalizeBackendName(name) {
	case "codex":
		return "本地 Codex 执行代理"
	case "agy":
		return "本地 Antigravity/agy 执行代理"
	default:
		return "本地执行代理"
	}
}
