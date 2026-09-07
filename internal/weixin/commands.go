package weixin

import (
	"context"
	"fmt"
	"strings"

	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/platform"
)

// CommandContext supplies what the built-in slash commands need.
type CommandContext struct {
	Event        platform.MessageEvent
	Reply        func(ctx context.Context, text string) error
	SessionStore *agent.SessionStore
	// DefaultBackend is the configured backend, used when no pin is stored.
	DefaultBackend string
	Workspace      string
	AgentEnabled   bool
}

// HandleCommand runs the channel's own slash commands, which are answered
// directly instead of being sent to the agent.
//
// Backend directives (/codex, /agy, /grok) are deliberately left alone: the
// agent runner parses those itself. Anything unrecognized returns handled=false
// so it flows on to the desktop queue and then the agent.
func HandleCommand(ctx context.Context, text string, cmdCtx CommandContext) (bool, error) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false, nil
	}

	command := trimmed
	args := ""
	if idx := strings.IndexAny(trimmed, " \t\n"); idx >= 0 {
		command = trimmed[:idx]
		args = strings.TrimSpace(trimmed[idx+1:])
	}

	switch strings.ToLower(command) {
	case "/reset":
		return true, handleReset(ctx, cmdCtx)
	case "/status":
		return true, handleStatus(ctx, cmdCtx)
	case "/echo":
		if args == "" {
			return true, cmdCtx.reply(ctx, "用法：/echo <文本>")
		}
		return true, cmdCtx.reply(ctx, args)
	default:
		return false, nil
	}
}

// handleReset drops the stored session for this conversation. WeChat has no
// thread to close, so this plays the role Slack's ✅ reaction plays: the next
// message starts a fresh agent session.
func handleReset(ctx context.Context, cmdCtx CommandContext) error {
	if cmdCtx.SessionStore == nil || !cmdCtx.SessionStore.Enabled() {
		return cmdCtx.reply(ctx, "当前未启用会话记录，无需重置。")
	}
	removed, err := cmdCtx.SessionStore.Remove(SessionKeyFromEvent(cmdCtx.Event))
	if err != nil {
		return cmdCtx.reply(ctx, "重置会话失败："+err.Error())
	}
	if !removed {
		return cmdCtx.reply(ctx, "没有找到可重置的会话，下一条消息会开启新会话。")
	}
	return cmdCtx.reply(ctx, "✅ 会话已重置，下一条消息会开启新会话。")
}

func handleStatus(ctx context.Context, cmdCtx CommandContext) error {
	backend := strings.TrimSpace(cmdCtx.DefaultBackend)
	if backend == "" {
		backend = "codex"
	}
	sessionID := "（无）"
	if cmdCtx.SessionStore != nil && cmdCtx.SessionStore.Enabled() {
		record, ok, err := cmdCtx.SessionStore.LookupRecord(SessionKeyFromEvent(cmdCtx.Event))
		if err == nil && ok {
			if strings.TrimSpace(record.Backend) != "" {
				backend = record.Backend
			}
			if strings.TrimSpace(record.SessionID) != "" {
				sessionID = record.SessionID
			}
		}
	}
	workspace := strings.TrimSpace(cmdCtx.Workspace)
	if workspace == "" {
		workspace = "（未设置）"
	}
	lines := []string{
		fmt.Sprintf("后端: %s", backend),
		fmt.Sprintf("会话: %s", sessionID),
		fmt.Sprintf("工作目录: %s", workspace),
		fmt.Sprintf("代理: %s", enabledLabel(cmdCtx.AgentEnabled)),
		"可用指令: /reset /status /echo /codex /agy /grok",
	}
	return cmdCtx.reply(ctx, strings.Join(lines, "\n"))
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "已启用"
	}
	return "未启用"
}

func (c CommandContext) reply(ctx context.Context, text string) error {
	if c.Reply == nil {
		return nil
	}
	return c.Reply(ctx, text)
}

// SessionKeyFromEvent builds the agent session key for a Weixin conversation.
// It must match the key agent.Runner derives from the same event.
func SessionKeyFromEvent(event platform.MessageEvent) agent.SessionKey {
	threadID := strings.TrimSpace(event.ThreadID)
	if threadID == "" {
		threadID = strings.TrimSpace(event.MessageID)
	}
	return agent.SessionKey{
		Provider:  event.Provider,
		ChannelID: event.ChannelID,
		ThreadTS:  threadID,
	}
}
