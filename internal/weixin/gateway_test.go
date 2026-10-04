package weixin

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

// fakeServer scripts getupdates responses and records outbound sends.
type fakeServer struct {
	mu        sync.Mutex
	responses []GetUpdatesResp
	index     int
	polls     []string
	pollTimes []time.Time
	sent      []Message
	typing    []SendTypingReq
	notified  []string
	pollGate  chan struct{}
}

func newFakeServer(t *testing.T, responses []GetUpdatesResp) (*httptest.Server, *fakeServer) {
	t.Helper()
	state := &fakeServer{responses: responses, pollGate: make(chan struct{}, 64)}

	mux := http.NewServeMux()
	mux.HandleFunc("/"+endpointGetUpdates, func(w http.ResponseWriter, r *http.Request) {
		var req GetUpdatesReq
		_ = json.NewDecoder(r.Body).Decode(&req)

		state.mu.Lock()
		state.polls = append(state.polls, req.GetUpdatesBuf)
		state.pollTimes = append(state.pollTimes, time.Now())
		var resp GetUpdatesResp
		if state.index < len(state.responses) {
			resp = state.responses[state.index]
			state.index++
		} else {
			// Past the script, hold empty so the loop keeps ticking harmlessly.
			resp = GetUpdatesResp{Ret: 0}
		}
		state.mu.Unlock()

		select {
		case state.pollGate <- struct{}{}:
		default:
		}
		writeJSON(t, w, resp)
	})
	mux.HandleFunc("/"+endpointSendMessage, func(w http.ResponseWriter, r *http.Request) {
		var req SendMessageReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		state.mu.Lock()
		if req.Msg != nil {
			state.sent = append(state.sent, *req.Msg)
		}
		state.mu.Unlock()
		writeJSON(t, w, SendMessageResp{Ret: 0})
	})
	mux.HandleFunc("/"+endpointSendTyping, func(w http.ResponseWriter, r *http.Request) {
		var req SendTypingReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		state.mu.Lock()
		state.typing = append(state.typing, req)
		state.mu.Unlock()
		writeJSON(t, w, SendTypingResp{Ret: 0})
	})
	mux.HandleFunc("/"+endpointGetConfig, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, GetConfigResp{Ret: 0, TypingTicket: "ticket"})
	})
	for _, path := range []string{endpointNotifyStart, endpointNotifyStop} {
		endpoint := path
		mux.HandleFunc("/"+endpoint, func(w http.ResponseWriter, r *http.Request) {
			state.mu.Lock()
			state.notified = append(state.notified, endpoint)
			state.mu.Unlock()
			writeJSON(t, w, NotifyResp{Ret: 0})
		})
	}

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, state
}

func (f *fakeServer) sentMessages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}

func (f *fakeServer) pollCursors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.polls...)
}

func (f *fakeServer) pollGaps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	gaps := make([]time.Duration, 0, len(f.pollTimes))
	for i := 1; i < len(f.pollTimes); i++ {
		gaps = append(gaps, f.pollTimes[i].Sub(f.pollTimes[i-1]))
	}
	return gaps
}

func (f *fakeServer) notifications() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.notified...)
}

// waitForPolls blocks until the gateway has completed n polls.
func (f *fakeServer) waitForPolls(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-f.pollGate:
		case <-deadline:
			t.Fatalf("timed out waiting for poll %d of %d", i+1, n)
		}
	}
}

type gatewayHarness struct {
	gateway  *Gateway
	server   *fakeServer
	stateDir string
	eventLog string
}

