package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	AppID     string `mapstructure:"app_id"`
	AppSecret string `mapstructure:"app_secret"`
	Region    string `mapstructure:"region"`
	Defaults  struct {
		Timezone        string `mapstructure:"timezone"`
		ReminderMinutes int    `mapstructure:"reminder_minutes"`
	} `mapstructure:"defaults"`
	OAuth struct {
		RedirectPort int `mapstructure:"redirect_port"`
	} `mapstructure:"oauth"`
	Agent struct {
		Enabled        bool     `mapstructure:"enabled"`
		Backend        string   `mapstructure:"backend"`
		Binary         string   `mapstructure:"binary"`
		Args           []string `mapstructure:"args"`
		CodexBinary    string   `mapstructure:"codex_binary"`
		Workspace      string   `mapstructure:"workspace"`
		Model          string   `mapstructure:"model"`
		AckText        string   `mapstructure:"ack_text"`
		ResultMaxChars int      `mapstructure:"result_max_chars"`
		TimeoutMinutes int      `mapstructure:"timeout_minutes"`
		SessionResume  bool     `mapstructure:"session_resume"`
	} `mapstructure:"agent"`
	Gateway struct {
		EventLog      string `mapstructure:"event_log"`
		AutoReplyText string `mapstructure:"auto_reply_text"`
	} `mapstructure:"gateway"`
	Slack struct {
		BotToken      string `mapstructure:"bot_token"`
		AppToken      string `mapstructure:"app_token"`
		SigningSecret string `mapstructure:"signing_secret"`
		BotUserID     string `mapstructure:"bot_user_id"`
		Gateway       struct {
			EventLog           string `mapstructure:"event_log"`
			AutoReplyText      string `mapstructure:"auto_reply_text"`
			RecoverMode        string `mapstructure:"recover_mode"`
			ProcessingReaction string `mapstructure:"processing_reaction"`
		} `mapstructure:"gateway"`
		Memory struct {
			Enabled                 bool   `mapstructure:"enabled"`
			Root                    string `mapstructure:"root"`
			MaxSectionChars         int    `mapstructure:"max_section_chars"`
			IncludeThreadTranscript bool   `mapstructure:"include_thread_transcript"`
			MaxTranscriptChars      int    `mapstructure:"max_transcript_chars"`
			MaxTranscriptRecords    int    `mapstructure:"max_transcript_records"`
		} `mapstructure:"memory"`
		LocalSummarizer struct {
			Enabled        bool   `mapstructure:"enabled"`
			URL            string `mapstructure:"url"`
			MaxTokens      int    `mapstructure:"max_tokens"`
			TimeoutSeconds int    `mapstructure:"timeout_seconds"`
			MinChars       int    `mapstructure:"min_chars"`
		} `mapstructure:"local_summarizer"`
		Agent struct {
			Enabled        bool     `mapstructure:"enabled"`
			Backend        string   `mapstructure:"backend"`
			Binary         string   `mapstructure:"binary"`
			Args           []string `mapstructure:"args"`
			CodexBinary    string   `mapstructure:"codex_binary"`
			Workspace      string   `mapstructure:"workspace"`
			Model          string   `mapstructure:"model"`
			AckText        string   `mapstructure:"ack_text"`
			ResultMaxChars int      `mapstructure:"result_max_chars"`
			TimeoutMinutes int      `mapstructure:"timeout_minutes"`
			SessionResume  bool     `mapstructure:"session_resume"`
		} `mapstructure:"agent"`
	} `mapstructure:"slack"`
	Webhook struct {
		ListenAddr        string `mapstructure:"listen_addr"`
		Path              string `mapstructure:"path"`
		VerificationToken string `mapstructure:"verification_token"`
		EventLog          string `mapstructure:"event_log"`
		AutoReplyText     string `mapstructure:"auto_reply_text"`
	} `mapstructure:"webhook"`
	CustomEmojis map[string]string `mapstructure:"custom_emojis"`
}

var (
	cfg     *Config
	cfgDir  string
	rootDir string
)

// GetConfigDir returns the .lark directory path
func GetConfigDir() string {
	return cfgDir
}

// GetRootDir returns the project root directory
func GetRootDir() string {
	return rootDir
}

