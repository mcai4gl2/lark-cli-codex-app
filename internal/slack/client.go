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

// Message is a compact Slack message representation for CLI output.
type Message struct {
	Type        string            `json:"type,omitempty"`
	Subtype     string            `json:"subtype,omitempty"`
	User        string            `json:"user,omitempty"`
	BotID       string            `json:"bot_id,omitempty"`
	Text        string            `json:"text,omitempty"`
	TS          string            `json:"ts"`
	ThreadTS    string            `json:"thread_ts,omitempty"`
	ReplyCount  int               `json:"reply_count,omitempty"`
	Channel     string            `json:"channel,omitempty"`
	ChannelName string            `json:"channel_name,omitempty"`
	Reactions   []ReactionSummary `json:"reactions,omitempty"`
}

// MessageList is the compact JSON response used by Slack message commands.
type MessageList struct {
	Messages []Message `json:"messages"`
	Count    int       `json:"count"`
	Channel  string    `json:"channel"`
	ThreadTS string    `json:"thread_ts,omitempty"`
	HasMore  bool      `json:"has_more,omitempty"`
}

// SendResult is the compact JSON response for Slack message sends.
type SendResult struct {
	Success  bool   `json:"success"`
	Channel  string `json:"channel"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts,omitempty"`
}

// ReactionSummary is a compact Slack reaction entry.
type ReactionSummary struct {
	Name  string   `json:"name"`
	Users []string `json:"users,omitempty"`
	Count int      `json:"count"`
}

// ReactionResult is the compact JSON response for adding or removing a reaction.
type ReactionResult struct {
	Success  bool   `json:"success"`
	Channel  string `json:"channel"`
	TS       string `json:"ts"`
	Reaction string `json:"reaction"`
}

// ReactionList is the compact JSON response for reactions.get.
type ReactionList struct {
	Channel string  `json:"channel"`
	Type    string  `json:"type,omitempty"`
	Message Message `json:"message"`
}

// HistoryOptions configures a conversations.history request.
type HistoryOptions struct {
	Channel string
	Limit   int
	Oldest  string
	Latest  string
}

// ThreadOptions configures a conversations.replies request.
type ThreadOptions struct {
	Channel  string
	ThreadTS string
	Limit    int
	Oldest   string
	Latest   string
}

// ReactionOptions configures reactions.add and reactions.remove.
type ReactionOptions struct {
	Channel   string
	Timestamp string
	Name      string
}

