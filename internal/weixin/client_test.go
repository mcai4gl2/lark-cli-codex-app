package weixin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type capturedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// newTestClient returns a client wired to a recording httptest server.
func newTestClient(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*Client, *[]capturedRequest, func()) {
	t.Helper()
	captured := &[]capturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*captured = append(*captured, capturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Header: r.Header.Clone(),
			Body:   body,
		})
		handler(w, r)
	}))
	client := NewClient(ClientConfig{
		BaseURL:        server.URL,
		Token:          "tok-123",
		ChannelVersion: "1.2.3",
		BotAgent:       "lark-cli/1.2.3",
		RouteTag:       "route-9",
		Client:         server.Client(),
	})
	return client, captured, server.Close
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestClientCommonHeadersAndBaseInfo(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, GetUpdatesResp{Ret: 0})
	})
	defer closeServer()

	if _, err := client.GetUpdates(context.Background(), "cursor-1", time.Second); err != nil {
		t.Fatalf("GetUpdates() error = %v", err)
	}

	if len(*captured) != 1 {
		t.Fatalf("captured %d requests, want 1", len(*captured))
	}
	req := (*captured)[0]
	if req.Path != "/"+endpointGetUpdates {
		t.Fatalf("path = %q", req.Path)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := req.Header.Get("AuthorizationType"); got != "ilink_bot_token" {
		t.Fatalf("AuthorizationType = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := req.Header.Get("iLink-App-Id"); got != DefaultAppID {
		t.Fatalf("iLink-App-Id = %q", got)
	}
	// 1.2.3 -> (1<<16)|(2<<8)|3 = 66051
	if got := req.Header.Get("iLink-App-ClientVersion"); got != "66051" {
		t.Fatalf("iLink-App-ClientVersion = %q", got)
	}
	if got := req.Header.Get("SKRouteTag"); got != "route-9" {
		t.Fatalf("SKRouteTag = %q", got)
	}

	uin := req.Header.Get("X-WECHAT-UIN")
	decoded, err := base64.StdEncoding.DecodeString(uin)
	if err != nil {
		t.Fatalf("X-WECHAT-UIN %q is not base64: %v", uin, err)
	}
	value, err := strconv.ParseUint(string(decoded), 10, 32)
	if err != nil {
		t.Fatalf("X-WECHAT-UIN payload %q is not a uint32 decimal string: %v", decoded, err)
	}
	_ = value

	var body GetUpdatesReq
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if body.GetUpdatesBuf != "cursor-1" {
		t.Fatalf("get_updates_buf = %q", body.GetUpdatesBuf)
	}
	if body.BaseInfo == nil || body.BaseInfo.ChannelVersion != "1.2.3" || body.BaseInfo.BotAgent != "lark-cli/1.2.3" {
		t.Fatalf("base_info = %+v", body.BaseInfo)
	}
}

func TestBuildClientVersion(t *testing.T) {
	cases := map[string]int{
		"1.0.11":      (1 << 16) | (0 << 8) | 11,
		"1.2.3":       (1 << 16) | (2 << 8) | 3,
		"v2.5.7":      (2 << 16) | (5 << 8) | 7,
		"0.0.0":       0,
		"3":           3 << 16,
		"1.4.9-dirty": (1 << 16) | (4 << 8) | 9,
		"":            0,
	}
	for input, want := range cases {
		if got := BuildClientVersion(input); got != want {
			t.Fatalf("BuildClientVersion(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestGetUpdatesTreatsClientTimeoutAsEmptyPoll(t *testing.T) {
	client, _, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(t, w, GetUpdatesResp{Ret: 0})
	})
	defer closeServer()

	resp, err := client.GetUpdates(context.Background(), "cursor-9", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("GetUpdates() error = %v, want nil on client timeout", err)
	}
	if resp.Ret != 0 || len(resp.Msgs) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.GetUpdatesBuf != "cursor-9" {
		t.Fatalf("timeout must preserve the cursor, got %q", resp.GetUpdatesBuf)
	}
}

func TestGetUpdatesPropagatesCallerCancellation(t *testing.T) {
	client, _, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(t, w, GetUpdatesResp{Ret: 0})
	})
	defer closeServer()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := client.GetUpdates(ctx, "", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetUpdates() error = %v, want context.Canceled", err)
	}
}

