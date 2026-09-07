package weixin

import (
	"encoding/json"
	"strings"
	"testing"
)

func textMessage(from, text string) Message {
	return Message{
		Seq:         1,
		MessageID:   1001,
		FromUserID:  from,
		MessageType: MessageTypeUser,
		ItemList:    []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: text}}},
	}
}

func TestNormalizeMessage(t *testing.T) {
	tests := []struct {
		name      string
		msg       Message
		wantOK    bool
		wantText  string
		wantThrd  string
		wantMsgID string
	}{
		{
			name:      "plain text",
			msg:       textMessage("u1", "hello"),
			wantOK:    true,
			wantText:  "hello",
			wantThrd:  "u1",
			wantMsgID: "1001",
		},
		{
			name: "session id becomes the thread id",
			msg: Message{
				Seq: 2, MessageID: 1002, FromUserID: "u1", SessionID: "s-1",
				MessageType: MessageTypeUser,
				ItemList:    []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "hi"}}},
			},
			wantOK:    true,
			wantText:  "hi",
			wantThrd:  "s-1",
			wantMsgID: "1002",
		},
		{
			name: "voice uses the server transcription",
			msg: Message{
				Seq: 3, MessageID: 1003, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{Type: ItemTypeVoice, VoiceItem: &VoiceItem{Text: "转写内容"}}},
			},
			wantOK:    true,
			wantText:  "转写内容",
			wantThrd:  "u1",
			wantMsgID: "1003",
		},
		{
			name: "quoted text is prefixed",
			msg: Message{
				Seq: 4, MessageID: 1004, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{
					Type:     ItemTypeText,
					TextItem: &TextItem{Text: "follow up"},
					RefMsg: &RefMessage{
						Title:       "摘要",
						MessageItem: &MessageItem{Type: ItemTypeText, TextItem: &TextItem{Text: "原文"}},
					},
				}},
			},
			wantOK:    true,
			wantText:  "[引用: 摘要 | 原文]\nfollow up",
			wantThrd:  "u1",
			wantMsgID: "1004",
		},
		{
			name: "quoted media contributes only its title",
			msg: Message{
				Seq: 5, MessageID: 1005, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{
					Type:     ItemTypeText,
					TextItem: &TextItem{Text: "看这个"},
					RefMsg: &RefMessage{
						Title:       "[图片]",
						MessageItem: &MessageItem{Type: ItemTypeImage, ImageItem: &ImageItem{}},
					},
				}},
			},
			wantOK:    true,
			wantText:  "[引用: [图片]]\n看这个",
			wantThrd:  "u1",
			wantMsgID: "1005",
		},
		{
			name: "seq is the fallback message id",
			msg: Message{
				Seq: 7, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "x"}}},
			},
			wantOK:    true,
			wantText:  "x",
			wantThrd:  "u1",
			wantMsgID: "seq-7",
		},
		{
			name: "bot echoes are ignored",
			msg: Message{
				Seq: 8, MessageID: 1008, FromUserID: "u1", MessageType: MessageTypeBot,
				ItemList: []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "echo"}}},
			},
		},
		{
			name: "empty body is ignored",
			msg: Message{
				Seq: 9, MessageID: 1009, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "   "}}},
			},
		},
		{
			name: "voice without transcription is ignored",
			msg: Message{
				Seq: 10, MessageID: 1010, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{Type: ItemTypeVoice, VoiceItem: &VoiceItem{}}},
			},
		},
		{
			name: "missing sender is ignored",
			msg: Message{
				Seq: 11, MessageID: 1011, MessageType: MessageTypeUser,
				ItemList: []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "x"}}},
			},
		},
		{
			name: "tool call items alone are ignored",
			msg: Message{
				Seq: 12, MessageID: 1012, FromUserID: "u1", MessageType: MessageTypeUser,
				ItemList: []MessageItem{{Type: ItemTypeToolCallStart, ToolCallStartItem: &ToolCallStartItem{ToolName: "bash"}}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := NormalizeMessage(tc.msg, "acct-im-bot")
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if event.Provider != Provider {
				t.Fatalf("Provider = %q", event.Provider)
			}
			if event.TeamID != "acct-im-bot" {
				t.Fatalf("TeamID = %q", event.TeamID)
			}
			if event.ChannelID != tc.msg.FromUserID || event.UserID != tc.msg.FromUserID {
				t.Fatalf("ChannelID = %q UserID = %q", event.ChannelID, event.UserID)
			}
			if event.ChannelType != "direct" {
				t.Fatalf("ChannelType = %q", event.ChannelType)
			}
			if event.MessageText != tc.wantText {
				t.Fatalf("MessageText = %q, want %q", event.MessageText, tc.wantText)
			}
			if event.ThreadID != tc.wantThrd {
				t.Fatalf("ThreadID = %q, want %q", event.ThreadID, tc.wantThrd)
			}
			if event.MessageID != tc.wantMsgID {
				t.Fatalf("MessageID = %q, want %q", event.MessageID, tc.wantMsgID)
			}
			if event.ReceivedAt == "" {
				t.Fatalf("ReceivedAt was not stamped")
			}
			var raw Message
			if err := json.Unmarshal(event.RawEvent, &raw); err != nil {
				t.Fatalf("RawEvent is not the original message: %v", err)
			}
			if raw.FromUserID != tc.msg.FromUserID {
				t.Fatalf("RawEvent.from_user_id = %q", raw.FromUserID)
			}
		})
	}
}

