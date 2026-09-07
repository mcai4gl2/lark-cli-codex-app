package weixin

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// qrScript replays a fixed sequence of get_qrcode_status responses and records
// what each poll carried.
type qrScript struct {
	mu         sync.Mutex
	statuses   []QRStatusResp
	index      int
	qrRequests int
	polls      []pollRecord
}

type pollRecord struct {
	host       string
	qrcode     string
	verifyCode string
}

func (s *qrScript) next() QRStatusResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= len(s.statuses) {
		// Past the script, hold in `wait` so the deadline decides the outcome.
		return QRStatusResp{Status: QRStatusWait}
	}
	status := s.statuses[s.index]
	s.index++
	return status
}

func (s *qrScript) record(r pollRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls = append(s.polls, r)
}

func (s *qrScript) recordQRRequest() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qrRequests++
	return s.qrRequests
}

func (s *qrScript) snapshot() []pollRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pollRecord(nil), s.polls...)
}

// qrMux serves both QR endpoints from a script.
func qrMux(t *testing.T, script *qrScript) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointGetQRCode, func(w http.ResponseWriter, r *http.Request) {
		n := script.recordQRRequest()
		writeJSON(t, w, QRCodeResp{
			QRCode:           "qr-" + string(rune('0'+n)),
			QRCodeImgContent: "https://example.test/qr",
		})
	})
	mux.HandleFunc("/"+endpointQRCodeStatus, func(w http.ResponseWriter, r *http.Request) {
		script.record(pollRecord{
			host:       r.Host,
			qrcode:     r.URL.Query().Get("qrcode"),
			verifyCode: r.URL.Query().Get("verify_code"),
		})
		writeJSON(t, w, script.next())
	})
	return mux
}

// newQRServer serves both QR endpoints from a script over plain HTTP.
func newQRServer(t *testing.T, script *qrScript) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(qrMux(t, script))
	t.Cleanup(server.Close)
	return server
}

// newQRTLSServer serves the same endpoints over TLS, which the redirect test
// needs because the state machine always rewrites the host to https.
func newQRTLSServer(t *testing.T, script *qrScript) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(qrMux(t, script))
	t.Cleanup(server.Close)
	return server
}

func newLoginConfig(t *testing.T, server *httptest.Server, stdin string) (*LoginConfig, *bytes.Buffer, *Store) {
	t.Helper()
	out := &bytes.Buffer{}
	store := NewStore(t.TempDir())
	cfg := &LoginConfig{
		Client: NewClient(ClientConfig{
			BaseURL:        server.URL,
			ChannelVersion: "1.0.0",
			Client:         server.Client(),
		}),
		Store:        store,
		Timeout:      5 * time.Second,
		PollInterval: time.Millisecond,
		Stdout:       out,
		Stdin:        strings.NewReader(stdin),
		RenderQR: func(w io.Writer, url string) {
			// Keep test output free of QR block art.
			_, _ = w.Write([]byte("[qr " + url + "]\n"))
		},
		Sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() },
	}
	return cfg, out, store
}

func runLogin(t *testing.T, cfg *LoginConfig) (LoginResult, error) {
	t.Helper()
	session, err := StartQRLogin(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartQRLogin() error = %v", err)
	}
	return WaitForLogin(context.Background(), session, cfg)
}

