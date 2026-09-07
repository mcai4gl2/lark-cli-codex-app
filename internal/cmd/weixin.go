package cmd

import (
	"context"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/config"
	"github.com/yjwong/lark-cli/internal/output"
	"github.com/yjwong/lark-cli/internal/summarizer"
	"github.com/yjwong/lark-cli/internal/weixin"
)

var weixinCmd = &cobra.Command{
	Use:   "weixin",
	Short: "Weixin (WeChat) commands",
	Long:  "Bind a Weixin bot account and run the local Weixin gateway.",
}

var weixinGatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Weixin gateway commands",
	Long:  "Run a local Weixin gateway that long-polls for messages.",
}

var weixinMsgCmd = &cobra.Command{
	Use:   "msg",
	Short: "Weixin message commands",
	Long:  "Send Weixin messages as the bound bot.",
}

var weixinAccountsCmd = &cobra.Command{
	Use:   "accounts",
	Short: "Weixin account commands",
	Long:  "Inspect and remove locally stored Weixin bot accounts.",
}

var (
	weixinLoginForce   bool
	weixinLoginVerbose bool
	weixinLoginTimeout time.Duration
)

var (
	weixinGatewayAccountID      string
	weixinGatewayEventLogPath   string
	weixinGatewayAutoReplyText  string
	weixinGatewayAgentEnabled   bool
	weixinGatewayAgentBackend   string
	weixinGatewayAgentBinary    string
	weixinGatewayAgentWorkspace string
	weixinGatewayDesktopWorker  bool
	weixinGatewayMemoryEnabled  bool
	weixinGatewayMemoryRoot     string
	weixinGatewayMemoryMaxChars int
	weixinGatewayAllowFrom      []string
	weixinGatewayTyping         bool
	weixinGatewayMedia          bool
)

var (
	weixinMsgSendAccountID string
	weixinMsgSendTo        string
	weixinMsgSendText      string
	weixinMsgSendMedia     string
)

// newWeixinClient builds an API client from config, optionally overriding the
// host and token (the QR endpoints always use the fixed host and no token).
func newWeixinClient(baseURL, token string) *weixin.Client {
	return weixin.NewClient(weixin.ClientConfig{
		BaseURL:        baseURL,
		Token:          token,
		AppID:          config.GetWeixinAppID(),
		ChannelVersion: version,
		BotAgent:       weixinBotAgent(),
		RouteTag:       config.GetWeixinRouteTag(),
	})
}

// weixinBotAgent defaults the observability identity to lark-cli/<version>.
func weixinBotAgent() string {
	if configured := config.GetWeixinBotAgent(); configured != "" {
		return configured
	}
	return "lark-cli/" + strings.TrimPrefix(version, "v")
}

func weixinStore() *weixin.Store {
	return weixin.NewStore(config.GetWeixinStateDir())
}

var weixinLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Bind a Weixin account by scanning a terminal QR code",
	Long: `Bind a Weixin bot account to this machine.

A QR code is printed in the terminal (with the raw link as a fallback). Scan it
with the WeChat mobile app and confirm. Credentials are written to
<config dir>/weixin/accounts/ at mode 0600.

Re-running after a successful bind is expected to report "already connected".

Examples:
  lark weixin login
  lark weixin login --verbose --timeout 10m`,
	Run: func(cmd *cobra.Command, args []string) {
		store := weixinStore()
		loginCfg := &weixin.LoginConfig{
			// QR login always talks to the fixed host, never a per-account baseurl.
			Client:     newWeixinClient(config.GetWeixinBaseURL(), ""),
			Store:      store,
			BotType:    config.GetWeixinBotType(),
			CDNBaseURL: config.GetWeixinCDNBaseURL(),
			Timeout:    weixinLoginTimeout,
			Verbose:    weixinLoginVerbose,
		}

		result, err := weixin.RunInteractiveLogin(cmd.Context(), loginCfg)
		if err != nil {
			output.Fatal("WEIXIN_LOGIN_ERROR", err)
		}
		if result.AlreadyConnected {
			output.JSON(map[string]interface{}{
				"ok":                true,
				"already_connected": true,
				"message":           result.Message,
			})
			return
		}

		allowFrom := config.GetWeixinGatewayAllowFrom()
		output.JSON(map[string]interface{}{
			"ok":         true,
			"account_id": result.AccountID,
			"user_id":    result.UserID,
			"base_url":   result.BaseURL,
			"message":    result.Message,
			// With no configured allow-list the gateway falls back to this user id,
			// which is what keeps it from running the agent for strangers.
			"allow_from_configured": len(allowFrom) > 0,
		})
	},
}

var weixinAccountsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List locally stored Weixin accounts",
	Run: func(cmd *cobra.Command, args []string) {
		store := weixinStore()
		ids, err := store.List()
		if err != nil {
			output.Fatal("WEIXIN_ACCOUNT_ERROR", err)
		}
		accounts := make([]map[string]interface{}, 0, len(ids))
		for _, id := range ids {
			account, loadErr := store.Load(id)
			entry := map[string]interface{}{"account_id": id}
			if loadErr != nil {
				entry["error"] = loadErr.Error()
			} else {
				entry["user_id"] = account.UserID
				entry["base_url"] = account.BaseURL
				entry["saved_at"] = account.SavedAt
				entry["has_token"] = strings.TrimSpace(account.Token) != ""
			}
			accounts = append(accounts, entry)
		}
		output.JSON(map[string]interface{}{
			"root":     store.Root(),
			"count":    len(accounts),
			"accounts": accounts,
		})
	},
}

var weixinAccountsRemoveCmd = &cobra.Command{
	Use:   "remove <account-id>",
	Short: "Remove a locally stored Weixin account",
	Long: `Remove a Weixin account's credentials, poll cursor, and context tokens.

This does not unbind the account on the WeChat side; run "lark weixin login"
again to re-bind.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		accountID := strings.TrimSpace(args[0])
		if accountID == "" {
			output.Fatalf("VALIDATION_ERROR", "account id is required")
		}
		store := weixinStore()
		if err := store.Remove(accountID); err != nil {
			output.Fatal("WEIXIN_ACCOUNT_ERROR", err)
		}
		output.JSON(map[string]interface{}{"success": true, "account_id": accountID})
	},
}

// resolveWeixinAccount loads the account the gateway and message commands act
// as, honoring an explicit --account or the weixin.account_id setting.
func resolveWeixinAccount(accountID string) weixin.Account {
	store := weixinStore()
	selected := strings.TrimSpace(accountID)
	if selected == "" {
		selected = config.GetWeixinAccountID()
	}
	account, err := store.Resolve(selected)
	if err != nil {
		output.Fatal("WEIXIN_ACCOUNT_ERROR", err)
	}
	if strings.TrimSpace(account.Token) == "" {
		output.Fatalf("WEIXIN_ACCOUNT_ERROR", "account %s has no stored token; run `lark weixin login` again", account.AccountID)
	}
	if strings.TrimSpace(account.BaseURL) == "" {
		account.BaseURL = config.GetWeixinBaseURL()
	}
	if strings.TrimSpace(account.CDNBaseURL) == "" {
		account.CDNBaseURL = config.GetWeixinCDNBaseURL()
	}
	return account
}

var weixinGatewayServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the local Weixin long-poll gateway",
	Long: `Run a local Weixin gateway.

The gateway long-polls the Weixin iLink service for direct messages, dispatches
them to the local agent (codex, agy, or grok), and sends the result back into
the conversation. No public HTTPS callback URL is required.

SECURITY: every accepted message runs the agent with workspace-write. The
sender allow-list is the containment boundary; it defaults to the user id
recorded at QR login.

Examples:
  lark weixin gateway serve
  lark weixin gateway serve --agent --agent-workspace ~/WorkSpace
  lark weixin gateway serve --allow-from user-a@im.wechat --memory`,
	Run: func(cmd *cobra.Command, args []string) {
		account := resolveWeixinAccount(weixinGatewayAccountID)

		agentCfg := weixin.DefaultAgentConfig(weixin.DefaultAgentConfigInput{
			Enabled:        config.GetWeixinAgentEnabled(),
			Backend:        config.GetWeixinAgentBackend(),
			Binary:         config.GetWeixinAgentBinary(),
			CodexBinary:    config.GetWeixinAgentCodexBinary(),
			GrokBinary:     config.GetWeixinAgentGrokBinary(),
			Workspace:      config.GetWeixinAgentWorkspace(),
			Model:          config.GetWeixinAgentModel(),
			Args:           config.GetWeixinAgentArgs(),
			AckText:        config.GetWeixinAgentAckText(),
			ResultMaxChars: config.GetWeixinAgentResultMaxChars(),
			TimeoutMinutes: config.GetWeixinAgentTimeoutMinutes(),
			SessionResume:  config.GetWeixinAgentSessionResume(),
		})
		if cmd.Flags().Changed("agent") {
			agentCfg.Enabled = weixinGatewayAgentEnabled
		}
		if strings.TrimSpace(weixinGatewayAgentBackend) != "" {
			agentCfg.Backend = strings.TrimSpace(weixinGatewayAgentBackend)
		}
		if strings.TrimSpace(weixinGatewayAgentBinary) != "" {
			agentCfg.Binary = strings.TrimSpace(weixinGatewayAgentBinary)
		}
		if strings.TrimSpace(weixinGatewayAgentWorkspace) != "" {
			agentCfg.Workspace = strings.TrimSpace(weixinGatewayAgentWorkspace)
		}
		if err := agent.ValidateDefaultBackend(agentCfg.Backend); err != nil {
			output.Fatal("AGENT_BACKEND", err)
		}

		var localSummarizer *summarizer.Client
		if config.GetSlackLocalSummarizerEnabled() {
			if url := config.GetSlackLocalSummarizerURL(); strings.TrimSpace(url) != "" {
				localSummarizer = summarizer.NewClient(summarizer.Config{
					URL:            url,
					MaxTokens:      config.GetSlackLocalSummarizerMaxTokens(),
					TimeoutSeconds: config.GetSlackLocalSummarizerTimeoutSeconds(),
				})
			}
		}

		allowFrom := config.GetWeixinGatewayAllowFrom()
		if len(weixinGatewayAllowFrom) > 0 {
			allowFrom = weixinGatewayAllowFrom
		}

		cfg := weixin.Config{
			AccountID:       account.AccountID,
			Token:           account.Token,
			BaseURL:         account.BaseURL,
			CDNBaseURL:      account.CDNBaseURL,
			StateRoot:       config.GetWeixinStateDir(),
			AppID:           config.GetWeixinAppID(),
			BotAgent:        weixinBotAgent(),
			ChannelVersion:  version,
			RouteTag:        config.GetWeixinRouteTag(),
			LoginUserID:     account.UserID,
			AllowFrom:       allowFrom,
			LongPollTimeout: time.Duration(config.GetWeixinGatewayLongPollTimeoutSeconds()) * time.Second,
			Typing:          config.GetWeixinGatewayTyping(),
			EventLogPath:    weixinGatewayEventLogPath,
			AutoReplyText:   weixinGatewayAutoReplyText,
			Agent:           agentCfg,

			DesktopWorker:    weixinGatewayDesktopWorker,
			DesktopQueueRoot: config.GetWeixinDesktopTaskRoot(),

			MemoryEnabled:                 config.GetWeixinMemoryEnabled(),
			MemoryRoot:                    config.GetWeixinMemoryRoot(),
			MemoryMaxSectionChars:         config.GetWeixinMemoryMaxSectionChars(),
			MemoryIncludeThreadTranscript: config.GetWeixinMemoryIncludeThreadTranscript(),
			MemoryMaxTranscriptChars:      config.GetWeixinMemoryMaxTranscriptChars(),
			MemoryMaxTranscriptRecords:    config.GetWeixinMemoryMaxTranscriptRecords(),
			LocalSummarizer:               localSummarizer,
			LocalSummarizerMinChars:       config.GetSlackLocalSummarizerMinChars(),
			MediaDownload:                 weixinGatewayMedia,
		}
		if cfg.EventLogPath == "" {
			cfg.EventLogPath = config.GetWeixinGatewayEventLogPath()
		}
		if cfg.AutoReplyText == "" {
			cfg.AutoReplyText = config.GetWeixinGatewayAutoReplyText()
		}
		if cmd.Flags().Changed("typing") {
			cfg.Typing = weixinGatewayTyping
		}
		if cmd.Flags().Changed("memory") {
			cfg.MemoryEnabled = weixinGatewayMemoryEnabled
		}
		if strings.TrimSpace(weixinGatewayMemoryRoot) != "" {
			cfg.MemoryRoot = strings.TrimSpace(weixinGatewayMemoryRoot)
		}
		if cmd.Flags().Changed("memory-max-section-chars") {
			cfg.MemoryMaxSectionChars = weixinGatewayMemoryMaxChars
		}

		service := weixin.NewGateway(cfg)
		// Report the effective allow-list, which falls back to the login user
		// when nothing is configured, rather than the raw config length.
		allowedSenders, acceptsEveryone := service.AllowList()
		output.JSON(map[string]interface{}{
			"ok":                        true,
			"mode":                      "weixin_long_poll",
			"account_id":                cfg.AccountID,
			"base_url":                  cfg.BaseURL,
			"event_log":                 cfg.EventLogPath,
			"auto_reply_enabled":        cfg.AutoReplyText != "",
			"agent_enabled":             cfg.Agent.Enabled,
			"agent_backend":             cfg.Agent.Backend,
			"agent_binary":              cfg.Agent.Binary,
			"agent_workspace":           cfg.Agent.Workspace,
			"agent_session_resume":      cfg.Agent.SessionResume,
			"desktop_worker":            cfg.DesktopWorker,
			"memory_enabled":            cfg.MemoryEnabled,
			"memory_root":               cfg.MemoryRoot,
			"typing":                    cfg.Typing,
			"media_download":            cfg.MediaDownload,
			"allow_from":                allowedSenders,
			"allow_from_any_sender":     acceptsEveryone,
			"long_poll_timeout_seconds": int(cfg.LongPollTimeout / time.Second),
			"public_https_required":     false,
		})

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if err := service.Serve(ctx); err != nil {
			output.Fatal("WEIXIN_GATEWAY_ERROR", err)
		}
	},
}

var weixinMsgSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a Weixin message",
	Long: `Send a Weixin message as the bound bot.

A conversation context token is required, so the recipient must have messaged
the bot at least once while a gateway was running.

Examples:
  lark weixin msg send --to user@im.wechat --text "hello"
  lark weixin msg send --to user@im.wechat --text "here you go" --media ./report.pdf`,
	Run: func(cmd *cobra.Command, args []string) {
		if strings.TrimSpace(weixinMsgSendTo) == "" {
			output.Fatalf("VALIDATION_ERROR", "--to is required")
		}
		if strings.TrimSpace(weixinMsgSendText) == "" && strings.TrimSpace(weixinMsgSendMedia) == "" {
			output.Fatalf("VALIDATION_ERROR", "--text or --media is required")
		}

		account := resolveWeixinAccount(weixinMsgSendAccountID)
		store := weixinStore()
		tokens := weixin.NewContextTokenStore(store.ContextTokenPath(account.AccountID))
		if _, err := tokens.Restore(); err != nil {
			output.Fatal("WEIXIN_CONTEXT_TOKEN_ERROR", err)
		}

		messenger := weixin.NewMessenger(weixin.MessengerConfig{
			Client:        newWeixinClient(account.BaseURL, account.Token),
			ContextTokens: tokens,
			Guard:         weixin.NewSessionGuard(),
			CDNBaseURL:    account.CDNBaseURL,
		})

		if media := strings.TrimSpace(weixinMsgSendMedia); media != "" {
			if err := messenger.SendMedia(cmd.Context(), weixinMsgSendTo, weixinMsgSendText, media); err != nil {
				output.Fatal("WEIXIN_API_ERROR", err)
			}
		} else if err := messenger.SendText(cmd.Context(), weixinMsgSendTo, weixinMsgSendText); err != nil {
			output.Fatal("WEIXIN_API_ERROR", err)
		}

		output.JSON(map[string]interface{}{
			"success":    true,
			"account_id": account.AccountID,
			"to":         weixinMsgSendTo,
		})
	},
}

func init() {
	weixinLoginCmd.Flags().BoolVar(&weixinLoginForce, "force", false, "always request a fresh QR code (each CLI run already does; accepted for parity)")
	weixinLoginCmd.Flags().BoolVar(&weixinLoginVerbose, "verbose", false, "print poll progress while waiting for the scan")
	weixinLoginCmd.Flags().DurationVar(&weixinLoginTimeout, "timeout", weixin.DefaultLoginTimeout, "how long to wait for the QR scan to be confirmed")

	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayAccountID, "account", "", "Weixin account id to serve; empty uses the most recently registered account")
	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayEventLogPath, "event-log", "", "path to JSONL event log file")
	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayAutoReplyText, "auto-reply-text", "", "optional plain-text auto-reply template; supports {{text}}, {{channel_id}}, {{message_id}}, {{user_id}}")
	weixinGatewayServeCmd.Flags().BoolVar(&weixinGatewayAgentEnabled, "agent", false, "dispatch inbound Weixin messages to local agent tasks")
	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayAgentBackend, "agent-backend", "", "agent backend: codex, agy, or grok")
	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayAgentBinary, "agent-binary", "", "agent backend binary path or command name")
	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayAgentWorkspace, "agent-workspace", "", "workspace root used when the local agent executes tasks")
	weixinGatewayServeCmd.Flags().BoolVar(&weixinGatewayDesktopWorker, "desktop-worker", false, "run the local desktop task worker inside the gateway process")
	weixinGatewayServeCmd.Flags().BoolVar(&weixinGatewayMemoryEnabled, "memory", false, "persist Weixin conversation memory/audit files")
	weixinGatewayServeCmd.Flags().StringVar(&weixinGatewayMemoryRoot, "memory-root", "", "root directory for Weixin memory/audit files")
	weixinGatewayServeCmd.Flags().IntVar(&weixinGatewayMemoryMaxChars, "memory-max-section-chars", 2000, "maximum characters per memory section injected into agent prompts")
	weixinGatewayServeCmd.Flags().StringSliceVar(&weixinGatewayAllowFrom, "allow-from", nil, "Weixin user IDs allowed to reach the agent; overrides config. Use '*' to accept everyone")
	weixinGatewayServeCmd.Flags().BoolVar(&weixinGatewayTyping, "typing", true, "show the WeChat typing indicator while an agent task runs")
	weixinGatewayServeCmd.Flags().BoolVar(&weixinGatewayMedia, "media", false, "download and decrypt inbound image, file, and video attachments")

	weixinMsgSendCmd.Flags().StringVar(&weixinMsgSendAccountID, "account", "", "Weixin account id to send as")
	weixinMsgSendCmd.Flags().StringVar(&weixinMsgSendTo, "to", "", "recipient Weixin user ID (required)")
	weixinMsgSendCmd.Flags().StringVar(&weixinMsgSendText, "text", "", "message text")
	weixinMsgSendCmd.Flags().StringVar(&weixinMsgSendMedia, "media", "", "local file to send as an attachment; --text becomes its caption")

	weixinAccountsCmd.AddCommand(weixinAccountsListCmd, weixinAccountsRemoveCmd)
	weixinGatewayCmd.AddCommand(weixinGatewayServeCmd)
	weixinMsgCmd.AddCommand(weixinMsgSendCmd)
	weixinCmd.AddCommand(weixinLoginCmd)
	weixinCmd.AddCommand(weixinAccountsCmd)
	weixinCmd.AddCommand(weixinGatewayCmd)
	weixinCmd.AddCommand(weixinMsgCmd)
}