// Init initializes the configuration
func Init() error {
	cfgDir = os.Getenv("LARK_CONFIG_DIR")
	if cfgDir == "" {
		cfgDir = os.Getenv("LARK_CAL_CONFIG_DIR")
	}
	if cfgDir == "" {
		return fmt.Errorf("LARK_CONFIG_DIR environment variable is not set")
	}

	rootDir = filepath.Dir(cfgDir)

	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(cfgDir)

	viper.SetDefault("region", "lark")
	viper.SetDefault("defaults.timezone", "Asia/Singapore")
	viper.SetDefault("defaults.reminder_minutes", 15)
	viper.SetDefault("oauth.redirect_port", 9999)
	viper.SetDefault("agent.enabled", false)
	viper.SetDefault("agent.backend", "codex")
	viper.SetDefault("agent.binary", "")
	viper.SetDefault("agent.args", []string{})
	viper.SetDefault("agent.codex_binary", "codex")
	viper.SetDefault("agent.ack_text", "收到，开始处理。")
	viper.SetDefault("agent.result_max_chars", 1800)
	viper.SetDefault("agent.timeout_minutes", 20)
	viper.SetDefault("agent.session_resume", false)
	viper.SetDefault("gateway.event_log", filepath.Join(cfgDir, "gateway-events.jsonl"))
	viper.SetDefault("slack.gateway.event_log", filepath.Join(rootDir, ".slack", "gateway-events.jsonl"))
	viper.SetDefault("slack.gateway.recover_mode", "thread")
	viper.SetDefault("slack.gateway.processing_reaction", "eyes")
	viper.SetDefault("slack.memory.enabled", false)
	viper.SetDefault("slack.memory.root", filepath.Join(rootDir, ".slack", "conversations"))
	viper.SetDefault("slack.memory.max_section_chars", 2000)
	viper.SetDefault("slack.memory.include_thread_transcript", true)
	viper.SetDefault("slack.memory.max_transcript_chars", 8000)
	viper.SetDefault("slack.memory.max_transcript_records", 30)
	viper.SetDefault("slack.local_summarizer.enabled", false)
	viper.SetDefault("slack.local_summarizer.url", "http://localhost:8080")
	viper.SetDefault("slack.local_summarizer.max_tokens", 128)
	viper.SetDefault("slack.local_summarizer.timeout_seconds", 30)
	viper.SetDefault("slack.local_summarizer.min_chars", 300)
	viper.SetDefault("slack.agent.enabled", false)
	viper.SetDefault("slack.agent.backend", "codex")
	viper.SetDefault("slack.agent.binary", "")
	viper.SetDefault("slack.agent.args", []string{})
	viper.SetDefault("slack.agent.codex_binary", "codex")
	viper.SetDefault("slack.agent.ack_text", "")
	viper.SetDefault("slack.agent.result_max_chars", 3500)
	viper.SetDefault("slack.agent.timeout_minutes", 20)
	viper.SetDefault("slack.agent.session_resume", false)
	viper.SetDefault("webhook.listen_addr", "0.0.0.0:8080")
	viper.SetDefault("webhook.path", "/webhook/feishu")
	viper.SetDefault("webhook.event_log", filepath.Join(cfgDir, "webhook-events.jsonl"))

	viper.SetEnvPrefix("LARK")
	viper.BindEnv("app_id", "LARK_APP_ID")
	viper.BindEnv("app_secret", "LARK_APP_SECRET")
	viper.BindEnv("agent.enabled", "LARK_AGENT_ENABLED")
	viper.BindEnv("agent.backend", "LARK_AGENT_BACKEND")
	viper.BindEnv("agent.binary", "LARK_AGENT_BINARY")
	viper.BindEnv("agent.args", "LARK_AGENT_ARGS")
	viper.BindEnv("agent.codex_binary", "LARK_AGENT_CODEX_BINARY")
	viper.BindEnv("agent.workspace", "LARK_AGENT_WORKSPACE")
	viper.BindEnv("agent.model", "LARK_AGENT_MODEL")
	viper.BindEnv("agent.ack_text", "LARK_AGENT_ACK_TEXT")
	viper.BindEnv("agent.result_max_chars", "LARK_AGENT_RESULT_MAX_CHARS")
	viper.BindEnv("agent.timeout_minutes", "LARK_AGENT_TIMEOUT_MINUTES")
	viper.BindEnv("agent.session_resume", "LARK_AGENT_SESSION_RESUME")
	viper.BindEnv("gateway.event_log", "LARK_GATEWAY_EVENT_LOG")
	viper.BindEnv("gateway.auto_reply_text", "LARK_GATEWAY_AUTO_REPLY_TEXT")
	viper.BindEnv("slack.bot_token", "SLACK_BOT_TOKEN")
	viper.BindEnv("slack.app_token", "SLACK_APP_TOKEN")
	viper.BindEnv("slack.signing_secret", "SLACK_SIGNING_SECRET")
	viper.BindEnv("slack.bot_user_id", "SLACK_BOT_USER_ID")
	viper.BindEnv("slack.agent.enabled", "SLACK_AGENT_ENABLED")
	viper.BindEnv("slack.agent.backend", "SLACK_AGENT_BACKEND")
	viper.BindEnv("slack.agent.binary", "SLACK_AGENT_BINARY")
	viper.BindEnv("slack.agent.args", "SLACK_AGENT_ARGS")
	viper.BindEnv("slack.agent.codex_binary", "SLACK_AGENT_CODEX_BINARY")
	viper.BindEnv("slack.agent.workspace", "SLACK_AGENT_WORKSPACE")
	viper.BindEnv("slack.agent.model", "SLACK_AGENT_MODEL")
	viper.BindEnv("slack.agent.ack_text", "SLACK_AGENT_ACK_TEXT")
	viper.BindEnv("slack.agent.result_max_chars", "SLACK_AGENT_RESULT_MAX_CHARS")
	viper.BindEnv("slack.agent.timeout_minutes", "SLACK_AGENT_TIMEOUT_MINUTES")
	viper.BindEnv("slack.agent.session_resume", "SLACK_AGENT_SESSION_RESUME")
	viper.BindEnv("slack.gateway.event_log", "SLACK_GATEWAY_EVENT_LOG")
	viper.BindEnv("slack.gateway.auto_reply_text", "SLACK_GATEWAY_AUTO_REPLY_TEXT")
	viper.BindEnv("slack.gateway.recover_mode", "SLACK_GATEWAY_RECOVER_MODE")
	viper.BindEnv("slack.gateway.processing_reaction", "SLACK_GATEWAY_PROCESSING_REACTION")
	viper.BindEnv("slack.memory.enabled", "SLACK_MEMORY_ENABLED")
	viper.BindEnv("slack.memory.root", "SLACK_MEMORY_ROOT")
	viper.BindEnv("slack.memory.max_section_chars", "SLACK_MEMORY_MAX_SECTION_CHARS")
	viper.BindEnv("slack.memory.include_thread_transcript", "SLACK_MEMORY_INCLUDE_THREAD_TRANSCRIPT")
	viper.BindEnv("slack.memory.max_transcript_chars", "SLACK_MEMORY_MAX_TRANSCRIPT_CHARS")
	viper.BindEnv("slack.memory.max_transcript_records", "SLACK_MEMORY_MAX_TRANSCRIPT_RECORDS")
	viper.BindEnv("slack.local_summarizer.enabled", "SLACK_LOCAL_SUMMARIZER_ENABLED")
	viper.BindEnv("slack.local_summarizer.url", "SLACK_LOCAL_SUMMARIZER_URL")
	viper.BindEnv("slack.local_summarizer.max_tokens", "SLACK_LOCAL_SUMMARIZER_MAX_TOKENS")
	viper.BindEnv("slack.local_summarizer.timeout_seconds", "SLACK_LOCAL_SUMMARIZER_TIMEOUT_SECONDS")
	viper.BindEnv("slack.local_summarizer.min_chars", "SLACK_LOCAL_SUMMARIZER_MIN_CHARS")
	viper.BindEnv("webhook.listen_addr", "LARK_WEBHOOK_LISTEN")
	viper.BindEnv("webhook.path", "LARK_WEBHOOK_PATH")
	viper.BindEnv("webhook.verification_token", "LARK_WEBHOOK_TOKEN")
	viper.BindEnv("webhook.event_log", "LARK_WEBHOOK_EVENT_LOG")
	viper.BindEnv("webhook.auto_reply_text", "LARK_WEBHOOK_AUTO_REPLY_TEXT")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("error reading config: %w", err)
		}
	}

	cfg = &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return nil
}

