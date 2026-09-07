package weixin

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

// Provider is the platform.MessageEvent provider name for this channel.
const Provider = "weixin"

// NormalizeMessage converts an inbound Weixin message into the shared event
// shape. It reports false for messages the gateway must ignore: the bot's own
// echoes and anything with no usable text body.
//
// WeChat has no threads, so ChannelID is the sender and ThreadID is the
// server's session id (falling back to the sender). That keeps one agent
// session per conversation, which is what agent.SessionKey expects.
func NormalizeMessage(msg Message, accountID string) (platform.MessageEvent, bool) {
	if msg.MessageType == MessageTypeBot {
		return platform.MessageEvent{}, false
	}
	from := strings.TrimSpace(msg.FromUserID)
	if from == "" {
		return platform.MessageEvent{}, false
	}

	body := strings.TrimSpace(BodyFromItems(msg.ItemList))
	if body == "" {
		return platform.MessageEvent{}, false
	}

	threadID := strings.TrimSpace(msg.SessionID)
	if threadID == "" {
		threadID = from
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		raw = nil
	}

	receivedAt := time.Now().Format(time.RFC3339Nano)

	return platform.MessageEvent{
		Provider:    Provider,
		ReceivedAt:  receivedAt,
		EventType:   "message",
		TeamID:      accountID,
		ChannelID:   from,
		ChannelType: "direct",
		MessageID:   messageIDString(msg),
		ThreadID:    threadID,
		UserID:      from,
		MessageType: "text",
		MessageText: body,
		RawContent:  body,
		RawEvent:    raw,
	}, true
}

// messageIDString renders the numeric server message id, falling back to the
// sequence number so every event carries a stable identifier.
func messageIDString(msg Message) string {
	if msg.MessageID != 0 {
		return strconv.FormatInt(msg.MessageID, 10)
	}
	if msg.Seq != 0 {
		return "seq-" + strconv.FormatInt(msg.Seq, 10)
	}
	return ""
}

// BodyFromItems extracts the human-readable body from a message's item list.
// A text item wins; a quoted message is prefixed as "[引用: …]". A voice item
// contributes its server-side transcription when one is present.
func BodyFromItems(items []MessageItem) string {
	for _, item := range items {
		switch item.Type {
		case ItemTypeText:
			if item.TextItem == nil {
				continue
			}
			text := item.TextItem.Text
			if item.RefMsg == nil {
				return text
			}
			quoted := quotedContext(item.RefMsg)
			if quoted == "" {
				return text
			}
			return "[引用: " + quoted + "]\n" + text
		case ItemTypeVoice:
			if item.VoiceItem != nil && strings.TrimSpace(item.VoiceItem.Text) != "" {
				return item.VoiceItem.Text
			}
		}
	}
	return ""
}

// quotedContext renders a quoted message's summary. Quoted media carries no
// text, so it contributes nothing beyond its title.
func quotedContext(ref *RefMessage) string {
	parts := make([]string, 0, 2)
	if title := strings.TrimSpace(ref.Title); title != "" {
		parts = append(parts, title)
	}
	if ref.MessageItem != nil && !IsMediaItem(*ref.MessageItem) {
		if body := strings.TrimSpace(BodyFromItems([]MessageItem{*ref.MessageItem})); body != "" {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, " | ")
}

// IsMediaItem reports whether an item carries an attachment rather than text.
func IsMediaItem(item MessageItem) bool {
	switch item.Type {
	case ItemTypeImage, ItemTypeVideo, ItemTypeFile, ItemTypeVoice:
		return true
	default:
		return false
	}
}

// HasUnsupportedVoice reports whether the message is a voice clip that arrived
// without server-side transcription, which this channel cannot interpret.
func HasUnsupportedVoice(items []MessageItem) bool {
	for _, item := range items {
		if item.Type != ItemTypeVoice {
			continue
		}
		if item.VoiceItem == nil || strings.TrimSpace(item.VoiceItem.Text) == "" {
			return true
		}
	}
	return false
}
