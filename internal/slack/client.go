package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

const defaultAPIBaseURL = "https://slack.com/api"

// ClientConfig configures Slack Web API calls.
type ClientConfig struct {
	BotToken string
	BaseURL  string
	Client   *http.Client
}

// Client is a minimal Slack Web API messenger.
type Client struct {
	botToken string
	baseURL  string
	client   *http.Client
}

// AuthInfo is the subset of auth.test needed by the gateway.
type AuthInfo struct {
	UserID string `json:"user_id"`
	BotID  string `json:"bot_id"`
	TeamID string `json:"team_id"`
}

type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// NewClient returns a Slack Web API client.
func NewClient(cfg ClientConfig) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		botToken: strings.TrimSpace(cfg.BotToken),
		baseURL:  baseURL,
		client:   httpClient,
	}
}

// Reply sends a threaded reply to the originating Slack message.
func (c *Client) Reply(ctx context.Context, event platform.MessageEvent, text string) error {
	threadTS := event.ThreadID
	if threadTS == "" {
		threadTS = event.MessageID
	}
	return c.postMessage(ctx, event.ChannelID, threadTS, text)
}

// Send sends a Slack message to a target channel, optionally in a thread.
func (c *Client) Send(ctx context.Context, target platform.MessageTarget, text string) error {
	return c.postMessage(ctx, target.ChannelID, target.ThreadID, text)
}

// AuthTest calls Slack auth.test.
func (c *Client) AuthTest(ctx context.Context) (AuthInfo, error) {
	var response struct {
		slackResponse
		AuthInfo
	}
	if err := c.call(ctx, "auth.test", map[string]string{}, &response); err != nil {
		return AuthInfo{}, err
	}
	if !response.OK {
		return AuthInfo{}, fmt.Errorf("slack auth.test failed: %s", response.Error)
	}
	return response.AuthInfo, nil
}

func (c *Client) postMessage(ctx context.Context, channel, threadTS, text string) error {
	channel = strings.TrimSpace(channel)
	text = strings.TrimSpace(text)
	if channel == "" {
		return fmt.Errorf("slack channel is required")
	}
	if text == "" {
		return fmt.Errorf("slack message text is required")
	}

	payload := map[string]interface{}{
		"channel":      channel,
		"text":         text,
		"unfurl_links": false,
		"unfurl_media": false,
	}
	if strings.TrimSpace(threadTS) != "" {
		payload["thread_ts"] = strings.TrimSpace(threadTS)
	}

	var response slackResponse
	if err := c.call(ctx, "chat.postMessage", payload, &response); err != nil {
		return err
	}
	if !response.OK {
		return fmt.Errorf("slack chat.postMessage failed: %s", response.Error)
	}
	return nil
}

func (c *Client) call(ctx context.Context, method string, payload interface{}, out interface{}) error {
	if c.botToken == "" {
		return fmt.Errorf("SLACK_BOT_TOKEN is required")
	}
	return c.callWithToken(ctx, method, payload, out, c.botToken)
}

func (c *Client) callWithToken(ctx context.Context, method string, payload interface{}, out interface{}, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("slack token is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build slack request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call slack %s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack %s returned HTTP %d", method, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode slack %s response: %w", method, err)
	}
	return nil
}