// Get returns the current configuration
func Get() *Config {
	if cfg == nil {
		cfg = &Config{}
	}
	return cfg
}

// GetAppID returns the app ID from config or environment
func GetAppID() string {
	return viper.GetString("app_id")
}

// GetAppSecret returns the app secret from environment
func GetAppSecret() string {
	return viper.GetString("app_secret")
}

// GetRegion returns the API/auth region: lark (default) or feishu
func GetRegion() string {
	region := strings.ToLower(strings.TrimSpace(viper.GetString("region")))
	switch region {
	case "feishu":
		return "feishu"
	case "lark", "":
		return "lark"
	default:
		return "lark"
	}
}

// GetTimezone returns the default timezone
func GetTimezone() string {
	return viper.GetString("defaults.timezone")
}

// GetRedirectPort returns the OAuth redirect port
func GetRedirectPort() int {
	return viper.GetInt("oauth.redirect_port")
}

// GetAgentEnabled returns whether inbound messages should be dispatched to local Codex tasks.
func GetAgentEnabled() bool {
	return viper.GetBool("agent.enabled")
}

// GetAgentBackend returns the local agent backend name.
func GetAgentBackend() string {
	return strings.TrimSpace(viper.GetString("agent.backend"))
}

// GetAgentBinary returns the neutral local agent binary path or command name.
func GetAgentBinary() string {
	return strings.TrimSpace(viper.GetString("agent.binary"))
}

