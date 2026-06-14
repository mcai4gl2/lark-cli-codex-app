package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yjwong/lark-cli/internal/platform"
	"github.com/yjwong/lark-cli/internal/slackmemory"
)

type captureMessenger struct {
	mu      sync.Mutex
	replies []string
	events  []platform.MessageEvent
}

func (m *captureMessenger) Reply(_ context.Context, event platform.MessageEvent, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	m.replies = append(m.replies, text)
	return nil
}

func (m *captureMessenger) Send(_ context.Context, _ platform.MessageTarget, _ string) error {
	return nil
}

func writeNoReactionResponse(w http.ResponseWriter, channel, ts string) {
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"type":    "message",
		"channel": channel,
		"message": map[string]interface{}{
			"type":      "message",
			"ts":        ts,
			"reactions": []map[string]interface{}{},
		},
	})
}

func TestServiceHandleEventQueuesDesktopRequest(t *testing.T) {
	messenger := &captureMessenger{}
	service := NewGateway(Config{
		EventLogPath: t.TempDir() + "/events.jsonl",
		BotUserID:    "U999",
		Messenger:    messenger,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev123",
		"event":{
			"type":"message",
			"channel_type":"im",
			"user":"U234",
			"channel":"D345",
			"text":"/gui open https://openai.com",
			"ts":"1710000000.000200"
		}
	}`)
	if err := service.handleEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}

	if len(messenger.replies) != 1 {
		t.Fatalf("reply count = %d", len(messenger.replies))
	}
	if messenger.events[0].Provider != "slack" || messenger.events[0].ThreadID != "1710000000.000200" {
		t.Fatalf("reply event = %+v", messenger.events[0])
	}
}

func TestServiceReconnectsAfterSocketReadError(t *testing.T) {
	var connections atomic.Int32
	var opens atomic.Int32
	var upgrader websocket.Upgrader
	eventHandled := make(chan struct{})

	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer conn.Close()

		switch connections.Add(1) {
		case 1:
			return
		case 2:
			envelope := map[string]interface{}{
				"envelope_id": "EvSocket2",
				"type":        "events_api",
				"payload": map[string]interface{}{
					"team_id":  "T123",
					"event_id": "Ev123",
					"event": map[string]interface{}{
						"type":         "app_mention",
						"user":         "U234",
						"channel":      "C345",
						"text":         "<@U999> after reconnect",
						"ts":           "1710000000.000300",
						"thread_ts":    "1710000000.000100",
						"channel_type": "channel",
					},
				},
			}
			if err := conn.WriteJSON(envelope); err != nil {
				t.Errorf("WriteJSON(envelope) error = %v", err)
				return
			}
			_, _, _ = conn.ReadMessage()
			close(eventHandled)
			<-r.Context().Done()
		default:
			t.Errorf("unexpected socket connection count = %d", connections.Load())
		}
	}))
	defer socketServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apps.connections.open" {
			t.Errorf("unexpected API path = %s", r.URL.Path)
		}
		opens.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":  true,
			"url": "ws" + strings.TrimPrefix(socketServer.URL, "http"),
		})
	}))
	defer apiServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewGateway(Config{
		AppToken:     "xapp-test",
		EventLogPath: filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:    "U999",
		Messenger:    &captureMessenger{},
		APIBaseURL:   apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Serve() returned before reconnecting: %v", err)
	case <-eventHandled:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event after reconnect")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve() error after cancel = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Serve() shutdown")
	}
	if got := opens.Load(); got < 2 {
		t.Fatalf("apps.connections.open calls = %d, want at least 2", got)
	}
}

func TestServiceHandleEventWritesSlackMemory(t *testing.T) {
	memoryRoot := t.TempDir()
	service := NewGateway(Config{
		EventLogPath:  filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:     "U999",
		Messenger:     &captureMessenger{},
		MemoryEnabled: true,
		MemoryRoot:    memoryRoot,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	payload := []byte(`{
		"team_id":"T123",
		"event_id":"Ev123",
		"event":{
			"type":"app_mention",
			"user":"U234",
			"channel":"C345",
			"text":"<@U999> remember this",
			"ts":"1710000000.000300",
			"thread_ts":"1710000000.000100"
		}
	}`)
	if err := service.handleEvent(context.Background(), payload); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}

	threadLog := filepath.Join(memoryRoot, "T123", "C345", "threads", "1710000000.000100", "events.jsonl")
	data, err := os.ReadFile(threadLog)
	if err != nil {
		t.Fatalf("ReadFile(threadLog): %v", err)
	}
	record := string(data)
	if !strings.Contains(record, `"direction":"inbound"`) || !strings.Contains(record, `"text":"remember this"`) {
		t.Fatalf("thread memory record = %q", record)
	}

	dailyEntries, err := filepath.Glob(filepath.Join(memoryRoot, "T123", "C345", "daily", "*.jsonl"))
	if err != nil {
		t.Fatalf("Glob(daily): %v", err)
	}
	if len(dailyEntries) != 1 {
		t.Fatalf("daily entry count = %d, entries = %#v", len(dailyEntries), dailyEntries)
	}
}

func TestGatewayCatchUpProcessesParticipatingThreadMessages(t *testing.T) {
	memoryRoot := t.TempDir()
	eventLogPath := filepath.Join(t.TempDir(), "events.jsonl")
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	var gotRequest struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Limit   int    `json:"limit"`
		Oldest  string `json:"oldest"`
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/reactions.get":
			writeNoReactionResponse(w, "C123", "1710000000.000100")
		case "/conversations.replies":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s", r.Method)
			}
			gotRequest.Channel = r.URL.Query().Get("channel")
			gotRequest.TS = r.URL.Query().Get("ts")
			gotRequest.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
			gotRequest.Oldest = r.URL.Query().Get("oldest")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
				"messages": []map[string]string{
					{
						"type":      "message",
						"user":      "U234",
						"text":      "missed reply",
						"ts":        "1710000000.000300",
						"thread_ts": "1710000000.000100",
					},
					{
						"type":      "message",
						"user":      "U234",
						"text":      "already processed root",
						"ts":        "1710000000.000100",
						"thread_ts": "1710000000.000100",
					},
				},
			})
		default:
			t.Fatalf("unexpected API path = %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	service := NewGateway(Config{
		BotToken:      "xoxb-test",
		EventLogPath:  eventLogPath,
		BotUserID:     "U999",
		Messenger:     &captureMessenger{},
		MemoryEnabled: true,
		MemoryRoot:    memoryRoot,
		RecoverMode:   RecoverModeThread,
		APIBaseURL:    apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})
	if err := service.recovery.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := service.recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	if err := service.catchUp(context.Background()); err != nil {
		t.Fatalf("catchUp() error = %v", err)
	}

	if gotRequest.Channel != "C123" || gotRequest.TS != "1710000000.000100" || gotRequest.Limit != slackThreadCatchUpLimit || gotRequest.Oldest != "1710000000.000100" {
		t.Fatalf("conversations.replies request = %+v", gotRequest)
	}
	data, err := os.ReadFile(eventLogPath)
	if err != nil {
		t.Fatalf("ReadFile(event log): %v", err)
	}
	if !strings.Contains(string(data), `"message_id":"1710000000.000300"`) || !strings.Contains(string(data), `"message_text":"missed reply"`) {
		t.Fatalf("event log = %q", string(data))
	}
	threads, err := service.recovery.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].LastProcessedTS != "1710000000.000300" {
		t.Fatalf("recovery threads = %+v", threads)
	}
}

func TestGatewayCatchUpDoesNotAdvanceStateWhenProcessingFails(t *testing.T) {
	memoryRoot := t.TempDir()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocker): %v", err)
	}
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/reactions.get":
			writeNoReactionResponse(w, "C123", "1710000000.000100")
		case "/conversations.replies":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
				"messages": []map[string]string{
					{
						"type":      "message",
						"user":      "U234",
						"text":      "missed reply",
						"ts":        "1710000000.000300",
						"thread_ts": "1710000000.000100",
					},
				},
			})
		default:
			t.Fatalf("unexpected API path = %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	service := NewGateway(Config{
		BotToken:      "xoxb-test",
		EventLogPath:  filepath.Join(blocker, "events.jsonl"),
		BotUserID:     "U999",
		Messenger:     &captureMessenger{},
		MemoryEnabled: true,
		MemoryRoot:    memoryRoot,
		RecoverMode:   RecoverModeThread,
		APIBaseURL:    apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})
	if err := service.recovery.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := service.recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	_ = service.catchUp(context.Background())

	threads, err := service.recovery.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].LastProcessedTS != "1710000000.000100" {
		t.Fatalf("recovery threads = %+v", threads)
	}
}

func TestGatewayProcessEntryDoesNotAdvanceStateWhenDesktopEnqueueFails(t *testing.T) {
	memoryRoot := t.TempDir()
	queueBlocker := filepath.Join(t.TempDir(), "queue-blocker")
	if err := os.WriteFile(queueBlocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(queueBlocker): %v", err)
	}
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	service := NewGateway(Config{
		EventLogPath:     filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:        "U999",
		Messenger:        &captureMessenger{},
		MemoryRoot:       memoryRoot,
		RecoverMode:      RecoverModeThread,
		DesktopQueueRoot: filepath.Join(queueBlocker, "queue"),
		Agent: AgentConfig{
			Enabled: false,
		},
	})
	if err := service.recovery.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := service.recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	err := service.processEntry(context.Background(), platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C123",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000300",
		MessageText: "/gui open https://openai.com",
	})
	if err == nil {
		t.Fatal("processEntry() error = nil")
	}

	threads, err := service.recovery.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].LastProcessedTS != "1710000000.000100" {
		t.Fatalf("recovery threads = %+v", threads)
	}
}

func TestGatewayCatchUpSkipsAlreadyClaimedMessages(t *testing.T) {
	memoryRoot := t.TempDir()
	eventLogPath := filepath.Join(t.TempDir(), "events.jsonl")
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	var requests atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/reactions.get":
			writeNoReactionResponse(w, "C123", "1710000000.000100")
		case "/conversations.replies":
			requests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
				"messages": []map[string]string{
					{
						"type":      "message",
						"user":      "U234",
						"text":      "already claimed reply",
						"ts":        "1710000000.000300",
						"thread_ts": "1710000000.000100",
					},
				},
			})
		default:
			t.Fatalf("unexpected API path = %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	service := NewGateway(Config{
		BotToken:      "xoxb-test",
		EventLogPath:  eventLogPath,
		BotUserID:     "U999",
		Messenger:     &captureMessenger{},
		MemoryEnabled: true,
		MemoryRoot:    memoryRoot,
		RecoverMode:   RecoverModeThread,
		APIBaseURL:    apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})
	if err := service.recovery.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := service.recovery.MarkProcessed(key, "1710000000.000300"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	if err := service.catchUp(context.Background()); err != nil {
		t.Fatalf("catchUp() error = %v", err)
	}

	if got := requests.Load(); got != 1 {
		t.Fatalf("conversations.replies calls = %d", got)
	}
	data, err := os.ReadFile(eventLogPath)
	if err == nil && strings.Contains(string(data), "already claimed reply") {
		t.Fatalf("event log = %q", string(data))
	}
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadFile(event log): %v", err)
	}
	threads, err := service.recovery.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].LastProcessedTS != "1710000000.000300" {
		t.Fatalf("recovery threads = %+v", threads)
	}
}

func TestGatewayServeRunsCatchUpAfterSocketConnect(t *testing.T) {
	memoryRoot := t.TempDir()
	eventLogPath := filepath.Join(t.TempDir(), "events.jsonl")
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	var upgrader websocket.Upgrader

	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer conn.Close()
		<-r.Context().Done()
	}))
	defer socketServer.Close()

	recovery := NewRecoveryStore(filepath.Join(memoryRoot, ".state", "recover-state.json"))
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apps.connections.open":
			if err := recovery.MarkParticipating(key); err != nil {
				t.Errorf("MarkParticipating() error = %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
				t.Errorf("MarkProcessed() error = %v", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":  true,
				"url": "ws" + strings.TrimPrefix(socketServer.URL, "http"),
			})
		case "/reactions.get":
			writeNoReactionResponse(w, "C123", "1710000000.000100")
		case "/conversations.replies":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok": true,
				"messages": []map[string]string{
					{
						"type":      "message",
						"user":      "U234",
						"text":      "after connect catchup",
						"ts":        "1710000000.000300",
						"thread_ts": "1710000000.000100",
					},
				},
			})
		default:
			t.Errorf("unexpected API path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewGateway(Config{
		AppToken:     "xapp-test",
		BotToken:     "xoxb-test",
		EventLogPath: eventLogPath,
		BotUserID:    "U999",
		Messenger:    &captureMessenger{},
		MemoryRoot:   memoryRoot,
		RecoverMode:  RecoverModeThread,
		APIBaseURL:   apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(3 * time.Second)
	for caughtUp := false; !caughtUp; {
		select {
		case err := <-errCh:
			t.Fatalf("Serve() returned before catch-up: %v", err)
		case <-ticker.C:
			data, err := os.ReadFile(eventLogPath)
			if err == nil && strings.Contains(string(data), "after connect catchup") {
				caughtUp = true
				cancel()
			}
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("ReadFile(event log): %v", err)
			}
		case <-timeout:
			t.Fatal("timed out waiting for catch-up after socket connect")
		}
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve() error after cancel = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Serve() shutdown")
	}

	data, err := os.ReadFile(eventLogPath)
	if err != nil {
		t.Fatalf("ReadFile(event log): %v", err)
	}
	if !strings.Contains(string(data), "after connect catchup") {
		t.Fatalf("event log = %q", string(data))
	}
}

func TestGatewayServePollsCatchUpWhileConnected(t *testing.T) {
	memoryRoot := t.TempDir()
	eventLogPath := filepath.Join(t.TempDir(), "events.jsonl")
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	var repliesCalls atomic.Int32
	var upgrader websocket.Upgrader

	socketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade() error = %v", err)
			return
		}
		defer conn.Close()
		<-r.Context().Done()
	}))
	defer socketServer.Close()

	recovery := NewRecoveryStore(filepath.Join(memoryRoot, ".state", "recover-state.json"))
	if err := recovery.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apps.connections.open":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":  true,
				"url": "ws" + strings.TrimPrefix(socketServer.URL, "http"),
			})
		case "/reactions.get":
			writeNoReactionResponse(w, "C123", "1710000000.000100")
		case "/conversations.replies":
			call := repliesCalls.Add(1)
			messages := []map[string]string{
				{
					"type":      "message",
					"user":      "U234",
					"text":      "already processed root",
					"ts":        "1710000000.000100",
					"thread_ts": "1710000000.000100",
				},
			}
			if call > 1 {
				messages = append(messages, map[string]string{
					"type":      "message",
					"user":      "U234",
					"text":      "polled reply",
					"ts":        "1710000000.000300",
					"thread_ts": "1710000000.000100",
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":       true,
				"messages": messages,
			})
		default:
			t.Errorf("unexpected API path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := NewGateway(Config{
		AppToken:             "xapp-test",
		BotToken:             "xoxb-test",
		EventLogPath:         eventLogPath,
		BotUserID:            "U999",
		Messenger:            &captureMessenger{},
		MemoryRoot:           memoryRoot,
		RecoverMode:          RecoverModeThread,
		RecoveryPollInterval: 10 * time.Millisecond,
		APIBaseURL:           apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Serve(ctx)
	}()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(3 * time.Second)
	for polled := false; !polled; {
		select {
		case err := <-errCh:
			t.Fatalf("Serve() returned before polling catch-up: %v", err)
		case <-ticker.C:
			data, err := os.ReadFile(eventLogPath)
			if err == nil && strings.Contains(string(data), "polled reply") {
				polled = true
				cancel()
			}
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("ReadFile(event log): %v", err)
			}
		case <-timeout:
			t.Fatal("timed out waiting for catch-up poll")
		}
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve() error after cancel = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Serve() shutdown")
	}
}

func TestGatewayCatchUpStopsTrackingCheckmarkedThread(t *testing.T) {
	memoryRoot := t.TempDir()
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	var repliesCalls atomic.Int32
	var reactionsCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/reactions.get":
			reactionsCalls.Add(1)
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Query().Get("channel") != "C123" || r.URL.Query().Get("timestamp") != "1710000000.000100" {
				t.Fatalf("reactions.get query = %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":      true,
				"type":    "message",
				"channel": "C123",
				"message": map[string]interface{}{
					"type": "message",
					"user": "U234",
					"ts":   "1710000000.000100",
					"reactions": []map[string]interface{}{
						{
							"name":  "white_check_mark",
							"users": []string{"U234"},
							"count": 1,
						},
					},
				},
			})
		case "/conversations.replies":
			repliesCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"ok":       true,
				"messages": []map[string]string{},
			})
		default:
			t.Errorf("unexpected API path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	service := NewGateway(Config{
		BotToken:     "xoxb-test",
		EventLogPath: filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:    "U999",
		Messenger:    &captureMessenger{},
		MemoryRoot:   memoryRoot,
		RecoverMode:  RecoverModeThread,
		APIBaseURL:   apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})
	var logs bytes.Buffer
	service.logger = log.New(&logs, "", 0)

	if err := service.recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	if err := service.catchUp(context.Background()); err != nil {
		t.Fatalf("catchUp() error = %v", err)
	}

	if reactionsCalls.Load() != 1 {
		t.Fatalf("reactions.get calls = %d", reactionsCalls.Load())
	}
	if repliesCalls.Load() != 0 {
		t.Fatalf("conversations.replies calls = %d", repliesCalls.Load())
	}
	if !strings.Contains(logs.String(), "thread 1710000000.000100, user U234 ticked at ") || !strings.Contains(logs.String(), ", stop tracking") {
		t.Fatalf("logs = %q", logs.String())
	}
	threads, err := service.recovery.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("threads = %+v", threads)
	}
}

func TestGatewayCatchUpStopsTrackingStaleThread(t *testing.T) {
	memoryRoot := t.TempDir()
	key := RecoveryThreadKey{
		TeamID:    "T123",
		ChannelID: "C123",
		ThreadTS:  "1710000000.000100",
	}
	var apiCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		t.Errorf("unexpected API path = %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	service := NewGateway(Config{
		BotToken:     "xoxb-test",
		EventLogPath: filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:    "U999",
		Messenger:    &captureMessenger{},
		MemoryRoot:   memoryRoot,
		RecoverMode:  RecoverModeThread,
		APIBaseURL:   apiServer.URL,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})
	var logs bytes.Buffer
	service.logger = log.New(&logs, "", 0)

	if err := service.recovery.MarkProcessed(key, "1710000000.000100"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}
	statePath := service.recovery.Path()
	staleState := `{
  "threads": [
    {
      "key": {
        "team_id": "T123",
        "channel_id": "C123",
        "thread_ts": "1710000000.000100"
      },
      "last_processed_ts": "1710000000.000100",
      "last_seen_at": "` + time.Now().Add(-25*time.Hour).Format(time.RFC3339Nano) + `"
    }
  ]
}
`
	if err := os.WriteFile(statePath, []byte(staleState), 0o600); err != nil {
		t.Fatalf("WriteFile(state): %v", err)
	}

	if err := service.catchUp(context.Background()); err != nil {
		t.Fatalf("catchUp() error = %v", err)
	}

	if apiCalls.Load() != 0 {
		t.Fatalf("API calls = %d", apiCalls.Load())
	}
	if !strings.Contains(logs.String(), "thread 1710000000.000100 last seen at ") || !strings.Contains(logs.String(), "older than 24h0m0s, stop tracking") {
		t.Fatalf("logs = %q", logs.String())
	}
	threads, err := service.recovery.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 0 {
		t.Fatalf("threads = %+v", threads)
	}
}

func TestNewGatewayWiresSlackMemoryIntoAgentConfig(t *testing.T) {
	memoryRoot := t.TempDir()
	threadDir := filepath.Join(memoryRoot, "T123", "C345", "threads", "1710000000.000100")
	if err := os.MkdirAll(threadDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(threadDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(threadDir, "memory.md"), []byte("thread note that should be truncated"), 0o600); err != nil {
		t.Fatalf("WriteFile(thread memory): %v", err)
	}

	service := NewGateway(Config{
		EventLogPath:          filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:             "U999",
		Messenger:             &captureMessenger{},
		MemoryEnabled:         true,
		MemoryRoot:            memoryRoot,
		MemoryMaxSectionChars: 11,
		Agent: AgentConfig{
			Enabled: false,
		},
		DesktopQueueRoot: t.TempDir(),
	})

	entry := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C345",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000300",
		MessageText: "hello",
	}
	if service.cfg.Agent.ContextProvider == nil {
		t.Fatal("ContextProvider is nil")
	}
	contextText, err := service.cfg.Agent.ContextProvider.PromptContext(entry)
	if err != nil {
		t.Fatalf("PromptContext() error = %v", err)
	}
	if !strings.Contains(contextText, "thread note") || strings.Contains(contextText, "should be truncated") {
		t.Fatalf("context text = %q", contextText)
	}

	if service.cfg.Agent.ReplyObserver == nil {
		t.Fatal("ReplyObserver is nil")
	}
	if err := service.cfg.Agent.ReplyObserver.ObserveReply(entry, "done"); err != nil {
		t.Fatalf("ObserveReply() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(threadDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile(thread events): %v", err)
	}
	if !strings.Contains(string(data), `"direction":"outbound"`) || !strings.Contains(string(data), `"text":"done"`) {
		t.Fatalf("thread events = %q", string(data))
	}
}

func TestNewGatewayWiresRecentTranscriptIntoAgentContext(t *testing.T) {
	memoryRoot := t.TempDir()
	store := slackmemory.NewStore(slackmemory.Config{Root: memoryRoot})
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C345",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000100",
		UserID:      "U123",
		MessageText: "prior request",
	}
	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	service := NewGateway(Config{
		EventLogPath:                  filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:                     "U999",
		Messenger:                     &captureMessenger{},
		MemoryEnabled:                 true,
		MemoryRoot:                    memoryRoot,
		MemoryIncludeThreadTranscript: true,
		MemoryMaxTranscriptChars:      1000,
		MemoryMaxTranscriptRecords:    10,
		Agent:                         AgentConfig{Enabled: false},
		DesktopQueueRoot:              t.TempDir(),
	})

	current := event
	current.MessageID = "1710000000.000200"
	current.MessageText = "current request"
	contextText, err := service.cfg.Agent.ContextProvider.PromptContext(current)
	if err != nil {
		t.Fatalf("PromptContext() error = %v", err)
	}
	if !strings.Contains(contextText, "## Slack recent thread transcript") || !strings.Contains(contextText, "prior request") {
		t.Fatalf("context text = %q", contextText)
	}
}

func TestNewGatewayCanDisableRecentTranscriptContext(t *testing.T) {
	memoryRoot := t.TempDir()
	store := slackmemory.NewStore(slackmemory.Config{Root: memoryRoot})
	event := platform.MessageEvent{
		Provider:    "slack",
		TeamID:      "T123",
		ChannelID:   "C345",
		ThreadID:    "1710000000.000100",
		MessageID:   "1710000000.000100",
		UserID:      "U123",
		MessageText: "prior request",
	}
	if err := store.RecordInbound(event); err != nil {
		t.Fatalf("RecordInbound() error = %v", err)
	}

	service := NewGateway(Config{
		EventLogPath:                  filepath.Join(t.TempDir(), "events.jsonl"),
		BotUserID:                     "U999",
		Messenger:                     &captureMessenger{},
		MemoryEnabled:                 true,
		MemoryRoot:                    memoryRoot,
		MemoryIncludeThreadTranscript: false,
		Agent:                         AgentConfig{Enabled: false},
		DesktopQueueRoot:              t.TempDir(),
	})

	current := event
	current.MessageID = "1710000000.000200"
	current.MessageText = "current request"
	contextText, err := service.cfg.Agent.ContextProvider.PromptContext(current)
	if err != nil {
		t.Fatalf("PromptContext() error = %v", err)
	}
	if strings.Contains(contextText, "recent thread transcript") || strings.Contains(contextText, "prior request") {
		t.Fatalf("context text = %q", contextText)
	}
}

func TestProcessingReactionObserverAddsAndRemovesReaction(t *testing.T) {
	type reactionRequest struct {
		Path      string
		Channel   string `json:"channel"`
		Timestamp string `json:"timestamp"`
		Name      string `json:"name"`
	}
	var got []reactionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request reactionRequest
		request.Path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		got = append(got, request)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	observer := processingReactionObserver{
		client:       NewClient(ClientConfig{BotToken: "xoxb-test", BaseURL: server.URL}),
		reactionName: ":hourglass_flowing_sand:",
	}
	event := platform.MessageEvent{
		ChannelID: "C123",
		MessageID: "1710000000.000200",
	}

	if err := observer.ProcessingStarted(event); err != nil {
		t.Fatalf("ProcessingStarted() error = %v", err)
	}
	if err := observer.ProcessingFinished(event); err != nil {
		t.Fatalf("ProcessingFinished() error = %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("request count = %d, requests = %#v", len(got), got)
	}
	if got[0].Path != "/reactions.add" || got[1].Path != "/reactions.remove" {
		t.Fatalf("paths = %s, %s", got[0].Path, got[1].Path)
	}
	for _, request := range got {
		if request.Channel != "C123" || request.Timestamp != "1710000000.000200" || request.Name != "hourglass_flowing_sand" {
			t.Fatalf("request = %+v", request)
		}
	}
}

func TestMemoryReplyObserverNilStoreNoop(t *testing.T) {
	observer := memoryReplyObserver{}

	if err := observer.ObserveReply(platform.MessageEvent{}, "ignored"); err != nil {
		t.Fatalf("ObserveReply() error = %v", err)
	}
}