func newGatewayHarness(t *testing.T, responses []GetUpdatesResp, mutate func(*Config)) *gatewayHarness {
	t.Helper()
	server, state := newFakeServer(t, responses)
	stateDir := t.TempDir()
	eventLog := filepath.Join(t.TempDir(), "events.jsonl")

	cfg := Config{
		AccountID:        "acct-im-bot",
		Token:            "tok",
		BaseURL:          server.URL,
		StateRoot:        stateDir,
		LoginUserID:      "u1",
		LongPollTimeout:  200 * time.Millisecond,
		EventLogPath:     eventLog,
		HTTPClient:       server.Client(),
		DesktopQueueRoot: filepath.Join(t.TempDir(), "desktop"),
		// The fake server answers instantly, so shrink the empty-poll floor to
		// keep tests fast. TestGatewayThrottlesEmptyPolls covers the real one.
		MinPollInterval: 5 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return &gatewayHarness{
		gateway:  NewGateway(cfg),
		server:   state,
		stateDir: stateDir,
		eventLog: eventLog,
	}
}

// serve runs the gateway until n polls have completed, then stops it.
func (h *gatewayHarness) serve(t *testing.T, polls int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.gateway.Serve(ctx) }()

	h.server.waitForPolls(t, polls)
	// Give the last batch a moment to finish dispatching before shutdown.
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Serve() did not return after cancellation")
	}
}

func (h *gatewayHarness) loggedEvents(t *testing.T) []platform.MessageEvent {
	t.Helper()
	file, err := os.Open(h.eventLog)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("open event log: %v", err)
	}
	defer file.Close()

	var events []platform.MessageEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event platform.MessageEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("parse event log line: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func inboundResponse(cursor string, msgs ...Message) GetUpdatesResp {
	return GetUpdatesResp{Ret: 0, GetUpdatesBuf: cursor, Msgs: msgs}
}

func TestGatewayPollsDispatchesAndReplies(t *testing.T) {
	msg := textMessage("u1", "hello there")
	msg.ContextToken = "ctx-1"

	h := newGatewayHarness(t, []GetUpdatesResp{inboundResponse("cursor-1", msg)}, func(cfg *Config) {
		// An auto-reply is the simplest way to prove the pipeline reaches an
		// outbound send without shelling out to a real agent binary.
		cfg.AutoReplyText = "收到：{{text}}"
	})
	h.serve(t, 2)

	events := h.loggedEvents(t)
	if len(events) != 1 {
		t.Fatalf("logged %d events, want 1", len(events))
	}
	if events[0].Provider != Provider || events[0].MessageText != "hello there" {
		t.Fatalf("event = %+v", events[0])
	}

	sent := h.server.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	if sent[0].ToUserID != "u1" || sent[0].ContextToken != "ctx-1" {
		t.Fatalf("outbound msg = %+v", sent[0])
	}
	if got := sent[0].ItemList[0].TextItem.Text; got != "收到：hello there" {
		t.Fatalf("reply text = %q", got)
	}
}