// GetAgentArgs returns extra arguments appended to the local agent backend command.
func GetAgentArgs() []string {
	return cleanStringSlice(viper.GetStringSlice("agent.args"), viper.GetString("agent.args"))
}

// GetAgentCodexBinary returns the codex binary path or command name.
func GetAgentCodexBinary() string {
	return strings.TrimSpace(viper.GetString("agent.codex_binary"))
}

// GetAgentWorkspace returns the workspace root used for Codex tasks.
func GetAgentWorkspace() string {
	path := strings.TrimSpace(viper.GetString("agent.workspace"))
	if path != "" {
		if !filepath.IsAbs(path) {
			return filepath.Join(rootDir, path)
		}
		return path
	}

	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "WorkSpace")
		if stat, statErr := os.Stat(candidate); statErr == nil && stat.IsDir() {
			return candidate
		}
	}

	if wd, wdErr := os.Getwd(); wdErr == nil {
		return wd
	}

	return rootDir
}

// GetAgentModel returns the optional model override for Codex tasks.
func GetAgentModel() string {
	return strings.TrimSpace(viper.GetString("agent.model"))
}

// GetAgentAckText returns the acknowledgement text sent immediately after accepting a task.
func GetAgentAckText() string {
	return viper.GetString("agent.ack_text")
}

// GetAgentResultMaxChars returns the maximum reply length for Feishu messages.
func GetAgentResultMaxChars() int {
	return viper.GetInt("agent.result_max_chars")
}

// GetAgentTimeoutMinutes returns the maximum runtime for a single Codex task.
func GetAgentTimeoutMinutes() int {
	return viper.GetInt("agent.timeout_minutes")
}

// GetGatewayEventLogPath returns the JSONL path used for gateway event persistence.
func GetGatewayEventLogPath() string {
	path := strings.TrimSpace(viper.GetString("gateway.event_log"))
	if path == "" {
		return filepath.Join(cfgDir, "gateway-events.jsonl")
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(rootDir, path)
	}
	return path
}

// GetGatewayAutoReplyText returns the optional static auto-reply text.
func GetGatewayAutoReplyText() string {
	return viper.GetString("gateway.auto_reply_text")
}

// GetSlackBotToken returns the Slack bot token used for Web API calls.
func GetSlackBotToken() string {
	return strings.TrimSpace(viper.GetString("slack.bot_token"))
}

// GetSlackAppToken returns the Slack app-level token used for Socket Mode.
func GetSlackAppToken() string {
	return strings.TrimSpace(viper.GetString("slack.app_token"))
}

// GetSlackSigningSecret returns the Slack signing secret for future webhook mode.
func GetSlackSigningSecret() string {
	return strings.TrimSpace(viper.GetString("slack.signing_secret"))
}

// GetSlackBotUserID returns the optional Slack bot user ID.
func GetSlackBotUserID() string {
	return strings.TrimSpace(viper.GetString("slack.bot_user_id"))
}

