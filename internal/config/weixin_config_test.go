package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

func TestWeixinConfigDefaults(t *testing.T) {
	viper.Reset()
	tmp := t.TempDir()
	t.Setenv("LARK_CONFIG_DIR", filepath.Join(tmp, ".lark"))

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if got := GetWeixinBaseURL(); got != "https://ilinkai.weixin.qq.com" {
		t.Fatalf("GetWeixinBaseURL() = %q", got)
	}
	if got := GetWeixinCDNBaseURL(); got != "https://novac2c.cdn.weixin.qq.com/c2c" {
		t.Fatalf("GetWeixinCDNBaseURL() = %q", got)
	}
	if got := GetWeixinAppID(); got != "bot" {
		t.Fatalf("GetWeixinAppID() = %q", got)
	}
	if got := GetWeixinBotType(); got != "3" {
		t.Fatalf("GetWeixinBotType() = %q", got)
	}
	if got := GetWeixinStateDir(); got != filepath.Join(tmp, ".lark", "weixin") {
		t.Fatalf("GetWeixinStateDir() = %q", got)
	}
	if got := GetWeixinGatewayEventLogPath(); got != filepath.Join(tmp, ".weixin", "gateway-events.jsonl") {
		t.Fatalf("GetWeixinGatewayEventLogPath() = %q", got)
	}
	if got := GetWeixinDesktopTaskRoot(); got != filepath.Join(tmp, ".weixin", "desktop-tasks") {
		t.Fatalf("GetWeixinDesktopTaskRoot() = %q", got)
	}
	if got := GetWeixinMemoryRoot(); got != filepath.Join(tmp, ".weixin", "conversations") {
		t.Fatalf("GetWeixinMemoryRoot() = %q", got)
	}
	if got := GetWeixinGatewayLongPollTimeoutSeconds(); got != 35 {
		t.Fatalf("GetWeixinGatewayLongPollTimeoutSeconds() = %d", got)
	}
	if !GetWeixinGatewayTyping() {
		t.Fatalf("GetWeixinGatewayTyping() = false")
	}
	if got := GetWeixinGatewayAllowFrom(); len(got) != 0 {
		t.Fatalf("GetWeixinGatewayAllowFrom() = %v", got)
	}
	if GetWeixinAgentEnabled() {
		t.Fatalf("GetWeixinAgentEnabled() = true")
	}
	if got := GetWeixinAgentBackend(); got != "codex" {
		t.Fatalf("GetWeixinAgentBackend() = %q", got)
	}
	if got := GetWeixinAgentResultMaxChars(); got != 3500 {
		t.Fatalf("GetWeixinAgentResultMaxChars() = %d", got)
	}
	if got := GetWeixinAgentTimeoutMinutes(); got != 20 {
		t.Fatalf("GetWeixinAgentTimeoutMinutes() = %d", got)
	}
}

func TestWeixinConfigEnvBindings(t *testing.T) {
	viper.Reset()
	tmp := t.TempDir()
	t.Setenv("LARK_CONFIG_DIR", filepath.Join(tmp, ".lark"))
	t.Setenv("WEIXIN_BASE_URL", "https://example.test")
	t.Setenv("WEIXIN_CDN_BASE_URL", "https://cdn.example.test/c2c")
	t.Setenv("WEIXIN_APP_ID", "bot2")
	t.Setenv("WEIXIN_BOT_TYPE", "4")
	t.Setenv("WEIXIN_BOT_AGENT", "lark-cli/1.2.3")
	t.Setenv("WEIXIN_ROUTE_TAG", "tag-1")
	t.Setenv("WEIXIN_ACCOUNT_ID", "abc-im-bot")
	t.Setenv("WEIXIN_GATEWAY_ALLOW_FROM", "u1,u2")
	t.Setenv("WEIXIN_GATEWAY_LONG_POLL_TIMEOUT_SECONDS", "50")
	t.Setenv("WEIXIN_GATEWAY_TYPING", "false")
	t.Setenv("WEIXIN_GATEWAY_EVENT_LOG", "custom/weixin-events.jsonl")
	t.Setenv("WEIXIN_MEMORY_ENABLED", "true")
	t.Setenv("WEIXIN_MEMORY_ROOT", "custom/weixin-memory")
	t.Setenv("WEIXIN_AGENT_ENABLED", "true")
	t.Setenv("WEIXIN_AGENT_BACKEND", "grok")
	t.Setenv("WEIXIN_AGENT_WORKSPACE", "weixin-work")
	t.Setenv("WEIXIN_AGENT_RESULT_MAX_CHARS", "2222")
	t.Setenv("WEIXIN_AGENT_SESSION_RESUME", "true")

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if got := GetWeixinBaseURL(); got != "https://example.test" {
		t.Fatalf("GetWeixinBaseURL() = %q", got)
	}
	if got := GetWeixinCDNBaseURL(); got != "https://cdn.example.test/c2c" {
		t.Fatalf("GetWeixinCDNBaseURL() = %q", got)
	}
	if got := GetWeixinAppID(); got != "bot2" {
		t.Fatalf("GetWeixinAppID() = %q", got)
	}
	if got := GetWeixinBotType(); got != "4" {
		t.Fatalf("GetWeixinBotType() = %q", got)
	}
	if got := GetWeixinBotAgent(); got != "lark-cli/1.2.3" {
		t.Fatalf("GetWeixinBotAgent() = %q", got)
	}
	if got := GetWeixinRouteTag(); got != "tag-1" {
		t.Fatalf("GetWeixinRouteTag() = %q", got)
	}
	if got := GetWeixinAccountID(); got != "abc-im-bot" {
		t.Fatalf("GetWeixinAccountID() = %q", got)
	}
	if got := GetWeixinGatewayAllowFrom(); !reflect.DeepEqual(got, []string{"u1", "u2"}) {
		t.Fatalf("GetWeixinGatewayAllowFrom() = %v", got)
	}
	if got := GetWeixinGatewayLongPollTimeoutSeconds(); got != 50 {
		t.Fatalf("GetWeixinGatewayLongPollTimeoutSeconds() = %d", got)
	}
	if GetWeixinGatewayTyping() {
		t.Fatalf("GetWeixinGatewayTyping() = true")
	}
	if got := GetWeixinGatewayEventLogPath(); got != filepath.Join(tmp, "custom/weixin-events.jsonl") {
		t.Fatalf("GetWeixinGatewayEventLogPath() = %q", got)
	}
	if !GetWeixinMemoryEnabled() {
		t.Fatalf("GetWeixinMemoryEnabled() = false")
	}
	if got := GetWeixinMemoryRoot(); got != filepath.Join(tmp, "custom/weixin-memory") {
		t.Fatalf("GetWeixinMemoryRoot() = %q", got)
	}
	if !GetWeixinAgentEnabled() {
		t.Fatalf("GetWeixinAgentEnabled() = false")
	}
	if got := GetWeixinAgentBackend(); got != "grok" {
		t.Fatalf("GetWeixinAgentBackend() = %q", got)
	}
	if got := GetWeixinAgentWorkspace(); got != filepath.Join(tmp, "weixin-work") {
		t.Fatalf("GetWeixinAgentWorkspace() = %q", got)
	}
	if got := GetWeixinAgentResultMaxChars(); got != 2222 {
		t.Fatalf("GetWeixinAgentResultMaxChars() = %d", got)
	}
	if !GetWeixinAgentSessionResume() {
		t.Fatalf("GetWeixinAgentSessionResume() = false")
	}
}