func TestGatewayPersistsCursorAndResumesFromIt(t *testing.T) {
	msg := textMessage("u1", "hi")
	h := newGatewayHarness(t, []GetUpdatesResp{inboundResponse("cursor-1", msg)}, nil)
	h.serve(t, 3)

	syncPath := NewStore(h.stateDir).SyncBufPath("acct-im-bot")
	state := NewSyncStore(syncPath).Load()
	if state.GetUpdatesBuf != "cursor-1" {
		t.Fatalf("persisted cursor = %q, want cursor-1", state.GetUpdatesBuf)
	}
	if state.LastSeq != msg.Seq {
		t.Fatalf("persisted last_seq = %d, want %d", state.LastSeq, msg.Seq)
	}

	cursors := h.server.pollCursors()
	if cursors[0] != "" {
		t.Fatalf("first poll should start with an empty cursor, got %q", cursors[0])
	}
	if cursors[1] != "cursor-1" {
		t.Fatalf("second poll cursor = %q, want cursor-1", cursors[1])
	}

	// A fresh gateway over the same state dir must resume from the stored cursor.
	server, state2 := newFakeServer(t, nil)
	resumed := NewGateway(Config{
		AccountID:        "acct-im-bot",
		Token:            "tok",
		BaseURL:          server.URL,
		StateRoot:        h.stateDir,
		LoginUserID:      "u1",
		LongPollTimeout:  200 * time.Millisecond,
		EventLogPath:     h.eventLog,
		HTTPClient:       server.Client(),
		DesktopQueueRoot: filepath.Join(t.TempDir(), "desktop"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = resumed.Serve(ctx) }()
	state2.waitForPolls(t, 1)
	cancel()

	if got := state2.pollCursors()[0]; got != "cursor-1" {
		t.Fatalf("resumed poll cursor = %q, want cursor-1", got)
	}
}

func TestGatewaySkipsAlreadyProcessedMessages(t *testing.T) {
	msg := textMessage("u1", "hi")
	msg.Seq = 5

	// The same message is delivered twice, as it would be after a crash
	// between the poll and the cursor write.
	h := newGatewayHarness(t, []GetUpdatesResp{
		inboundResponse("cursor-1", msg),
		inboundResponse("cursor-1", msg),
	}, func(cfg *Config) { cfg.AutoReplyText = "ack" })
	h.serve(t, 3)

	if events := h.loggedEvents(t); len(events) != 1 {
		t.Fatalf("logged %d events, want 1 (the replay must be dropped)", len(events))
	}
	if sent := h.server.sentMessages(); len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
}

func TestGatewayDropsUnauthorizedSenders(t *testing.T) {
	h := newGatewayHarness(t, []GetUpdatesResp{
		inboundResponse("cursor-1", textMessage("stranger", "run rm -rf /")),
	}, func(cfg *Config) {
		cfg.AllowFrom = []string{"u1"}
		cfg.AutoReplyText = "ack"
	})
	h.serve(t, 2)

	if events := h.loggedEvents(t); len(events) != 0 {
		t.Fatalf("an unauthorized message must not be persisted, got %+v", events)
	}
	if sent := h.server.sentMessages(); len(sent) != 0 {
		t.Fatalf("an unauthorized sender must get no reply, got %+v", sent)
	}
}

func TestGatewayAllowsConfiguredAndWildcardSenders(t *testing.T) {
	h := newGatewayHarness(t, nil, func(cfg *Config) { cfg.AllowFrom = []string{"u1", "u2"} })
	if !h.gateway.Allowed("u2") || h.gateway.Allowed("u3") {
		t.Fatalf("configured allow-list is not being enforced")
	}

	wildcard := newGatewayHarness(t, nil, func(cfg *Config) { cfg.AllowFrom = []string{AllowAllSenders} })
	if !wildcard.gateway.Allowed("anyone") {
		t.Fatalf("an explicit \"*\" must accept every sender")
	}

	// With nothing configured, the login user is the allow-list.
	fallback := newGatewayHarness(t, nil, nil)
	if !fallback.gateway.Allowed("u1") || fallback.gateway.Allowed("u2") {
		t.Fatalf("the login user should be the default allow-list")
	}
}

func TestGatewayRefusesToServeWithoutAnAllowList(t *testing.T) {
	h := newGatewayHarness(t, nil, func(cfg *Config) {
		cfg.LoginUserID = ""
		cfg.AllowFrom = nil
	})
	err := h.gateway.Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "allow_from") {
		t.Fatalf("Serve() error = %v, want a missing allow-list error", err)
	}
}

func TestGatewayPausesOnStaleToken(t *testing.T) {
	h := newGatewayHarness(t, []GetUpdatesResp{
		{Ret: 0, ErrCode: StaleTokenErrCode, ErrMsg: "session timeout"},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.gateway.Serve(ctx) }()
	h.server.waitForPolls(t, 1)
	// The guard is set before the loop sleeps out the cooldown.
	time.Sleep(150 * time.Millisecond)

	if !h.gateway.guard.Paused() {
		t.Fatalf("a stale-token errcode must pause the session")
	}
	if remaining := h.gateway.guard.Remaining(); remaining > StaleTokenPause || remaining <= 0 {
		t.Fatalf("pause remaining = %s, want a window under %s", remaining, StaleTokenPause)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("Serve() did not return after cancellation")
	}
}

func TestGatewayBacksOffAfterConsecutiveFailures(t *testing.T) {
	const (
		retry   = 20 * time.Millisecond
		backoff = 500 * time.Millisecond
	)
	failure := GetUpdatesResp{Ret: 1, ErrMsg: "boom"}
	h := newGatewayHarness(t, []GetUpdatesResp{failure, failure, failure, failure}, func(cfg *Config) {
		cfg.RetryDelay = retry
		cfg.BackoffDelay = backoff
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.gateway.Serve(ctx) }()
	h.server.waitForPolls(t, 4)
	cancel()
	<-done

	gaps := h.server.pollGaps()
	if len(gaps) < 3 {
		t.Fatalf("recorded %d gaps, want at least 3", len(gaps))
	}
	// The first two failures are retried quickly...
	for i := 0; i < 2; i++ {
		if gaps[i] >= backoff {
			t.Fatalf("gap %d = %s; the short retry delay should apply before the third failure", i, gaps[i])
		}
	}
	// ...and the third in a row escalates to the long backoff.
	if gaps[2] < backoff {
		t.Fatalf("gap after three consecutive failures = %s, want at least %s", gaps[2], backoff)
	}
}

func TestGatewayResetsTheFailureCountAfterASuccessfulPoll(t *testing.T) {
	const (
		retry   = 20 * time.Millisecond
		backoff = 500 * time.Millisecond
	)
	failure := GetUpdatesResp{Ret: 1, ErrMsg: "boom"}
	h := newGatewayHarness(t, []GetUpdatesResp{
		failure, failure,
		{Ret: 0, GetUpdatesBuf: "c1"},
		failure, failure,
	}, func(cfg *Config) {
		cfg.RetryDelay = retry
		cfg.BackoffDelay = backoff
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.gateway.Serve(ctx) }()
	h.server.waitForPolls(t, 5)
	cancel()
	<-done

	// Two failures, a success, then two more must never reach the long backoff:
	// the success in the middle clears the streak.
	for i, gap := range h.server.pollGaps() {
		if gap >= backoff {
			t.Fatalf("gap %d = %s; a successful poll should have reset the failure streak", i, gap)
		}
	}
}

func TestGatewayThrottlesEmptyPolls(t *testing.T) {
	// A server that answers immediately and empty must not turn the loop into a
	// busy wait against the real API.
	const floor = 120 * time.Millisecond
	h := newGatewayHarness(t, nil, func(cfg *Config) { cfg.MinPollInterval = floor })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.gateway.Serve(ctx) }()
	h.server.waitForPolls(t, 4)
	cancel()
	<-done

	for i, gap := range h.server.pollGaps() {
		if gap < floor/2 {
			t.Fatalf("gap %d = %s; empty polls should be spaced by about %s", i, gap, floor)
		}
	}
}

func TestGatewayDoesNotThrottleWhenMessagesArrive(t *testing.T) {
	// Work must be picked up promptly; the floor only applies to empty polls.
	const floor = 2 * time.Second
	msg := textMessage("u1", "hi")
	second := textMessage("u1", "again")
	second.Seq = 2
	second.MessageID = 1002

	h := newGatewayHarness(t, []GetUpdatesResp{
		inboundResponse("c1", msg),
		inboundResponse("c2", second),
	}, func(cfg *Config) { cfg.MinPollInterval = floor })

	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.gateway.Serve(ctx) }()
	// Wait for the third poll so both message-bearing batches have been fully
	// handled before the gateway is stopped.
	h.server.waitForPolls(t, 3)
	elapsed := time.Since(start)
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if elapsed >= floor {
		t.Fatalf("three polls took %s; the empty-poll floor must not delay real work", elapsed)
	}
	if events := h.loggedEvents(t); len(events) != 2 {
		t.Fatalf("logged %d events, want 2", len(events))
	}
}

func TestGatewayNotifiesStartAndStop(t *testing.T) {
	h := newGatewayHarness(t, nil, nil)
	h.serve(t, 1)

	notifications := h.server.notifications()
	if len(notifications) < 2 {
		t.Fatalf("notifications = %v, want both a start and a stop", notifications)
	}
	if notifications[0] != endpointNotifyStart {
		t.Fatalf("first notification = %q", notifications[0])
	}
	if notifications[len(notifications)-1] != endpointNotifyStop {
		t.Fatalf("last notification = %q, want the shutdown notice", notifications[len(notifications)-1])
	}
}

func TestGatewayRestoresAndPersistsContextTokens(t *testing.T) {
	stateDir := t.TempDir()
	store := NewStore(stateDir)
	if err := os.MkdirAll(store.AccountsDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(store.ContextTokenPath("acct-im-bot"), []byte(`{"u9":"ctx-restored"}`), 0o600); err != nil {
		t.Fatalf("seed context tokens: %v", err)
	}

	msg := textMessage("u1", "hi")
	msg.ContextToken = "ctx-fresh"
	server, state := newFakeServer(t, []GetUpdatesResp{inboundResponse("c1", msg)})
	gateway := NewGateway(Config{
		AccountID:        "acct-im-bot",
		Token:            "tok",
		BaseURL:          server.URL,
		StateRoot:        stateDir,
		LoginUserID:      "u1",
		LongPollTimeout:  200 * time.Millisecond,
		EventLogPath:     filepath.Join(t.TempDir(), "events.jsonl"),
		HTTPClient:       server.Client(),
		DesktopQueueRoot: filepath.Join(t.TempDir(), "desktop"),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gateway.Serve(ctx) }()
	state.waitForPolls(t, 2)
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	if token, ok := gateway.contextTokens.Get("u9"); !ok || token != "ctx-restored" {
		t.Fatalf("restored token = %q (%v)", token, ok)
	}
	if token, ok := gateway.contextTokens.Get("u1"); !ok || token != "ctx-fresh" {
		t.Fatalf("recorded token = %q (%v)", token, ok)
	}

	// The new token must be on disk for the next process.
	data, err := os.ReadFile(store.ContextTokenPath("acct-im-bot"))
	if err != nil {
		t.Fatalf("read context tokens: %v", err)
	}
	var persisted map[string]string
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("parse context tokens: %v", err)
	}
	if persisted["u1"] != "ctx-fresh" || persisted["u9"] != "ctx-restored" {
		t.Fatalf("persisted tokens = %v", persisted)
	}
}

func TestGatewayAnswersSlashCommandsWithoutTheAgent(t *testing.T) {
	h := newGatewayHarness(t, []GetUpdatesResp{
		inboundResponse("c1", textMessage("u1", "/echo hello")),
	}, nil)
	h.serve(t, 2)

	sent := h.server.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}
	if got := sent[0].ItemList[0].TextItem.Text; got != "hello" {
		t.Fatalf("echo reply = %q", got)
	}
}

func TestGatewayNoticesUnsupportedVoice(t *testing.T) {
	voice := Message{
		Seq: 1, MessageID: 1, FromUserID: "u1", MessageType: MessageTypeUser,
		ContextToken: "ctx-1",
		ItemList:     []MessageItem{{Type: ItemTypeVoice, VoiceItem: &VoiceItem{}}},
	}
	h := newGatewayHarness(t, []GetUpdatesResp{inboundResponse("c1", voice)}, nil)
	h.serve(t, 2)

	sent := h.server.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want a single notice", len(sent))
	}
	if !strings.Contains(sent[0].ItemList[0].TextItem.Text, "语音") {
		t.Fatalf("notice text = %q", sent[0].ItemList[0].TextItem.Text)
	}
}

func TestGatewayIgnoresItsOwnEchoes(t *testing.T) {
	echo := textMessage("u1", "an earlier reply")
	echo.MessageType = MessageTypeBot

	h := newGatewayHarness(t, []GetUpdatesResp{inboundResponse("c1", echo)}, func(cfg *Config) {
		cfg.AutoReplyText = "ack"
	})
	h.serve(t, 2)

	if events := h.loggedEvents(t); len(events) != 0 {
		t.Fatalf("a bot echo must not be persisted, got %+v", events)
	}
	if sent := h.server.sentMessages(); len(sent) != 0 {
		t.Fatalf("a bot echo must not be answered, got %+v", sent)
	}
}

func TestGatewayHonorsServerSuggestedPollTimeout(t *testing.T) {
	h := newGatewayHarness(t, []GetUpdatesResp{
		{Ret: 0, GetUpdatesBuf: "c1", LongPollingTimeoutMS: 250},
	}, nil)
	h.serve(t, 2)

	// The suggestion is accepted for the next poll; the assertion here is that
	// the loop keeps running rather than stalling on a bad value.
	if len(h.server.pollCursors()) < 2 {
		t.Fatalf("the loop stopped after the timeout suggestion")
	}
}