func TestHasUnsupportedVoice(t *testing.T) {
	if !HasUnsupportedVoice([]MessageItem{{Type: ItemTypeVoice, VoiceItem: &VoiceItem{}}}) {
		t.Fatalf("a voice item with no text should be reported as unsupported")
	}
	if HasUnsupportedVoice([]MessageItem{{Type: ItemTypeVoice, VoiceItem: &VoiceItem{Text: "转写"}}}) {
		t.Fatalf("a transcribed voice item is supported")
	}
	if HasUnsupportedVoice([]MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "x"}}}) {
		t.Fatalf("a text item is not a voice item")
	}
}

func TestIsMediaItem(t *testing.T) {
	media := []int{ItemTypeImage, ItemTypeVideo, ItemTypeFile, ItemTypeVoice}
	for _, itemType := range media {
		if !IsMediaItem(MessageItem{Type: itemType}) {
			t.Fatalf("item type %d should be media", itemType)
		}
	}
	if IsMediaItem(MessageItem{Type: ItemTypeText}) {
		t.Fatalf("a text item is not media")
	}
}

func TestNormalizeMessageKeepsSessionKeyStable(t *testing.T) {
	// Two messages in the same conversation must land on one agent session.
	first, _ := NormalizeMessage(Message{
		Seq: 1, MessageID: 1, FromUserID: "u1", SessionID: "s-1", MessageType: MessageTypeUser,
		ItemList: []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "a"}}},
	}, "acct")
	second, _ := NormalizeMessage(Message{
		Seq: 2, MessageID: 2, FromUserID: "u1", SessionID: "s-1", MessageType: MessageTypeUser,
		ItemList: []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "b"}}},
	}, "acct")

	if SessionKeyFromEvent(first) != SessionKeyFromEvent(second) {
		t.Fatalf("session keys diverged: %+v vs %+v", SessionKeyFromEvent(first), SessionKeyFromEvent(second))
	}
	if key := SessionKeyFromEvent(first); key.Provider != Provider || key.ChannelID != "u1" || key.ThreadTS != "s-1" {
		t.Fatalf("session key = %+v", key)
	}
}

func TestBodyFromItemsPrefersTheFirstTextItem(t *testing.T) {
	body := BodyFromItems([]MessageItem{
		{Type: ItemTypeImage, ImageItem: &ImageItem{}},
		{Type: ItemTypeText, TextItem: &TextItem{Text: "caption"}},
	})
	if body != "caption" {
		t.Fatalf("BodyFromItems() = %q", body)
	}
	if got := BodyFromItems(nil); got != "" {
		t.Fatalf("BodyFromItems(nil) = %q", got)
	}
	if got := strings.TrimSpace(BodyFromItems([]MessageItem{{Type: ItemTypeFile, FileItem: &FileItem{FileName: "a.pdf"}}})); got != "" {
		t.Fatalf("a bare file item has no text body, got %q", got)
	}
}
