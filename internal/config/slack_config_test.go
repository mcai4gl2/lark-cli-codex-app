package config

import (
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestSlackConfigDefaultsAndEnvBindings(t *testing.T) {
	viper.Reset()
	tmp := t.TempDir()
	t.Setenv("LARK_CONFIG_DIR", filepath.Join(tmp, ".lark"))
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	t.Setenv("SLACK_BOT_USER_ID", "U999")
	t.Setenv("SLACK_AGENT_ENABLED", "true")
	t.Setenv("SLACK_AGENT_WORKSPACE", "slack-work")
	t.Setenv("SLACK_AGENT_RESULT_MAX_CHARS", "3333")
	t.Setenv("SLACK_GATEWAY_EVENT_LOG", "custom/slack-events.jsonl")
	t.Setenv("SLACK_GATEWAY_RECOVER_MODE", "mention-dm")
	t.Setenv("SLACK_GATEWAY_PROCESSING_REACTION", ":hourglass_flowing_sand:")
	t.Setenv("SLACK_MEMORY_ENABLED", "true")
	t.Setenv("SLACK_MEMORY_ROOT", "custom/slack-memory")
	t.Setenv("SLACK_MEMORY_MAX_SECTION_CHARS", "1234")
	t.Setenv("SLACK_MEMORY_INCLUDE_THREAD_TRANSCRIPT", "false")
	t.Setenv("SLACK_MEMORY_MAX_TRANSCRIPT_CHARS", "4321")
	t.Setenv("SLACK_MEMORY_MAX_TRANSCRIPT_RECORDS", "12")

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if got := GetSlackBotToken(); got != "xoxb-test" {
		t.Fatalf("GetSlackBotToken() = %q", got)
	}
	if got := GetSlackAppToken(); got != "xapp-test" {
		t.Fatalf("GetSlackAppToken() = %q", got)
	}
	if got := GetSlackBotUserID(); got != "U999" {
		t.Fatalf("GetSlackBotUserID() = %q", got)
	}
	if !GetSlackAgentEnabled() {
		t.Fatalf("GetSlackAgentEnabled() = false")
	}
	if got := GetSlackAgentWorkspace(); got != filepath.Join(tmp, "slack-work") {
		t.Fatalf("GetSlackAgentWorkspace() = %q", got)
	}
	if got := GetSlackAgentResultMaxChars(); got != 3333 {
		t.Fatalf("GetSlackAgentResultMaxChars() = %d", got)
	}
	if got := GetSlackGatewayEventLogPath(); got != filepath.Join(tmp, "custom/slack-events.jsonl") {
		t.Fatalf("GetSlackGatewayEventLogPath() = %q", got)
	}
	if got := GetSlackGatewayRecoverMode(); got != "mention-dm" {
		t.Fatalf("GetSlackGatewayRecoverMode() = %q", got)
	}
	if got := GetSlackGatewayProcessingReaction(); got != "hourglass_flowing_sand" {
		t.Fatalf("GetSlackGatewayProcessingReaction() = %q", got)
	}
	if !GetSlackMemoryEnabled() {
		t.Fatalf("GetSlackMemoryEnabled() = false")
	}
	if got := GetSlackMemoryRoot(); got != filepath.Join(tmp, "custom/slack-memory") {
		t.Fatalf("GetSlackMemoryRoot() = %q", got)
	}
	if got := GetSlackMemoryMaxSectionChars(); got != 1234 {
		t.Fatalf("GetSlackMemoryMaxSectionChars() = %d", got)
	}
	if GetSlackMemoryIncludeThreadTranscript() {
		t.Fatalf("GetSlackMemoryIncludeThreadTranscript() = true")
	}
	if got := GetSlackMemoryMaxTranscriptChars(); got != 4321 {
		t.Fatalf("GetSlackMemoryMaxTranscriptChars() = %d", got)
	}
	if got := GetSlackMemoryMaxTranscriptRecords(); got != 12 {
		t.Fatalf("GetSlackMemoryMaxTranscriptRecords() = %d", got)
	}
}

func TestSlackConfigDefaultsUseSlackStateDir(t *testing.T) {
	viper.Reset()
	tmp := t.TempDir()
	t.Setenv("LARK_CONFIG_DIR", filepath.Join(tmp, ".lark"))

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if got := GetSlackGatewayEventLogPath(); got != filepath.Join(tmp, ".slack", "gateway-events.jsonl") {
		t.Fatalf("GetSlackGatewayEventLogPath() = %q", got)
	}
	if got := GetSlackGatewayRecoverMode(); got != "thread" {
		t.Fatalf("GetSlackGatewayRecoverMode() = %q", got)
	}
	if got := GetSlackGatewayProcessingReaction(); got != "eyes" {
		t.Fatalf("GetSlackGatewayProcessingReaction() = %q", got)
	}
	if got := GetSlackDesktopTaskRoot(); got != filepath.Join(tmp, ".slack", "desktop-tasks") {
		t.Fatalf("GetSlackDesktopTaskRoot() = %q", got)
	}
	if got := GetSlackMemoryRoot(); got != filepath.Join(tmp, ".slack", "conversations") {
		t.Fatalf("GetSlackMemoryRoot() = %q", got)
	}
	if GetSlackMemoryEnabled() {
		t.Fatalf("GetSlackMemoryEnabled() = true")
	}
	if got := GetSlackMemoryMaxSectionChars(); got != 2000 {
		t.Fatalf("GetSlackMemoryMaxSectionChars() = %d", got)
	}
	if !GetSlackMemoryIncludeThreadTranscript() {
		t.Fatalf("GetSlackMemoryIncludeThreadTranscript() = false")
	}
	if got := GetSlackMemoryMaxTranscriptChars(); got != 8000 {
		t.Fatalf("GetSlackMemoryMaxTranscriptChars() = %d", got)
	}
	if got := GetSlackMemoryMaxTranscriptRecords(); got != 30 {
		t.Fatalf("GetSlackMemoryMaxTranscriptRecords() = %d", got)
	}
	if got := GetSlackAgentResultMaxChars(); got != 3500 {
		t.Fatalf("GetSlackAgentResultMaxChars() = %d", got)
	}
	if got := GetSlackAgentAckText(); got != "" {
		t.Fatalf("GetSlackAgentAckText() = %q", got)
	}
}

func TestSlackMemoryLimitFallbacks(t *testing.T) {
	viper.Reset()
	tmp := t.TempDir()
	t.Setenv("LARK_CONFIG_DIR", filepath.Join(tmp, ".lark"))
	t.Setenv("SLACK_MEMORY_MAX_SECTION_CHARS", "0")
	t.Setenv("SLACK_MEMORY_MAX_TRANSCRIPT_CHARS", "0")
	t.Setenv("SLACK_MEMORY_MAX_TRANSCRIPT_RECORDS", "-1")

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if got := GetSlackMemoryMaxSectionChars(); got != 2000 {
		t.Fatalf("GetSlackMemoryMaxSectionChars() = %d", got)
	}
	if got := GetSlackMemoryMaxTranscriptChars(); got != 8000 {
		t.Fatalf("GetSlackMemoryMaxTranscriptChars() = %d", got)
	}
	if got := GetSlackMemoryMaxTranscriptRecords(); got != 30 {
		t.Fatalf("GetSlackMemoryMaxTranscriptRecords() = %d", got)
	}
}