// GetSlackGatewayEventLogPath returns the JSONL path used for Slack gateway persistence.
func GetSlackGatewayEventLogPath() string {
	path := strings.TrimSpace(viper.GetString("slack.gateway.event_log"))
	if path == "" {
		return filepath.Join(rootDir, ".slack", "gateway-events.jsonl")
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(rootDir, path)
	}
	return path
}

// GetSlackGatewayAutoReplyText returns the optional Slack gateway auto-reply text.
func GetSlackGatewayAutoReplyText() string {
	return viper.GetString("slack.gateway.auto_reply_text")
}

// GetSlackGatewayRecoverMode returns the Slack catch-up mode.
func GetSlackGatewayRecoverMode() string {
	return strings.TrimSpace(viper.GetString("slack.gateway.recover_mode"))
}

// GetSlackGatewayProcessingReaction returns the Slack processing reaction name.
func GetSlackGatewayProcessingReaction() string {
	return strings.Trim(strings.TrimSpace(viper.GetString("slack.gateway.processing_reaction")), ":")
}

// GetSlackDesktopTaskRoot returns the Slack-specific desktop queue root.
func GetSlackDesktopTaskRoot() string {
	return filepath.Join(rootDir, ".slack", "desktop-tasks")
}

// GetSlackMemoryEnabled returns whether Slack memory/audit folders are enabled.
func GetSlackMemoryEnabled() bool {
	return viper.GetBool("slack.memory.enabled")
}

// GetSlackMemoryRoot returns the root directory for Slack conversation memory.
func GetSlackMemoryRoot() string {
	path := strings.TrimSpace(viper.GetString("slack.memory.root"))
	if path == "" {
		return filepath.Join(rootDir, ".slack", "conversations")
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(rootDir, path)
	}
	return path
}

// GetSlackMemoryMaxSectionChars returns the per-section prompt context limit.
func GetSlackMemoryMaxSectionChars() int {
	maxChars := viper.GetInt("slack.memory.max_section_chars")
	if maxChars <= 0 {
		return 2000
	}
	return maxChars
}

// GetSlackMemoryIncludeThreadTranscript returns whether recent thread transcript context is injected.
func GetSlackMemoryIncludeThreadTranscript() bool {
	return viper.GetBool("slack.memory.include_thread_transcript")
}

// GetSlackMemoryMaxTranscriptChars returns the recent transcript prompt context limit.
func GetSlackMemoryMaxTranscriptChars() int {
	maxChars := viper.GetInt("slack.memory.max_transcript_chars")
	if maxChars <= 0 {
		return 8000
	}
	return maxChars
}

// GetSlackMemoryMaxTranscriptRecords returns the recent transcript record limit.
func GetSlackMemoryMaxTranscriptRecords() int {
	maxRecords := viper.GetInt("slack.memory.max_transcript_records")
	if maxRecords <= 0 {
		return 30
	}
	return maxRecords
}

// GetSlackLocalSummarizerEnabled returns whether the local summarizer is enabled for Slack transcripts.
func GetSlackLocalSummarizerEnabled() bool {
	return viper.GetBool("slack.local_summarizer.enabled")
}

// GetSlackLocalSummarizerURL returns the base URL of the local summarizer service.
func GetSlackLocalSummarizerURL() string {
	return strings.TrimSpace(viper.GetString("slack.local_summarizer.url"))
}

// GetSlackLocalSummarizerMaxTokens returns the token budget for each summarization call.
func GetSlackLocalSummarizerMaxTokens() int {
	v := viper.GetInt("slack.local_summarizer.max_tokens")
	if v <= 0 {
		return 128
	}
	return v
}

// GetSlackLocalSummarizerTimeoutSeconds returns the HTTP timeout for summarization calls.
func GetSlackLocalSummarizerTimeoutSeconds() int {
	v := viper.GetInt("slack.local_summarizer.timeout_seconds")
	if v <= 0 {
		return 30
	}
	return v
}

// GetSlackLocalSummarizerMinChars returns the minimum outbound record length before summarization is attempted.
func GetSlackLocalSummarizerMinChars() int {
	v := viper.GetInt("slack.local_summarizer.min_chars")
	if v <= 0 {
		return 300
	}
	return v
}

