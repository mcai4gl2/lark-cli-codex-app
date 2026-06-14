package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/yjwong/lark-cli/internal/platform"
)

func TestClientReplyPostsToThread(t *testing.T) {
	var got struct {
		Channel     string `json:"channel"`
		Text        string `json:"text"`
		ThreadTS    string `json:"thread_ts"`
		UnfurlLinks bool   `json:"unfurl_links"`
		UnfurlMedia bool   `json:"unfurl_media"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if gotAuth := r.Header.Get("Authorization"); gotAuth != "Bearer xoxb-test" {
			t.Fatalf("Authorization = %q", gotAuth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C123","ts":"1.2"}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	err := client.Reply(context.Background(), platform.MessageEvent{
		Provider:  "slack",
		ChannelID: "C123",
		ThreadID:  "111.222",
		MessageID: "333.444",
	}, "hello")
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}

	if got.Channel != "C123" || got.ThreadTS != "111.222" || got.Text != "hello" {
		t.Fatalf("post body = %+v", got)
	}
	if got.UnfurlLinks || got.UnfurlMedia {
		t.Fatalf("unfurls should be disabled: %+v", got)
	}
}

func TestClientAuthTestReturnsBotUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"user_id":"U123","bot_id":"B123","team_id":"T123"}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	auth, err := client.AuthTest(context.Background())
	if err != nil {
		t.Fatalf("AuthTest() error = %v", err)
	}
	if auth.UserID != "U123" || auth.BotID != "B123" || auth.TeamID != "T123" {
		t.Fatalf("auth = %+v", auth)
	}
}

func TestClientReturnsSlackErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	err := client.Send(context.Background(), platform.MessageTarget{ChannelID: "C404"}, "hello")
	if err == nil {
		t.Fatalf("Send() error = nil")
	}
}

func TestClientHistoryCallsConversationsHistory(t *testing.T) {
	var got struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
		Oldest  string `json:"oldest"`
		Latest  string `json:"latest"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.history" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U123","text":"hello","ts":"111.222","thread_ts":"111.222"}],"has_more":false}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	messages, err := client.History(context.Background(), HistoryOptions{
		Channel: "C123",
		Limit:   25,
		Oldest:  "100.000",
		Latest:  "200.000",
	})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}

	if got.Channel != "C123" || got.Limit != 25 || got.Oldest != "100.000" || got.Latest != "200.000" {
		t.Fatalf("request = %+v", got)
	}
	if len(messages.Messages) != 1 || messages.Messages[0].Text != "hello" || messages.Count != 1 {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestClientThreadCallsConversationsReplies(t *testing.T) {
	var got struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Limit   int    `json:"limit"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U123","text":"root","ts":"111.222"},{"type":"message","user":"U456","text":"reply","ts":"111.333","thread_ts":"111.222"}],"has_more":false}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	messages, err := client.Thread(context.Background(), ThreadOptions{
		Channel:  "C123",
		ThreadTS: "111.222",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("Thread() error = %v", err)
	}

	if got.Channel != "C123" || got.TS != "111.222" || got.Limit != 10 {
		t.Fatalf("request = %+v", got)
	}
	if len(messages.Messages) != 2 || messages.Messages[1].ThreadTS != "111.222" {
		t.Fatalf("messages = %+v", messages)
	}
	if _, err := strconv.ParseFloat(messages.Messages[0].TS, 64); err != nil {
		t.Fatalf("timestamp should remain Slack ts-compatible: %v", err)
	}
}

func TestClientThreadIncludesOldestAndLatest(t *testing.T) {
	var got struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Limit   int    `json:"limit"`
		Oldest  string `json:"oldest"`
		Latest  string `json:"latest"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U123","text":"root","ts":"111.222"}],"has_more":false}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	_, err := client.Thread(context.Background(), ThreadOptions{
		Channel:  "C123",
		ThreadTS: "111.222",
		Limit:    10,
		Oldest:   "100.000",
		Latest:   "200.000",
	})
	if err != nil {
		t.Fatalf("Thread() error = %v", err)
	}

	if got.Channel != "C123" || got.TS != "111.222" || got.Limit != 10 || got.Oldest != "100.000" || got.Latest != "200.000" {
		t.Fatalf("request = %+v", got)
	}
}

func TestClientAddReactionCallsReactionsAdd(t *testing.T) {
	var got struct {
		Channel   string `json:"channel"`
		Timestamp string `json:"timestamp"`
		Name      string `json:"name"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reactions.add" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	result, err := client.AddReaction(context.Background(), ReactionOptions{
		Channel:   "C123",
		Timestamp: "111.222",
		Name:      ":thumbsup:",
	})
	if err != nil {
		t.Fatalf("AddReaction() error = %v", err)
	}

	if got.Channel != "C123" || got.Timestamp != "111.222" || got.Name != "thumbsup" {
		t.Fatalf("request = %+v", got)
	}
	if !result.Success || result.Reaction != "thumbsup" || result.TS != "111.222" {
		t.Fatalf("result = %+v", result)
	}
}

func TestClientRemoveReactionCallsReactionsRemove(t *testing.T) {
	var got struct {
		Channel   string `json:"channel"`
		Timestamp string `json:"timestamp"`
		Name      string `json:"name"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reactions.remove" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	result, err := client.RemoveReaction(context.Background(), ReactionOptions{
		Channel:   "C123",
		Timestamp: "111.222",
		Name:      "eyes",
	})
	if err != nil {
		t.Fatalf("RemoveReaction() error = %v", err)
	}

	if got.Channel != "C123" || got.Timestamp != "111.222" || got.Name != "eyes" {
		t.Fatalf("request = %+v", got)
	}
	if !result.Success || result.Reaction != "eyes" || result.TS != "111.222" {
		t.Fatalf("result = %+v", result)
	}
}

func TestClientGetReactionsCallsReactionsGet(t *testing.T) {
	var got struct {
		Channel   string `json:"channel"`
		Timestamp string `json:"timestamp"`
		Full      bool   `json:"full"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reactions.get" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"type":"message","channel":"C123","message":{"type":"message","user":"U123","text":"hello","ts":"111.222","reactions":[{"name":"eyes","users":["U456","U789"],"count":2}]}}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL})
	result, err := client.GetReactions(context.Background(), ReactionGetOptions{
		Channel:   "C123",
		Timestamp: "111.222",
		Full:      true,
	})
	if err != nil {
		t.Fatalf("GetReactions() error = %v", err)
	}

	if got.Channel != "C123" || got.Timestamp != "111.222" || !got.Full {
		t.Fatalf("request = %+v", got)
	}
	if result.Channel != "C123" || result.Message.TS != "111.222" || len(result.Message.Reactions) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result.Message.Reactions[0].Name != "eyes" || result.Message.Reactions[0].Count != 2 {
		t.Fatalf("reaction = %+v", result.Message.Reactions[0])
	}
}
