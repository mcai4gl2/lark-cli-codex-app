package agent

import (
	"context"
	"fmt"
	"sort"
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

var backends = map[string]Backend{
	"codex": CodexBackend{},
	"agy":   AgyBackend{},
	"grok":  GrokBackend{},
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

// RegisteredBackendNames returns sorted registered backend names for error messages.
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

func splitArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