func TestWaitForLoginConfirmedPersistsAccount(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusWait},
		{Status: QRStatusScanned},
		{
			Status:      QRStatusConfirmed,
			BotToken:    "bot-token-1",
			ILinkBotID:  "b0f5860fdecb@im.bot",
			BaseURL:     "https://idc2.example.test",
			ILinkUserID: "user-1@im.wechat",
		},
	}}
	server := newQRServer(t, script)
	cfg, out, store := newLoginConfig(t, server, "")
	cfg.CDNBaseURL = "https://cdn.example.test/c2c"

	result, err := runLogin(t, cfg)
	if err != nil {
		t.Fatalf("WaitForLogin() error = %v", err)
	}
	if !result.Connected || result.AccountID != "b0f5860fdecb-im-bot" {
		t.Fatalf("result = %+v", result)
	}
	if result.UserID != "user-1@im.wechat" || result.BaseURL != "https://idc2.example.test" {
		t.Fatalf("result = %+v", result)
	}

	account, err := store.Load("b0f5860fdecb-im-bot")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if account.Token != "bot-token-1" || account.UserID != "user-1@im.wechat" {
		t.Fatalf("persisted account = %+v", account)
	}
	if account.BaseURL != "https://idc2.example.test" || account.CDNBaseURL != "https://cdn.example.test/c2c" {
		t.Fatalf("persisted account = %+v", account)
	}
	ids, _ := store.List()
	if len(ids) != 1 || ids[0] != "b0f5860fdecb-im-bot" {
		t.Fatalf("index = %v", ids)
	}
	if !strings.Contains(out.String(), "正在验证") {
		t.Fatalf("expected the scanned notice, got %q", out.String())
	}
}

func TestWaitForLoginConfirmedDefaultsBaseURL(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusConfirmed, BotToken: "t", ILinkBotID: "x@im.bot"},
	}}
	server := newQRServer(t, script)
	cfg, _, store := newLoginConfig(t, server, "")

	result, err := runLogin(t, cfg)
	if err != nil {
		t.Fatalf("WaitForLogin() error = %v", err)
	}
	if result.BaseURL != DefaultBaseURL {
		t.Fatalf("BaseURL = %q, want the fixed default", result.BaseURL)
	}
	account, _ := store.Load("x-im-bot")
	if account.BaseURL != DefaultBaseURL {
		t.Fatalf("persisted BaseURL = %q", account.BaseURL)
	}
}

func TestWaitForLoginSubmitsVerifyCode(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusNeedVerifyCode},
		{Status: QRStatusScanned},
		{Status: QRStatusConfirmed, BotToken: "t", ILinkBotID: "x@im.bot"},
	}}
	server := newQRServer(t, script)
	cfg, out, _ := newLoginConfig(t, server, "1234\n")

	result, err := runLogin(t, cfg)
	if err != nil {
		t.Fatalf("WaitForLogin() error = %v", err)
	}
	if !result.Connected {
		t.Fatalf("result = %+v", result)
	}

	polls := script.snapshot()
	if len(polls) < 2 {
		t.Fatalf("polls = %v", polls)
	}
	if polls[0].verifyCode != "" {
		t.Fatalf("first poll carried a verify code: %v", polls[0])
	}
	if polls[1].verifyCode != "1234" {
		t.Fatalf("second poll verify_code = %q, want 1234", polls[1].verifyCode)
	}
	if !strings.Contains(out.String(), "输入手机微信显示的数字") {
		t.Fatalf("expected the verify prompt, got %q", out.String())
	}
}

func TestWaitForLoginRepromptsOnVerifyCodeMismatch(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusNeedVerifyCode},
		{Status: QRStatusNeedVerifyCode},
		{Status: QRStatusConfirmed, BotToken: "t", ILinkBotID: "x@im.bot"},
	}}
	server := newQRServer(t, script)
	cfg, out, _ := newLoginConfig(t, server, "1111\n2222\n")

	if _, err := runLogin(t, cfg); err != nil {
		t.Fatalf("WaitForLogin() error = %v", err)
	}
	if !strings.Contains(out.String(), "你输入的数字不匹配") {
		t.Fatalf("expected the mismatch prompt, got %q", out.String())
	}
	polls := script.snapshot()
	if len(polls) < 3 || polls[1].verifyCode != "1111" || polls[2].verifyCode != "2222" {
		t.Fatalf("polls = %v", polls)
	}
}