func TestSendMessageReportsAPIError(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, SendMessageResp{Ret: 7, ErrMsg: "denied"})
	})
	defer closeServer()

	err := client.SendMessage(context.Background(), &Message{ToUserID: "u1"})
	if err == nil || !strings.Contains(err.Error(), "ret=7") || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if (*captured)[0].Path != "/"+endpointSendMessage {
		t.Fatalf("path = %q", (*captured)[0].Path)
	}
}

func TestSendMessageHappyPath(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, SendMessageResp{Ret: 0})
	})
	defer closeServer()

	msg := &Message{
		ToUserID:     "u1",
		ClientID:     "cid-1",
		MessageType:  MessageTypeBot,
		MessageState: MessageStateFinish,
		ContextToken: "ctx-1",
		ItemList:     []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: "hi"}}},
	}
	if err := client.SendMessage(context.Background(), msg); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}

	var body SendMessageReq
	if err := json.Unmarshal((*captured)[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Msg == nil || body.Msg.ContextToken != "ctx-1" || body.Msg.MessageType != MessageTypeBot {
		t.Fatalf("msg = %+v", body.Msg)
	}
	if body.BaseInfo == nil {
		t.Fatalf("base_info missing from sendmessage request")
	}
}

func TestGetConfigSendTypingAndNotify(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + endpointGetConfig:
			writeJSON(t, w, GetConfigResp{Ret: 0, TypingTicket: "ticket-1"})
		default:
			writeJSON(t, w, map[string]int{"ret": 0})
		}
	})
	defer closeServer()

	ctx := context.Background()
	cfg, err := client.GetConfig(ctx, "u1", "ctx-1")
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if cfg.TypingTicket != "ticket-1" {
		t.Fatalf("typing_ticket = %q", cfg.TypingTicket)
	}
	if err := client.SendTyping(ctx, "u1", "ticket-1", TypingStatusTyping); err != nil {
		t.Fatalf("SendTyping() error = %v", err)
	}
	if err := client.NotifyStart(ctx); err != nil {
		t.Fatalf("NotifyStart() error = %v", err)
	}
	if err := client.NotifyStop(ctx); err != nil {
		t.Fatalf("NotifyStop() error = %v", err)
	}

	wantPaths := []string{
		"/" + endpointGetConfig,
		"/" + endpointSendTyping,
		"/" + endpointNotifyStart,
		"/" + endpointNotifyStop,
	}
	if len(*captured) != len(wantPaths) {
		t.Fatalf("captured %d requests, want %d", len(*captured), len(wantPaths))
	}
	for i, want := range wantPaths {
		if (*captured)[i].Path != want {
			t.Fatalf("request %d path = %q, want %q", i, (*captured)[i].Path, want)
		}
	}

	var typing SendTypingReq
	if err := json.Unmarshal((*captured)[1].Body, &typing); err != nil {
		t.Fatalf("decode sendtyping body: %v", err)
	}
	if typing.Status != TypingStatusTyping || typing.TypingTicket != "ticket-1" || typing.ILinkUserID != "u1" {
		t.Fatalf("sendtyping body = %+v", typing)
	}
}

func TestGetUploadURLRejectsAPIError(t *testing.T) {
	client, _, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, GetUploadURLResp{Ret: 5, ErrMsg: "quota"})
	})
	defer closeServer()

	if _, err := client.GetUploadURL(context.Background(), GetUploadURLReq{FileKey: "k"}); err == nil ||
		!strings.Contains(err.Error(), "ret=5") {
		t.Fatalf("GetUploadURL() error = %v", err)
	}
}

