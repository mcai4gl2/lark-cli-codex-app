package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
