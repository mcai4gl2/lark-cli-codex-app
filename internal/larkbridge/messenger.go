package larkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yjwong/lark-cli/internal/api"
	"github.com/yjwong/lark-cli/internal/platform"
)

type larkAPI interface {
	ReplyMessage(messageID, msgType, content, rootID string, replyInThread bool) (*api.SendMessageResponse, error)
	SendMessage(receiveIDType, receiveID, msgType, content string) (*api.SendMessageResponse, error)
}

// Messenger adapts the existing Lark API client to the shared platform
// messenger contract.
type Messenger struct {
	client larkAPI
}

func NewMessenger(client larkAPI) *Messenger {
	if client == nil {
		client = api.NewClient()
	}
	return &Messenger{client: client}
}

func (m *Messenger) Reply(_ context.Context, event platform.MessageEvent, text string) error {
	if strings.TrimSpace(event.MessageID) == "" {
		return fmt.Errorf("lark reply requires message_id")
	}
	content, err := textContent(text)
	if err != nil {
		return err
	}

	rootID := event.ThreadID
	if rootID == event.MessageID {
		rootID = ""
	}
	_, err = m.client.ReplyMessage(event.MessageID, "text", content, rootID, true)
	return err
}

func (m *Messenger) Send(_ context.Context, target platform.MessageTarget, text string) error {
	receiveIDType := "chat_id"
	receiveID := strings.TrimSpace(target.ChannelID)
	if receiveID == "" {
		receiveIDType = "open_id"
		receiveID = strings.TrimSpace(target.UserID)
	}
	if receiveID == "" {
		return fmt.Errorf("lark send requires channel_id or user_id")
	}

	content, err := textContent(text)
	if err != nil {
		return err
	}
	_, err = m.client.SendMessage(receiveIDType, receiveID, "text", content)
	return err
}

func textContent(text string) (string, error) {
	payload, err := json.Marshal(map[string]string{"text": strings.TrimSpace(text)})
	if err != nil {
		return "", fmt.Errorf("marshal lark text content: %w", err)
	}
	return string(payload), nil
}
