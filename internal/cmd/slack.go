package cmd

import (
	"context"
	"errors"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/yjwong/lark-cli/internal/config"
	"github.com/yjwong/lark-cli/internal/output"
	"github.com/yjwong/lark-cli/internal/platform"
	slackgateway "github.com/yjwong/lark-cli/internal/slack"
	"github.com/yjwong/lark-cli/internal/slackmemory"
	"github.com/yjwong/lark-cli/internal/summarizer"
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Slack commands",
	Long:  "Run Slack-specific gateway and messaging commands.",
}

var slackMsgCmd = &cobra.Command{
	Use:   "msg",
	Short: "Slack message commands",
	Long:  "Send and retrieve Slack messages.",
}

var slackGatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Slack gateway commands",
	Long:  "Run a local Slack gateway using Socket Mode.",
}

var slackMemoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Slack memory commands",
	Long:  "Inspect and update local Slack memory files.",
}

var (
	slackGatewayEventLogPath       string
	slackGatewayAutoReplyText      string
	slackGatewayAgentEnabled       bool
	slackGatewayAgentBackend       string
	slackGatewayAgentBinary        string
	slackGatewayAgentWorkspace     string
	slackGatewayDesktopWorker      bool
	slackGatewayMemoryEnabled      bool
	slackGatewayMemoryRoot         string
	slackGatewayMemoryMaxChars     int
	slackGatewayRecoverMode        string
	slackGatewayProcessingReaction string
)

var (
	slackMsgSendChannel  string
	slackMsgSendText     string
	slackMsgSendThreadTS string

	slackMsgHistoryChannel string
	slackMsgHistoryLimit   int
	slackMsgHistoryOldest  string
	slackMsgHistoryLatest  string

	slackMsgThreadChannel string
	slackMsgThreadTS      string
	slackMsgThreadLimit   int
	slackMsgThreadOldest  string
	slackMsgThreadLatest  string

	slackMsgReactChannel  string
	slackMsgReactTS       string
	slackMsgReactReaction string

	slackMsgReactListChannel string
	slackMsgReactListTS      string
	slackMsgReactListFull    bool

	slackMsgReactRemoveChannel  string
	slackMsgReactRemoveTS       string
	slackMsgReactRemoveReaction string
)