func TestWaitForLoginGivesUpAfterRepeatedVerifyCodeBlocks(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusVerifyCodeBlocked},
		{Status: QRStatusVerifyCodeBlocked},
		{Status: QRStatusVerifyCodeBlocked},
		{Status: QRStatusVerifyCodeBlocked},
	}}
	server := newQRServer(t, script)
	cfg, _, store := newLoginConfig(t, server, "")

	result, err := runLogin(t, cfg)
	if err == nil {
		t.Fatalf("WaitForLogin() error = nil, want a give-up error")
	}
	if result.Connected || result.AlreadyConnected {
		t.Fatalf("result = %+v", result)
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("nothing should be persisted, got %v", ids)
	}
}

func TestWaitForLoginGivesUpAfterRepeatedExpiry(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusExpired},
		{Status: QRStatusExpired},
		{Status: QRStatusExpired},
		{Status: QRStatusExpired},
	}}
	server := newQRServer(t, script)
	cfg, out, store := newLoginConfig(t, server, "")

	if _, err := runLogin(t, cfg); err == nil {
		t.Fatalf("WaitForLogin() error = nil, want a give-up error")
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("nothing should be persisted, got %v", ids)
	}
	// One initial code plus two refreshes before the budget is spent.
	if script.qrRequests != 3 {
		t.Fatalf("QR requests = %d, want 3", script.qrRequests)
	}
	if !strings.Contains(out.String(), "二维码已过期") {
		t.Fatalf("expected the expiry notice, got %q", out.String())
	}
}

func TestWaitForLoginFollowsIDCRedirect(t *testing.T) {
	redirectScript := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusConfirmed, BotToken: "t2", ILinkBotID: "y@im.bot"},
	}}
	redirectServer := newQRTLSServer(t, redirectScript)
	redirectHost := strings.TrimPrefix(redirectServer.URL, "https://")

	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusScannedButRedirect, RedirectHost: redirectHost},
	}}
	server := newQRTLSServer(t, script)

	// One client trusting both test certificates, so the state machine's
	// "https://" + redirect_host rewrite reaches the second server for real.
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	pool.AddCert(redirectServer.Certificate())
	httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}

	cfg, _, store := newLoginConfig(t, server, "")
	cfg.Client = NewClient(ClientConfig{BaseURL: server.URL, Client: httpClient})

	result, err := runLogin(t, cfg)
	if err != nil {
		t.Fatalf("WaitForLogin() error = %v", err)
	}
	if !result.Connected || result.AccountID != "y-im-bot" {
		t.Fatalf("result = %+v", result)
	}
	if len(redirectScript.snapshot()) == 0 {
		t.Fatalf("the redirect host was never polled")
	}
	if _, err := store.Load("y-im-bot"); err != nil {
		t.Fatalf("account not persisted: %v", err)
	}
}

func TestWaitForLoginBindedRedirectIsSuccessAndKeepsCredentials(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{{Status: QRStatusBindedRedirect}}}
	server := newQRServer(t, script)
	cfg, _, store := newLoginConfig(t, server, "")
	if err := store.Save("existing-im-bot", Account{Token: "keep-me", UserID: "u1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	result, err := runLogin(t, cfg)
	if err != nil {
		t.Fatalf("WaitForLogin() error = %v, binded_redirect must not look like a failure", err)
	}
	if !result.AlreadyConnected || result.Connected {
		t.Fatalf("result = %+v", result)
	}
	account, err := store.Load("existing-im-bot")
	if err != nil || account.Token != "keep-me" {
		t.Fatalf("existing credentials were disturbed: %+v (%v)", account, err)
	}
}

func TestWaitForLoginRejectsConfirmedWithoutBotID(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{
		{Status: QRStatusConfirmed, BotToken: "t"},
	}}
	server := newQRServer(t, script)
	cfg, _, store := newLoginConfig(t, server, "")

	if _, err := runLogin(t, cfg); err == nil || !strings.Contains(err.Error(), "ilink_bot_id") {
		t.Fatalf("WaitForLogin() error = %v, want an ilink_bot_id failure", err)
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("nothing should be persisted, got %v", ids)
	}
}