// ReactionGetOptions configures reactions.get.
type ReactionGetOptions struct {
	Channel   string
	Timestamp string
	Full      bool
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

// SendMessage posts a Slack message and returns Slack's channel/timestamp.
func (c *Client) SendMessage(ctx context.Context, channel, threadTS, text string) (SendResult, error) {
	return c.postMessageResult(ctx, channel, threadTS, text)
}

// History calls conversations.history.
func (c *Client) History(ctx context.Context, opts HistoryOptions) (MessageList, error) {
	channel := strings.TrimSpace(opts.Channel)
	if channel == "" {
		return MessageList{}, fmt.Errorf("slack channel is required")
	}

	payload := map[string]interface{}{
		"channel": channel,
	}
	if opts.Limit > 0 {
		payload["limit"] = opts.Limit
	}
	if strings.TrimSpace(opts.Oldest) != "" {
		payload["oldest"] = strings.TrimSpace(opts.Oldest)
	}
	if strings.TrimSpace(opts.Latest) != "" {
		payload["latest"] = strings.TrimSpace(opts.Latest)
	}

	var response struct {
		slackResponse
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"has_more"`
	}
	if err := c.call(ctx, "conversations.history", payload, &response); err != nil {
		return MessageList{}, err
	}
	if !response.OK {
		return MessageList{}, fmt.Errorf("slack conversations.history failed: %s", response.Error)
	}

	return MessageList{
		Messages: response.Messages,
		Count:    len(response.Messages),
		Channel:  channel,
		HasMore:  response.HasMore,
	}, nil
}

// Thread calls conversations.replies.
func (c *Client) Thread(ctx context.Context, opts ThreadOptions) (MessageList, error) {
	channel := strings.TrimSpace(opts.Channel)
	threadTS := strings.TrimSpace(opts.ThreadTS)
	if channel == "" {
		return MessageList{}, fmt.Errorf("slack channel is required")
	}
	if threadTS == "" {
		return MessageList{}, fmt.Errorf("slack thread timestamp is required")
	}

	payload := map[string]interface{}{
		"channel": channel,
		"ts":      threadTS,
	}
	if opts.Limit > 0 {
		payload["limit"] = opts.Limit
	}
	if strings.TrimSpace(opts.Oldest) != "" {
		payload["oldest"] = strings.TrimSpace(opts.Oldest)
	}
	if strings.TrimSpace(opts.Latest) != "" {
		payload["latest"] = strings.TrimSpace(opts.Latest)
	}

	var response struct {
		slackResponse
		Messages []Message `json:"messages"`
		HasMore  bool      `json:"has_more"`
	}
	if err := c.call(ctx, "conversations.replies", payload, &response); err != nil {
		return MessageList{}, err
	}
	if !response.OK {
		return MessageList{}, fmt.Errorf("slack conversations.replies failed: %s", response.Error)
	}

	return MessageList{
		Messages: response.Messages,
		Count:    len(response.Messages),
		Channel:  channel,
		ThreadTS: threadTS,
		HasMore:  response.HasMore,
	}, nil
}

// AddReaction calls reactions.add for a Slack message.
func (c *Client) AddReaction(ctx context.Context, opts ReactionOptions) (ReactionResult, error) {
	return c.callReactionMutation(ctx, "reactions.add", opts)
}

// RemoveReaction calls reactions.remove for a Slack message.
func (c *Client) RemoveReaction(ctx context.Context, opts ReactionOptions) (ReactionResult, error) {
	return c.callReactionMutation(ctx, "reactions.remove", opts)
}

// GetReactions calls reactions.get for a Slack message.
func (c *Client) GetReactions(ctx context.Context, opts ReactionGetOptions) (ReactionList, error) {
	channel := strings.TrimSpace(opts.Channel)
	timestamp := strings.TrimSpace(opts.Timestamp)
	if channel == "" {
		return ReactionList{}, fmt.Errorf("slack channel is required")
	}
	if timestamp == "" {
		return ReactionList{}, fmt.Errorf("slack timestamp is required")
	}

	payload := map[string]interface{}{
		"channel":   channel,
		"timestamp": timestamp,
	}
	if opts.Full {
		payload["full"] = true
	}

	var response struct {
		slackResponse
		Type    string  `json:"type"`
		Channel string  `json:"channel"`
		Message Message `json:"message"`
	}
	if err := c.call(ctx, "reactions.get", payload, &response); err != nil {
		return ReactionList{}, err
	}
	if !response.OK {
		return ReactionList{}, fmt.Errorf("slack reactions.get failed: %s", response.Error)
	}

	return ReactionList{
		Channel: response.Channel,
		Type:    response.Type,
		Message: response.Message,
	}, nil
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
	_, err := c.postMessageResult(ctx, channel, threadTS, text)
	return err
}

func (c *Client) postMessageResult(ctx context.Context, channel, threadTS, text string) (SendResult, error) {
	channel = strings.TrimSpace(channel)
	text = strings.TrimSpace(text)
	if channel == "" {
		return SendResult{}, fmt.Errorf("slack channel is required")
	}
	if text == "" {
		return SendResult{}, fmt.Errorf("slack message text is required")
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

	var response struct {
		slackResponse
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Message struct {
			ThreadTS string `json:"thread_ts"`
		} `json:"message"`
	}
	if err := c.call(ctx, "chat.postMessage", payload, &response); err != nil {
		return SendResult{}, err
	}
	if !response.OK {
		return SendResult{}, fmt.Errorf("slack chat.postMessage failed: %s", response.Error)
	}
	resultThreadTS := strings.TrimSpace(threadTS)
	if resultThreadTS == "" {
		resultThreadTS = response.Message.ThreadTS
	}
	return SendResult{
		Success:  true,
		Channel:  response.Channel,
		TS:       response.TS,
		ThreadTS: resultThreadTS,
	}, nil
}

func (c *Client) callReactionMutation(ctx context.Context, method string, opts ReactionOptions) (ReactionResult, error) {
	channel := strings.TrimSpace(opts.Channel)
	timestamp := strings.TrimSpace(opts.Timestamp)
	name := normalizeReactionName(opts.Name)
	if channel == "" {
		return ReactionResult{}, fmt.Errorf("slack channel is required")
	}
	if timestamp == "" {
		return ReactionResult{}, fmt.Errorf("slack timestamp is required")
	}
	if name == "" {
		return ReactionResult{}, fmt.Errorf("slack reaction name is required")
	}

	payload := map[string]interface{}{
		"channel":   channel,
		"timestamp": timestamp,
		"name":      name,
	}

	var response slackResponse
	if err := c.call(ctx, method, payload, &response); err != nil {
		return ReactionResult{}, err
	}
	if !response.OK {
		return ReactionResult{}, fmt.Errorf("slack %s failed: %s", method, response.Error)
	}

	return ReactionResult{
		Success:  true,
		Channel:  channel,
		TS:       timestamp,
		Reaction: name,
	}, nil
}

func normalizeReactionName(name string) string {
	return strings.Trim(strings.TrimSpace(name), ":")
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
