package weixin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yjwong/lark-cli/internal/platform"
)

// sentMessages collects the messages a fake sendmessage endpoint received.
type sentMessages struct {
	mu   sync.Mutex
	msgs []Message
}

func (s *sentMessages) add(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
}

func (s *sentMessages) all() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.msgs...)
}

// newMessengerHarness wires a messenger to a fake sendmessage endpoint.
func newMessengerHarness(t *testing.T) (*Messenger, *sentMessages, *ContextTokenStore) {
	t.Helper()
	sent := &sentMessages{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req SendMessageReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode sendmessage body: %v", err)
		}
		if req.Msg != nil {
			sent.add(*req.Msg)
		}
		writeJSON(t, w, SendMessageResp{Ret: 0})
	}))
	t.Cleanup(server.Close)

	tokens := NewContextTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	messenger := NewMessenger(MessengerConfig{
		Client:        NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()}),
		ContextTokens: tokens,
		Guard:         NewSessionGuard(),
	})
	return messenger, sent, tokens
}

func TestMessengerReplyEchoesContextToken(t *testing.T) {
	messenger, sent, tokens := newMessengerHarness(t)
	if err := tokens.Set("u1", "ctx-1"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	event := platform.MessageEvent{Provider: Provider, ChannelID: "u1", UserID: "u1"}
	if err := messenger.Reply(context.Background(), event, "hello"); err != nil {
		t.Fatalf("Reply() error = %v", err)
	}

	msgs := sent.all()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	msg := msgs[0]
	if msg.ToUserID != "u1" || msg.ContextToken != "ctx-1" {
		t.Fatalf("msg = %+v", msg)
	}
	if msg.MessageType != MessageTypeBot || msg.MessageState != MessageStateFinish {
		t.Fatalf("msg type/state = %d/%d", msg.MessageType, msg.MessageState)
	}
	if msg.ClientID == "" {
		t.Fatalf("client_id was not generated")
	}
	if len(msg.ItemList) != 1 || msg.ItemList[0].TextItem.Text != "hello" {
		t.Fatalf("item_list = %+v", msg.ItemList)
	}
}

func TestMessengerSendsWithoutContextTokenWhenUnknown(t *testing.T) {
	messenger, sent, _ := newMessengerHarness(t)

	// A reply is still attempted: dropping it would be worse than sending
	// without the conversation reference.
	if err := messenger.SendText(context.Background(), "u-unknown", "hi"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	msgs := sent.all()
	if len(msgs) != 1 || msgs[0].ContextToken != "" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestMessengerStripsMarkdownBeforeSending(t *testing.T) {
	messenger, sent, _ := newMessengerHarness(t)

	if err := messenger.SendText(context.Background(), "u1", "# Title\n\n**bold** and `code`"); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}
	text := sent.all()[0].ItemList[0].TextItem.Text
	if strings.Contains(text, "#") || strings.Contains(text, "**") || strings.Contains(text, "`") {
		t.Fatalf("markdown survived: %q", text)
	}
	if !strings.Contains(text, "Title") || !strings.Contains(text, "bold") {
		t.Fatalf("content was lost: %q", text)
	}
}

func TestMessengerSendsEachChunkAsItsOwnMessage(t *testing.T) {
	messenger, sent, _ := newMessengerHarness(t)
	messenger.cfg.MaxChunkRunes = 20

	long := strings.Repeat("abcde fghij ", 10)
	if err := messenger.SendText(context.Background(), "u1", long); err != nil {
		t.Fatalf("SendText() error = %v", err)
	}

	msgs := sent.all()
	if len(msgs) < 2 {
		t.Fatalf("expected the reply to be split, got %d messages", len(msgs))
	}
	clientIDs := make(map[string]bool)
	for _, msg := range msgs {
		if len(msg.ItemList) != 1 {
			t.Fatalf("each request must carry exactly one item, got %d", len(msg.ItemList))
		}
		if len([]rune(msg.ItemList[0].TextItem.Text)) > 20 {
			t.Fatalf("chunk exceeds the limit: %q", msg.ItemList[0].TextItem.Text)
		}
		if clientIDs[msg.ClientID] {
			t.Fatalf("client_id %q was reused across chunks", msg.ClientID)
		}
		clientIDs[msg.ClientID] = true
	}
}

func TestMessengerRejectsEmptyRecipientAndText(t *testing.T) {
	messenger, _, _ := newMessengerHarness(t)

	if err := messenger.SendText(context.Background(), "  ", "hi"); err == nil {
		t.Fatalf("SendText() with no recipient should fail")
	}
	if err := messenger.SendText(context.Background(), "u1", "   "); err == nil {
		t.Fatalf("SendText() with no text should fail")
	}
}

func TestMessengerRefusesToSendWhilePaused(t *testing.T) {
	messenger, sent, _ := newMessengerHarness(t)
	messenger.cfg.Guard.Pause()

	err := messenger.SendText(context.Background(), "u1", "hi")
	if err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("SendText() error = %v, want a paused-session error", err)
	}
	if len(sent.all()) != 0 {
		t.Fatalf("nothing should reach the server while paused")
	}
}

func TestMessengerSendUsesTargetUserIDFallback(t *testing.T) {
	messenger, sent, _ := newMessengerHarness(t)

	target := platform.MessageTarget{Provider: Provider, UserID: "u2"}
	if err := messenger.Send(context.Background(), target, "hi"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if sent.all()[0].ToUserID != "u2" {
		t.Fatalf("ToUserID = %q", sent.all()[0].ToUserID)
	}
}

func TestExtractMediaDirectives(t *testing.T) {
	body, paths := ExtractMediaDirectives("Here is the report.\nMEDIA:/tmp/report.pdf\nThanks.")
	if body != "Here is the report.\nThanks." {
		t.Fatalf("body = %q", body)
	}
	if len(paths) != 1 || paths[0] != "/tmp/report.pdf" {
		t.Fatalf("paths = %v", paths)
	}

	// A relative path is left in the text rather than opened.
	body, paths = ExtractMediaDirectives("MEDIA:report.pdf")
	if len(paths) != 0 || !strings.Contains(body, "MEDIA:report.pdf") {
		t.Fatalf("relative path was consumed: body=%q paths=%v", body, paths)
	}

	body, paths = ExtractMediaDirectives("no directives here")
	if len(paths) != 0 || body != "no directives here" {
		t.Fatalf("body = %q paths = %v", body, paths)
	}
}

func TestNewClientIDIsUnique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := NewClientID()
		if seen[id] {
			t.Fatalf("client id %q was generated twice", id)
		}
		seen[id] = true
	}
}
