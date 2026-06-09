package cmd

import (
	"context"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/yjwong/lark-cli/internal/config"
	"github.com/yjwong/lark-cli/internal/output"
	slackgateway "github.com/yjwong/lark-cli/internal/slack"
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Slack commands",
	Long:  "Run Slack-specific gateway and messaging commands.",
}

var slackGatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Slack gateway commands",
	Long:  "Run a local Slack gateway using Socket Mode.",
}

var (
	slackGatewayEventLogPath   string
	slackGatewayAutoReplyText  string
	slackGatewayAgentEnabled   bool
	slackGatewayAgentWorkspace string
	slackGatewayDesktopWorker  bool
)

var slackGatewayServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the local Slack Socket Mode gateway",
	Long: `Run a local Slack gateway using Socket Mode event subscriptions.

This mode keeps an outbound WebSocket connection to Slack and does not require
a public HTTPS callback URL.

Examples:
  lark slack gateway serve
  lark slack gateway serve --agent --agent-workspace ~/WorkSpace
  lark slack gateway serve --event-log ~/.slack/gateway-events.jsonl`,
	Run: func(cmd *cobra.Command, args []string) {
		agentCfg := slackgateway.DefaultAgentConfig(
			config.GetSlackAgentEnabled(),
			config.GetSlackAgentCodexBinary(),
			config.GetSlackAgentWorkspace(),
			config.GetSlackAgentModel(),
			config.GetSlackAgentAckText(),
			config.GetSlackAgentResultMaxChars(),
			config.GetSlackAgentTimeoutMinutes(),
		)
		if cmd.Flags().Changed("agent") {
			agentCfg.Enabled = slackGatewayAgentEnabled
		}
		if strings.TrimSpace(slackGatewayAgentWorkspace) != "" {
			agentCfg.Workspace = strings.TrimSpace(slackGatewayAgentWorkspace)
		}

		cfg := slackgateway.Config{
			AppToken:         config.GetSlackAppToken(),
			BotToken:         config.GetSlackBotToken(),
			BotUserID:        config.GetSlackBotUserID(),
			EventLogPath:     slackGatewayEventLogPath,
			AutoReplyText:    slackGatewayAutoReplyText,
			Agent:            agentCfg,
			DesktopWorker:    slackGatewayDesktopWorker,
			DesktopQueueRoot: config.GetSlackDesktopTaskRoot(),
		}
		if cfg.EventLogPath == "" {
			cfg.EventLogPath = config.GetSlackGatewayEventLogPath()
		}
		if cfg.AutoReplyText == "" {
			cfg.AutoReplyText = config.GetSlackGatewayAutoReplyText()
		}

		service := slackgateway.NewGateway(cfg)
		output.JSON(map[string]interface{}{
			"ok":                    true,
			"mode":                  "slack_socket_mode",
			"event_log":             cfg.EventLogPath,
			"auto_reply_enabled":    cfg.AutoReplyText != "",
			"agent_enabled":         cfg.Agent.Enabled,
			"agent_workspace":       cfg.Agent.Workspace,
			"desktop_worker":        cfg.DesktopWorker,
			"public_https_required": false,
		})

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if err := service.Serve(ctx); err != nil {
			output.Fatal("SLACK_GATEWAY_ERROR", err)
		}
	},
}

func init() {
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayEventLogPath, "event-log", "", "path to JSONL event log file")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayAutoReplyText, "auto-reply-text", "", "optional plain-text auto-reply template; supports {{text}}, {{channel_id}}, {{message_id}}, {{user_id}}")
	slackGatewayServeCmd.Flags().BoolVar(&slackGatewayAgentEnabled, "agent", false, "dispatch inbound Slack messages to local codex exec tasks")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayAgentWorkspace, "agent-workspace", "", "workspace root used when the local Codex agent executes tasks")
	slackGatewayServeCmd.Flags().BoolVar(&slackGatewayDesktopWorker, "desktop-worker", false, "run the local desktop task worker inside the gateway process")

	slackGatewayCmd.AddCommand(slackGatewayServeCmd)
	slackCmd.AddCommand(slackGatewayCmd)
}