// GetSlackAgentEnabled returns whether Slack messages should dispatch to Codex.
func GetSlackAgentEnabled() bool {
	return viper.GetBool("slack.agent.enabled")
}

// GetSlackAgentBackend returns the Slack local agent backend name.
func GetSlackAgentBackend() string {
	return strings.TrimSpace(viper.GetString("slack.agent.backend"))
}

// GetSlackAgentBinary returns the neutral Slack local agent binary path or command name.
func GetSlackAgentBinary() string {
	return strings.TrimSpace(viper.GetString("slack.agent.binary"))
}

// GetSlackAgentArgs returns extra arguments appended to the Slack local agent backend command.
func GetSlackAgentArgs() []string {
	return cleanStringSlice(viper.GetStringSlice("slack.agent.args"), viper.GetString("slack.agent.args"))
}

// GetSlackAgentCodexBinary returns the codex binary path or command name for Slack tasks.
func GetSlackAgentCodexBinary() string {
	return strings.TrimSpace(viper.GetString("slack.agent.codex_binary"))
}

// GetSlackAgentWorkspace returns the workspace root used for Slack Codex tasks.
func GetSlackAgentWorkspace() string {
	path := strings.TrimSpace(viper.GetString("slack.agent.workspace"))
	if path != "" {
		if !filepath.IsAbs(path) {
			return filepath.Join(rootDir, path)
		}
		return path
	}
	return GetAgentWorkspace()
}

// GetSlackAgentModel returns the optional model override for Slack Codex tasks.
func GetSlackAgentModel() string {
	return strings.TrimSpace(viper.GetString("slack.agent.model"))
}

// GetSlackAgentAckText returns the Slack acknowledgement text.
func GetSlackAgentAckText() string {
	return viper.GetString("slack.agent.ack_text")
}

// GetSlackAgentResultMaxChars returns the maximum Slack reply length.
func GetSlackAgentResultMaxChars() int {
	return viper.GetInt("slack.agent.result_max_chars")
}

// GetSlackAgentTimeoutMinutes returns the maximum runtime for a Slack Codex task.
func GetSlackAgentTimeoutMinutes() int {
	return viper.GetInt("slack.agent.timeout_minutes")
}

// GetAgentSessionResume returns whether Codex session resume is enabled.
func GetAgentSessionResume() bool {
	return viper.GetBool("agent.session_resume")
}

// GetSlackAgentSessionResume returns whether Slack Codex session resume is enabled.
func GetSlackAgentSessionResume() bool {
	return viper.GetBool("slack.agent.session_resume")
}

// GetWebhookListenAddr returns the listen address for webhook server.
func GetWebhookListenAddr() string {
	return viper.GetString("webhook.listen_addr")
}

// GetWebhookPath returns the webhook callback path.
func GetWebhookPath() string {
	path := strings.TrimSpace(viper.GetString("webhook.path"))
	if path == "" {
		return "/webhook/feishu"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

// GetWebhookVerificationToken returns the verification token for event callbacks.
func GetWebhookVerificationToken() string {
	return viper.GetString("webhook.verification_token")
}

// GetWebhookEventLogPath returns the JSONL path used for webhook event persistence.
func GetWebhookEventLogPath() string {
	path := strings.TrimSpace(viper.GetString("webhook.event_log"))
	if path == "" {
		return filepath.Join(cfgDir, "webhook-events.jsonl")
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(rootDir, path)
	}
	return path
}

// GetWebhookAutoReplyText returns the optional static auto-reply text.
func GetWebhookAutoReplyText() string {
	return viper.GetString("webhook.auto_reply_text")
}

// TokensFilePath returns the path to the tokens file
func TokensFilePath() string {
	return filepath.Join(cfgDir, "tokens.json")
}

// TenantTokensFilePath returns the path to the tenant tokens file
func TenantTokensFilePath() string {
	return filepath.Join(cfgDir, "tenant_tokens.json")
}

// GetCustomEmojis returns the custom emoji mappings
func GetCustomEmojis() map[string]string {
	return viper.GetStringMapString("custom_emojis")
}

func cleanStringSlice(values []string, raw string) []string {
	if strings.Contains(raw, ",") {
		values = strings.Split(raw, ",")
	} else if len(values) == 0 && strings.TrimSpace(raw) != "" {
		values = strings.Split(raw, ",")
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