func TestFetchQRCodeOmitsAuthorizationAndSendsLocalTokens(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, QRCodeResp{QRCode: "qr-1", QRCodeImgContent: "https://example.test/qr"})
	})
	defer closeServer()

	resp, err := client.FetchQRCode(context.Background(), "3", []string{"t1", "t2"})
	if err != nil {
		t.Fatalf("FetchQRCode() error = %v", err)
	}
	if resp.QRCode != "qr-1" || resp.QRCodeImgContent != "https://example.test/qr" {
		t.Fatalf("resp = %+v", resp)
	}

	req := (*captured)[0]
	if req.Path != "/"+endpointGetQRCode {
		t.Fatalf("path = %q", req.Path)
	}
	if req.Query != "bot_type=3" {
		t.Fatalf("query = %q", req.Query)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("QR login must be unauthenticated, got Authorization = %q", got)
	}
	var body QRCodeReq
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.LocalTokenList) != 2 || body.LocalTokenList[0] != "t1" {
		t.Fatalf("local_token_list = %v", body.LocalTokenList)
	}
}

func TestFetchQRCodeCapsLocalTokenList(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, QRCodeResp{QRCode: "qr-1", QRCodeImgContent: "u"})
	})
	defer closeServer()

	tokens := make([]string, 25)
	for i := range tokens {
		tokens[i] = "t" + strconv.Itoa(i)
	}
	if _, err := client.FetchQRCode(context.Background(), "", tokens); err != nil {
		t.Fatalf("FetchQRCode() error = %v", err)
	}
	var body QRCodeReq
	if err := json.Unmarshal((*captured)[0].Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.LocalTokenList) != maxLocalTokenListSize {
		t.Fatalf("local_token_list length = %d, want %d", len(body.LocalTokenList), maxLocalTokenListSize)
	}
	if (*captured)[0].Query != "bot_type="+DefaultBotType {
		t.Fatalf("query = %q", (*captured)[0].Query)
	}
}

func TestPollQRStatusSendsVerifyCode(t *testing.T) {
	client, captured, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, QRStatusResp{Status: QRStatusWait})
	})
	defer closeServer()

	if _, err := client.PollQRStatus(context.Background(), "qr 1", "4321"); err != nil {
		t.Fatalf("PollQRStatus() error = %v", err)
	}
	req := (*captured)[0]
	if req.Method != http.MethodGet {
		t.Fatalf("method = %q", req.Method)
	}
	if !strings.Contains(req.Query, "qrcode=qr+1") || !strings.Contains(req.Query, "verify_code=4321") {
		t.Fatalf("query = %q", req.Query)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("QR status poll must be unauthenticated, got %q", got)
	}
	if got := req.Header.Get("iLink-App-Id"); got != DefaultAppID {
		t.Fatalf("iLink-App-Id = %q", got)
	}
}

func TestPostJSONReportsHTTPError(t *testing.T) {
	client, _, closeServer := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream down"))
	})
	defer closeServer()

	err := client.SendMessage(context.Background(), &Message{ToUserID: "u1"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("SendMessage() error = %v", err)
	}
}

func TestWithBaseURLAndWithToken(t *testing.T) {
	client := NewClient(ClientConfig{BaseURL: "https://a.test/", Token: "t1"})
	if client.BaseURL() != "https://a.test" {
		t.Fatalf("BaseURL() = %q", client.BaseURL())
	}
	redirected := client.WithBaseURL("https://b.test")
	if redirected.BaseURL() != "https://b.test" || client.BaseURL() != "https://a.test" {
		t.Fatalf("WithBaseURL mutated the original client")
	}
	if same := client.WithBaseURL(""); same != client {
		t.Fatalf("WithBaseURL(\"\") should return the receiver")
	}
	if untokened := client.WithToken(""); untokened.token != "" || client.token != "t1" {
		t.Fatalf("WithToken mutated the original client")
	}
}

func TestClassifyNetworkError(t *testing.T) {
	if kind, _ := ClassifyNetworkError(context.DeadlineExceeded); kind != "timeout" {
		t.Fatalf("deadline kind = %q", kind)
	}
	if kind, _ := ClassifyNetworkError(errors.New("dial tcp 127.0.0.1:1: connect: connection refused")); kind != "tcp" {
		t.Fatalf("refused kind = %q", kind)
	}
	if kind, _ := ClassifyNetworkError(errors.New("something odd")); kind != "unknown" {
		t.Fatalf("unknown kind = %q", kind)
	}
	if kind, _ := ClassifyNetworkError(nil); kind != "" {
		t.Fatalf("nil kind = %q", kind)
	}
}