var (
	slackMemoryTeamID   string
	slackMemoryChannel  string
	slackMemoryThreadTS string
	slackMemoryScope    string
	slackMemoryText     string
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
  lark slack gateway serve --event-log ~/.slack/gateway-events.jsonl
  lark slack gateway serve --memory --memory-root ~/.slack/conversations`,
	Run: func(cmd *cobra.Command, args []string) {
		agentCfg := slackgateway.DefaultAgentConfig(slackgateway.DefaultAgentConfigInput{
			Enabled:        config.GetSlackAgentEnabled(),
			Backend:        config.GetSlackAgentBackend(),
			Binary:         config.GetSlackAgentBinary(),
			CodexBinary:    config.GetSlackAgentCodexBinary(),
			Workspace:      config.GetSlackAgentWorkspace(),
			Model:          config.GetSlackAgentModel(),
			Args:           config.GetSlackAgentArgs(),
			AckText:        config.GetSlackAgentAckText(),
			ResultMaxChars: config.GetSlackAgentResultMaxChars(),
			TimeoutMinutes: config.GetSlackAgentTimeoutMinutes(),
			SessionResume:  config.GetSlackAgentSessionResume(),
		})
		if cmd.Flags().Changed("agent") {
			agentCfg.Enabled = slackGatewayAgentEnabled
		}
		if strings.TrimSpace(slackGatewayAgentBackend) != "" {
			agentCfg.Backend = strings.TrimSpace(slackGatewayAgentBackend)
		}
		if strings.TrimSpace(slackGatewayAgentBinary) != "" {
			agentCfg.Binary = strings.TrimSpace(slackGatewayAgentBinary)
		}
		if strings.TrimSpace(slackGatewayAgentWorkspace) != "" {
			agentCfg.Workspace = strings.TrimSpace(slackGatewayAgentWorkspace)
		}

		var localSummarizer *summarizer.Client
		if config.GetSlackLocalSummarizerEnabled() {
			url := config.GetSlackLocalSummarizerURL()
			if strings.TrimSpace(url) != "" {
				localSummarizer = summarizer.NewClient(summarizer.Config{
					URL:            url,
					MaxTokens:      config.GetSlackLocalSummarizerMaxTokens(),
					TimeoutSeconds: config.GetSlackLocalSummarizerTimeoutSeconds(),
				})
			}
		}

		cfg := slackgateway.Config{
			AppToken:                      config.GetSlackAppToken(),
			BotToken:                      config.GetSlackBotToken(),
			BotUserID:                     config.GetSlackBotUserID(),
			EventLogPath:                  slackGatewayEventLogPath,
			AutoReplyText:                 slackGatewayAutoReplyText,
			Agent:                         agentCfg,
			DesktopWorker:                 slackGatewayDesktopWorker,
			DesktopQueueRoot:              config.GetSlackDesktopTaskRoot(),
			MemoryEnabled:                 config.GetSlackMemoryEnabled(),
			MemoryRoot:                    config.GetSlackMemoryRoot(),
			MemoryMaxSectionChars:         config.GetSlackMemoryMaxSectionChars(),
			MemoryIncludeThreadTranscript: config.GetSlackMemoryIncludeThreadTranscript(),
			MemoryMaxTranscriptChars:      config.GetSlackMemoryMaxTranscriptChars(),
			MemoryMaxTranscriptRecords:    config.GetSlackMemoryMaxTranscriptRecords(),
			RecoverMode:                   slackgateway.NormalizeRecoverMode(config.GetSlackGatewayRecoverMode()),
			ProcessingReactionName:        config.GetSlackGatewayProcessingReaction(),
			LocalSummarizer:               localSummarizer,
			LocalSummarizerMinChars:       config.GetSlackLocalSummarizerMinChars(),
		}
		if cfg.EventLogPath == "" {
			cfg.EventLogPath = config.GetSlackGatewayEventLogPath()
		}
		if cfg.AutoReplyText == "" {
			cfg.AutoReplyText = config.GetSlackGatewayAutoReplyText()
		}
		if cmd.Flags().Changed("memory") {
			cfg.MemoryEnabled = slackGatewayMemoryEnabled
		}
		if strings.TrimSpace(slackGatewayMemoryRoot) != "" {
			cfg.MemoryRoot = strings.TrimSpace(slackGatewayMemoryRoot)
		}
		if cmd.Flags().Changed("memory-max-section-chars") {
			cfg.MemoryMaxSectionChars = slackGatewayMemoryMaxChars
		}
		if cmd.Flags().Changed("recover-mode") {
			cfg.RecoverMode = slackgateway.NormalizeRecoverMode(slackGatewayRecoverMode)
		}
		if cmd.Flags().Changed("processing-reaction") {
			cfg.ProcessingReactionName = strings.Trim(strings.TrimSpace(slackGatewayProcessingReaction), ":")
		}

		service := slackgateway.NewGateway(cfg)
		output.JSON(map[string]interface{}{
			"ok":                            true,
			"mode":                          "slack_socket_mode",
			"event_log":                     cfg.EventLogPath,
			"auto_reply_enabled":            cfg.AutoReplyText != "",
			"agent_enabled":                 cfg.Agent.Enabled,
			"agent_backend":                 cfg.Agent.Backend,
			"agent_binary":                  cfg.Agent.Binary,
			"agent_workspace":               cfg.Agent.Workspace,
			"agent_session_resume":          cfg.Agent.SessionResume,
			"desktop_worker":                cfg.DesktopWorker,
			"memory_enabled":                cfg.MemoryEnabled,
			"memory_root":                   cfg.MemoryRoot,
			"memory_max_chars":              cfg.MemoryMaxSectionChars,
			"memory_thread_transcript":      cfg.MemoryIncludeThreadTranscript,
			"memory_transcript_max_chars":   cfg.MemoryMaxTranscriptChars,
			"memory_transcript_max_records": cfg.MemoryMaxTranscriptRecords,
			"recover_mode":                  cfg.RecoverMode,
			"processing_reaction":           cfg.ProcessingReactionName,
			"local_summarizer_enabled":      localSummarizer != nil,
			"local_summarizer_url":          config.GetSlackLocalSummarizerURL(),
			"local_summarizer_min_chars":    config.GetSlackLocalSummarizerMinChars(),
			"public_https_required":         false,
		})

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if err := service.Serve(ctx); err != nil {
			output.Fatal("SLACK_GATEWAY_ERROR", err)
		}
	},
}

var slackMsgSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a Slack message",
	Long: `Send a Slack message as the bot.

Examples:
  lark slack msg send --channel C123 --text "hello"
  lark slack msg send --channel C123 --thread-ts 1710000000.000100 --text "reply"`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMsgSendChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if strings.TrimSpace(slackMsgSendText) == "" {
			output.Fatalf("VALIDATION_ERROR", "--text is required")
		}

		client := slackgateway.NewClient(slackgateway.ClientConfig{BotToken: config.GetSlackBotToken()})
		result, err := client.SendMessage(cmd.Context(), slackMsgSendChannel, slackMsgSendThreadTS, slackMsgSendText)
		if err != nil {
			output.Fatal("SLACK_API_ERROR", err)
		}
		output.JSON(result)
	},
}

var slackMsgHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Get Slack channel history",
	Long: `Retrieve compact Slack channel history via conversations.history.

Examples:
  lark slack msg history --channel C123
  lark slack msg history --channel C123 --limit 20`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMsgHistoryChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if slackMsgHistoryLimit < 0 {
			output.Fatalf("VALIDATION_ERROR", "--limit must be >= 0")
		}

		client := slackgateway.NewClient(slackgateway.ClientConfig{BotToken: config.GetSlackBotToken()})
		result, err := client.History(cmd.Context(), slackgateway.HistoryOptions{
			Channel: slackMsgHistoryChannel,
			Limit:   slackMsgHistoryLimit,
			Oldest:  slackMsgHistoryOldest,
			Latest:  slackMsgHistoryLatest,
		})
		if err != nil {
			output.Fatal("SLACK_API_ERROR", err)
		}
		output.JSON(result)
	},
}

var slackMsgThreadCmd = &cobra.Command{
	Use:   "thread",
	Short: "Get Slack thread replies",
	Long: `Retrieve compact Slack thread messages via conversations.replies.

Examples:
  lark slack msg thread --channel C123 --thread-ts 1710000000.000100
  lark slack msg thread --channel C123 --thread-ts 1710000000.000100 --limit 20`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMsgThreadChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if strings.TrimSpace(slackMsgThreadTS) == "" {
			output.Fatalf("VALIDATION_ERROR", "--thread-ts is required")
		}
		if slackMsgThreadLimit < 0 {
			output.Fatalf("VALIDATION_ERROR", "--limit must be >= 0")
		}

		client := slackgateway.NewClient(slackgateway.ClientConfig{BotToken: config.GetSlackBotToken()})
		result, err := client.Thread(cmd.Context(), slackgateway.ThreadOptions{
			Channel:  slackMsgThreadChannel,
			ThreadTS: slackMsgThreadTS,
			Limit:    slackMsgThreadLimit,
			Oldest:   slackMsgThreadOldest,
			Latest:   slackMsgThreadLatest,
		})
		if err != nil {
			output.Fatal("SLACK_API_ERROR", err)
		}
		output.JSON(result)
	},
}

var slackMsgReactCmd = &cobra.Command{
	Use:   "react",
	Short: "Add a Slack reaction to a message",
	Long: `Add a Slack emoji reaction to a message.

Examples:
  lark slack msg react --channel C123 --ts 1710000000.000100 --reaction eyes
  lark slack msg react --channel C123 --ts 1710000000.000100 --reaction :thumbsup:`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMsgReactChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if strings.TrimSpace(slackMsgReactTS) == "" {
			output.Fatalf("VALIDATION_ERROR", "--ts is required")
		}
		if strings.TrimSpace(slackMsgReactReaction) == "" {
			output.Fatalf("VALIDATION_ERROR", "--reaction is required")
		}

		client := slackgateway.NewClient(slackgateway.ClientConfig{BotToken: config.GetSlackBotToken()})
		result, err := client.AddReaction(cmd.Context(), slackgateway.ReactionOptions{
			Channel:   slackMsgReactChannel,
			Timestamp: slackMsgReactTS,
			Name:      slackMsgReactReaction,
		})
		if err != nil {
			output.Fatal("SLACK_API_ERROR", err)
		}
		output.JSON(result)
	},
}

var slackMsgReactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List Slack reactions for a message",
	Long: `List Slack reactions for a message.

Examples:
  lark slack msg react list --channel C123 --ts 1710000000.000100
  lark slack msg react list --channel C123 --ts 1710000000.000100 --full`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMsgReactListChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if strings.TrimSpace(slackMsgReactListTS) == "" {
			output.Fatalf("VALIDATION_ERROR", "--ts is required")
		}

		client := slackgateway.NewClient(slackgateway.ClientConfig{BotToken: config.GetSlackBotToken()})
		result, err := client.GetReactions(cmd.Context(), slackgateway.ReactionGetOptions{
			Channel:   slackMsgReactListChannel,
			Timestamp: slackMsgReactListTS,
			Full:      slackMsgReactListFull,
		})
		if err != nil {
			output.Fatal("SLACK_API_ERROR", err)
		}
		output.JSON(result)
	},
}

var slackMsgReactRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a Slack reaction from a message",
	Long: `Remove a Slack emoji reaction from a message.

Examples:
  lark slack msg react remove --channel C123 --ts 1710000000.000100 --reaction eyes`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(slackMsgReactRemoveChannel) == "" {
			output.Fatalf("VALIDATION_ERROR", "--channel is required")
		}
		if strings.TrimSpace(slackMsgReactRemoveTS) == "" {
			output.Fatalf("VALIDATION_ERROR", "--ts is required")
		}
		if strings.TrimSpace(slackMsgReactRemoveReaction) == "" {
			output.Fatalf("VALIDATION_ERROR", "--reaction is required")
		}

		client := slackgateway.NewClient(slackgateway.ClientConfig{BotToken: config.GetSlackBotToken()})
		result, err := client.RemoveReaction(cmd.Context(), slackgateway.ReactionOptions{
			Channel:   slackMsgReactRemoveChannel,
			Timestamp: slackMsgReactRemoveTS,
			Name:      slackMsgReactRemoveReaction,
		})
		if err != nil {
			output.Fatal("SLACK_API_ERROR", err)
		}
		output.JSON(result)
	},
}

var slackMemoryPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print Slack memory paths",
	Run: func(cmd *cobra.Command, args []string) {
		validateSlackMemoryChannel()

		store := slackmemory.NewStore(slackmemory.Config{Root: config.GetSlackMemoryRoot()})
		event := slackMemoryEventFromFlags()
		output.JSON(map[string]interface{}{
			"root":           store.Root(),
			"channel_memory": store.ChannelMemoryPath(event),
			"thread_memory":  store.ThreadMemoryPath(event),
			"thread_summary": store.ThreadSummaryPath(event),
		})
	},
}

var slackMemoryShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show Slack memory markdown",
	Run: func(cmd *cobra.Command, args []string) {
		validateSlackMemoryChannel()

		store := slackmemory.NewStore(slackmemory.Config{Root: config.GetSlackMemoryRoot()})
		path, err := slackMemoryPathForScope(store, slackMemoryEventFromFlags(), slackMemoryScope)
		if err != nil {
			output.Fatal("VALIDATION_ERROR", err)
		}
		text, err := store.ReadMarkdown(path, 0)
		if err != nil {
			output.Fatal("MEMORY_ERROR", err)
		}
		output.JSON(map[string]interface{}{"path": path, "text": text})
	},
}

var slackMemoryAppendCmd = &cobra.Command{
	Use:   "append",
	Short: "Append text to Slack memory markdown",
	Run: func(cmd *cobra.Command, args []string) {
		validateSlackMemoryChannel()
		if strings.TrimSpace(slackMemoryText) == "" {
			output.Fatalf("VALIDATION_ERROR", "--text is required")
		}

		store := slackmemory.NewStore(slackmemory.Config{Root: config.GetSlackMemoryRoot()})
		path, err := slackMemoryPathForScope(store, slackMemoryEventFromFlags(), slackMemoryScope)
		if err != nil {
			output.Fatal("VALIDATION_ERROR", err)
		}
		if err := store.AppendMarkdown(path, slackMemoryText); err != nil {
			output.Fatal("MEMORY_ERROR", err)
		}
		output.JSON(map[string]interface{}{"success": true, "path": path})
	},
}

func validateSlackMemoryChannel() {
	if strings.TrimSpace(slackMemoryChannel) == "" {
		output.Fatalf("VALIDATION_ERROR", "--channel is required")
	}
}

func slackMemoryEventFromFlags() platform.MessageEvent {
	return platform.MessageEvent{
		Provider:  "slack",
		TeamID:    slackMemoryTeamID,
		ChannelID: slackMemoryChannel,
		ThreadID:  slackMemoryThreadTS,
		MessageID: slackMemoryThreadTS,
	}
}

func slackMemoryPathForScope(store *slackmemory.Store, event platform.MessageEvent, scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "channel":
		return store.ChannelMemoryPath(event), nil
	case "thread":
		if strings.TrimSpace(event.ThreadID) == "" {
			return "", errors.New("--thread-ts is required for thread scope")
		}
		return store.ThreadMemoryPath(event), nil
	case "summary":
		if strings.TrimSpace(event.ThreadID) == "" {
			return "", errors.New("--thread-ts is required for summary scope")
		}
		return store.ThreadSummaryPath(event), nil
	default:
		return "", errors.New("--scope must be one of: channel, thread, summary")
	}
}

func init() {
	slackMsgSendCmd.Flags().StringVar(&slackMsgSendChannel, "channel", "", "Slack channel ID (required)")
	slackMsgSendCmd.Flags().StringVar(&slackMsgSendText, "text", "", "Message text (required)")
	slackMsgSendCmd.Flags().StringVar(&slackMsgSendThreadTS, "thread-ts", "", "Slack thread timestamp for replies")

	slackMsgHistoryCmd.Flags().StringVar(&slackMsgHistoryChannel, "channel", "", "Slack channel ID (required)")
	slackMsgHistoryCmd.Flags().IntVar(&slackMsgHistoryLimit, "limit", 20, "Maximum number of messages to retrieve")
	slackMsgHistoryCmd.Flags().StringVar(&slackMsgHistoryOldest, "oldest", "", "Oldest Slack timestamp to include")
	slackMsgHistoryCmd.Flags().StringVar(&slackMsgHistoryLatest, "latest", "", "Latest Slack timestamp to include")

	slackMsgThreadCmd.Flags().StringVar(&slackMsgThreadChannel, "channel", "", "Slack channel ID (required)")
	slackMsgThreadCmd.Flags().StringVar(&slackMsgThreadTS, "thread-ts", "", "Slack thread timestamp (required)")
	slackMsgThreadCmd.Flags().IntVar(&slackMsgThreadLimit, "limit", 20, "Maximum number of messages to retrieve")
	slackMsgThreadCmd.Flags().StringVar(&slackMsgThreadOldest, "oldest", "", "Oldest Slack timestamp to include")
	slackMsgThreadCmd.Flags().StringVar(&slackMsgThreadLatest, "latest", "", "Latest Slack timestamp to include")

	slackMsgReactCmd.Flags().StringVar(&slackMsgReactChannel, "channel", "", "Slack channel ID (required)")
	slackMsgReactCmd.Flags().StringVar(&slackMsgReactTS, "ts", "", "Slack message timestamp (required)")
	slackMsgReactCmd.Flags().StringVar(&slackMsgReactReaction, "reaction", "", "Reaction emoji name (required)")

	slackMsgReactListCmd.Flags().StringVar(&slackMsgReactListChannel, "channel", "", "Slack channel ID (required)")
	slackMsgReactListCmd.Flags().StringVar(&slackMsgReactListTS, "ts", "", "Slack message timestamp (required)")
	slackMsgReactListCmd.Flags().BoolVar(&slackMsgReactListFull, "full", false, "request the full reaction user list when Slack can provide it")

	slackMsgReactRemoveCmd.Flags().StringVar(&slackMsgReactRemoveChannel, "channel", "", "Slack channel ID (required)")
	slackMsgReactRemoveCmd.Flags().StringVar(&slackMsgReactRemoveTS, "ts", "", "Slack message timestamp (required)")
	slackMsgReactRemoveCmd.Flags().StringVar(&slackMsgReactRemoveReaction, "reaction", "", "Reaction emoji name (required)")

	slackGatewayServeCmd.Flags().StringVar(&slackGatewayEventLogPath, "event-log", "", "path to JSONL event log file")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayAutoReplyText, "auto-reply-text", "", "optional plain-text auto-reply template; supports {{text}}, {{channel_id}}, {{message_id}}, {{user_id}}")
	slackGatewayServeCmd.Flags().BoolVar(&slackGatewayAgentEnabled, "agent", false, "dispatch inbound Slack messages to local agent tasks")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayAgentBackend, "agent-backend", "", "agent backend: codex or agy")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayAgentBinary, "agent-binary", "", "agent backend binary path or command name")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayAgentWorkspace, "agent-workspace", "", "workspace root used when the local agent executes tasks")
	slackGatewayServeCmd.Flags().BoolVar(&slackGatewayDesktopWorker, "desktop-worker", false, "run the local desktop task worker inside the gateway process")
	slackGatewayServeCmd.Flags().BoolVar(&slackGatewayMemoryEnabled, "memory", false, "persist Slack channel and thread memory/audit files")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayMemoryRoot, "memory-root", "", "root directory for Slack memory/audit files")
	slackGatewayServeCmd.Flags().IntVar(&slackGatewayMemoryMaxChars, "memory-max-section-chars", 2000, "maximum characters per memory section injected into Codex prompts")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayRecoverMode, "recover-mode", "", "Slack catch-up mode: thread, mention-dm, or off")
	slackGatewayServeCmd.Flags().StringVar(&slackGatewayProcessingReaction, "processing-reaction", "", "emoji reaction used while processing Slack messages; empty disables reactions")

	for _, c := range []*cobra.Command{slackMemoryPathCmd, slackMemoryShowCmd, slackMemoryAppendCmd} {
		c.Flags().StringVar(&slackMemoryTeamID, "team", "", "Slack team ID")
		c.Flags().StringVar(&slackMemoryChannel, "channel", "", "Slack channel ID (required)")
		c.Flags().StringVar(&slackMemoryThreadTS, "thread-ts", "", "Slack thread timestamp")
	}
	slackMemoryShowCmd.Flags().StringVar(&slackMemoryScope, "scope", "channel", "memory scope: channel, thread, or summary")
	slackMemoryAppendCmd.Flags().StringVar(&slackMemoryScope, "scope", "channel", "memory scope: channel, thread, or summary")
	slackMemoryAppendCmd.Flags().StringVar(&slackMemoryText, "text", "", "memory text to append")

	slackMsgCmd.AddCommand(slackMsgSendCmd)
	slackMsgCmd.AddCommand(slackMsgHistoryCmd)
	slackMsgCmd.AddCommand(slackMsgThreadCmd)
	slackMsgCmd.AddCommand(slackMsgReactCmd)
	slackMsgReactCmd.AddCommand(slackMsgReactListCmd)
	slackMsgReactCmd.AddCommand(slackMsgReactRemoveCmd)
	slackMemoryCmd.AddCommand(slackMemoryPathCmd, slackMemoryShowCmd, slackMemoryAppendCmd)
	slackGatewayCmd.AddCommand(slackGatewayServeCmd)
	slackCmd.AddCommand(slackMsgCmd)
	slackCmd.AddCommand(slackGatewayCmd)
	slackCmd.AddCommand(slackMemoryCmd)
}
