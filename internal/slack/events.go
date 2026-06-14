package slack

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

var slackMentionPattern = regexp.MustCompile(`<@([A-Z0-9]+)(?:\|[^>]+)?>`)

type eventCallback struct {
	TeamID  string          `json:"team_id"`
	EventID string          `json:"event_id"`
	Event   slackInnerEvent `json:"event"`
	Raw     json.RawMessage `json:"-"`
}

type slackInnerEvent struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype,omitempty"`
	User        string `json:"user,omitempty"`
	BotID       string `json:"bot_id,omitempty"`
	Channel     string `json:"channel,omitempty"`
	ChannelType string `json:"channel_type,omitempty"`
	Text        string `json:"text,omitempty"`
	TS          string `json:"ts,omitempty"`
	ThreadTS    string `json:"thread_ts,omitempty"`
}

// NormalizeEvent converts a Slack Events API callback payload into a shared
// message event. The boolean is false for supported-but-ignored events.
func NormalizeEvent(payload []byte, botUserID string) (platform.MessageEvent, bool, error) {
	var callback eventCallback
	if err := json.Unmarshal(payload, &callback); err != nil {
		return platform.MessageEvent{}, false, fmt.Errorf("parse slack event callback: %w", err)
	}
	callback.Raw = append(callback.Raw, payload...)

	event := callback.Event
	if shouldIgnoreEvent(event, botUserID) {
		return platform.MessageEvent{}, false, nil
	}

	text := strings.TrimSpace(event.Text)
	if event.Type == "app_mention" {
		text = stripMention(text, botUserID)
	} else if event.Type != "message" || event.ChannelType != "im" {
		return platform.MessageEvent{}, false, nil
	}
	if strings.TrimSpace(text) == "" {
		return platform.MessageEvent{}, false, nil
	}

	threadID := strings.TrimSpace(event.ThreadTS)
	if threadID == "" {
		threadID = strings.TrimSpace(event.TS)
	}
	channelType := strings.TrimSpace(event.ChannelType)
	if channelType == "" {
		channelType = inferChannelType(event.Channel)
	}

	return platform.MessageEvent{
		Provider:    "slack",
		ReceivedAt:  time.Now().Format(time.RFC3339Nano),
		EventID:     callback.EventID,
		EventType:   event.Type,
		TeamID:      callback.TeamID,
		ChannelID:   event.Channel,
		ChannelType: channelType,
		MessageID:   event.TS,
		ThreadID:    threadID,
		UserID:      event.User,
		BotID:       strings.TrimSpace(botUserID),
		MessageType: "text",
		MessageText: text,
		RawContent:  event.Text,
		RawEvent:    callback.Raw,
	}, true, nil
}

func NormalizeThreadMessageForCatchUp(teamID, channelID, channelType, threadTS string, message Message, botUserID string, mode RecoverMode) (platform.MessageEvent, bool) {
	if mode == RecoverModeOff {
		return platform.MessageEvent{}, false
	}

	messageType := strings.TrimSpace(message.Type)
	if messageType != "" && messageType != "message" {
		return platform.MessageEvent{}, false
	}
	if !belongsToThread(message, threadTS) {
		return platform.MessageEvent{}, false
	}

	event := slackInnerEvent{
		Type:     "message",
		Subtype:  message.Subtype,
		User:     message.User,
		BotID:    message.BotID,
		Text:     message.Text,
		TS:       message.TS,
		ThreadTS: message.ThreadTS,
	}
	if shouldIgnoreEvent(event, botUserID) {
		return platform.MessageEvent{}, false
	}

	resolvedChannelType := strings.TrimSpace(channelType)
	if resolvedChannelType == "" {
		resolvedChannelType = inferChannelType(channelID)
	}

	text := strings.TrimSpace(message.Text)
	switch mode {
	case RecoverModeThread:
	case RecoverModeMentionDM:
		if resolvedChannelType != "im" {
			if !mentionsBot(text, botUserID) {
				return platform.MessageEvent{}, false
			}
			text = stripMention(text, botUserID)
		} else if mentionsBot(text, botUserID) {
			text = stripMention(text, botUserID)
		}
	default:
		return platform.MessageEvent{}, false
	}
	if strings.TrimSpace(text) == "" {
		return platform.MessageEvent{}, false
	}

	threadID := strings.TrimSpace(message.ThreadTS)
	if threadID == "" {
		threadID = strings.TrimSpace(threadTS)
	}
	if threadID == "" {
		threadID = strings.TrimSpace(message.TS)
	}

	return platform.MessageEvent{
		Provider:    "slack",
		ReceivedAt:  time.Now().Format(time.RFC3339Nano),
		EventType:   "message",
		TeamID:      teamID,
		ChannelID:   channelID,
		ChannelType: resolvedChannelType,
		MessageID:   message.TS,
		ThreadID:    threadID,
		UserID:      message.User,
		BotID:       strings.TrimSpace(botUserID),
		MessageType: "text",
		MessageText: text,
		RawContent:  message.Text,
	}, true
}

func belongsToThread(message Message, threadTS string) bool {
	threadTS = strings.TrimSpace(threadTS)
	if threadTS == "" {
		return true
	}
	return strings.TrimSpace(message.ThreadTS) == threadTS || strings.TrimSpace(message.TS) == threadTS
}

func shouldIgnoreEvent(event slackInnerEvent, botUserID string) bool {
	if strings.TrimSpace(event.Subtype) != "" {
		return true
	}
	if strings.TrimSpace(event.BotID) != "" {
		return true
	}
	if botUserID != "" && strings.TrimSpace(event.User) == strings.TrimSpace(botUserID) {
		return true
	}
	return false
}

func mentionsBot(text, botUserID string) bool {
	botUserID = strings.TrimSpace(botUserID)
	if botUserID == "" {
		return false
	}
	matches := slackMentionPattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) >= 2 && match[1] == botUserID {
			return true
		}
	}
	return false
}

func stripMention(text, botUserID string) string {
	text = slackMentionPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := slackMentionPattern.FindStringSubmatch(match)
		if len(parts) >= 2 && strings.TrimSpace(botUserID) != "" && parts[1] != strings.TrimSpace(botUserID) {
			return match
		}
		return " "
	})
	return strings.Join(strings.Fields(text), " ")
}

func inferChannelType(channel string) string {
	switch {
	case strings.HasPrefix(channel, "D"):
		return "im"
	case strings.HasPrefix(channel, "G"):
		return "group"
	case strings.HasPrefix(channel, "C"):
		return "channel"
	default:
		return ""
	}
}