func TestWaitForLoginTimesOut(t *testing.T) {
	script := &qrScript{statuses: []QRStatusResp{{Status: QRStatusWait}}}
	server := newQRServer(t, script)
	cfg, _, store := newLoginConfig(t, server, "")
	cfg.Timeout = 30 * time.Millisecond
	cfg.Now = fakeClock(30 * time.Millisecond)

	result, err := runLogin(t, cfg)
	if err == nil {
		t.Fatalf("WaitForLogin() error = nil, want a timeout")
	}
	if result.Connected {
		t.Fatalf("result = %+v", result)
	}
	if ids, _ := store.List(); len(ids) != 0 {
		t.Fatalf("nothing should be persisted, got %v", ids)
	}
}

func TestWaitForLoginTreatsTransportErrorAsWait(t *testing.T) {
	var polls int
	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointGetQRCode, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, QRCodeResp{QRCode: "qr-1", QRCodeImgContent: "https://example.test/qr"})
	})
	mux.HandleFunc("/"+endpointQRCodeStatus, func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls == 1 {
			// A gateway timeout mid-poll must not abort the login attempt.
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		}
		writeJSON(t, w, QRStatusResp{Status: QRStatusConfirmed, BotToken: "t", ILinkBotID: "z@im.bot"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cfg := &LoginConfig{
		Client:       NewClient(ClientConfig{BaseURL: server.URL, Client: server.Client()}),
		Store:        NewStore(t.TempDir()),
		Timeout:      2 * time.Second,
		PollInterval: time.Millisecond,
		Stdout:       &bytes.Buffer{},
		RenderQR:     func(w io.Writer, url string) {},
		Sleep:        func(ctx context.Context, d time.Duration) error { return ctx.Err() },
	}

	result, err := runLogin(t, cfg)
	if err != nil {
		t.Fatalf("WaitForLogin() error = %v", err)
	}
	if !result.Connected || result.AccountID != "z-im-bot" {
		t.Fatalf("result = %+v", result)
	}
	if polls < 2 {
		t.Fatalf("polls = %d, the loop should have retried after the gateway error", polls)
	}
}

func TestDisplayQRCodeAlwaysPrintsFallbackLink(t *testing.T) {
	out := &bytes.Buffer{}
	DisplayQRCode(out, "https://example.test/qr")
	rendered := out.String()
	if !strings.Contains(rendered, qrFallbackNotice) {
		t.Fatalf("missing the fallback notice: %q", rendered)
	}
	if !strings.Contains(rendered, "https://example.test/qr") {
		t.Fatalf("missing the raw link: %q", rendered)
	}

	empty := &bytes.Buffer{}
	DisplayQRCode(empty, "")
	if empty.Len() != 0 {
		t.Fatalf("an empty URL should render nothing, got %q", empty.String())
	}
}

func TestStartQRLoginSendsStoredTokens(t *testing.T) {
	var body QRCodeReq
	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointGetQRCode, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(t, w, QRCodeResp{QRCode: "qr-1", QRCodeImgContent: "u"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	store := NewStore(t.TempDir())
	if err := store.Save("a-im-bot", Account{Token: "tok-a"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	cfg := &LoginConfig{
		Client: NewClient(ClientConfig{BaseURL: server.URL, Client: server.Client()}),
		Store:  store,
		Stdout: &bytes.Buffer{},
	}
	if _, err := StartQRLogin(context.Background(), cfg); err != nil {
		t.Fatalf("StartQRLogin() error = %v", err)
	}
	if len(body.LocalTokenList) != 1 || body.LocalTokenList[0] != "tok-a" {
		t.Fatalf("local_token_list = %v", body.LocalTokenList)
	}
}

// fakeClock returns a Now function that advances past the deadline after the
// first few reads, so timeout paths run without real sleeping.
func fakeClock(step time.Duration) func() time.Time {
	base := time.Now()
	calls := 0
	return func() time.Time {
		calls++
		if calls <= 2 {
			return base
		}
		return base.Add(step * time.Duration(calls))
	}
}
