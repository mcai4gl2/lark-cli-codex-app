package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/desktop"
	"github.com/yjwong/lark-cli/internal/inbound"
	"github.com/yjwong/lark-cli/internal/platform"
	"github.com/yjwong/lark-cli/internal/slackmemory"
)

// AgentConfig aliases the shared Codex agent config for Slack gateway callers.
type AgentConfig = agent.Config

// Config configures the Slack Socket Mode gateway.
type Config struct {
	AppToken                      string
	BotToken                      string
	BotUserID                     string
	EventLogPath                  string
	AutoReplyText                 string
	Agent                         AgentConfig
	DesktopWorker                 bool
	DesktopQueueRoot              string
	MemoryEnabled                 bool
	MemoryRoot                    string
	MemoryMaxSectionChars         int
	MemoryIncludeThreadTranscript bool
	MemoryMaxTranscriptChars      int
	MemoryMaxTranscriptRecords    int
	RecoverMode                   RecoverMode
	RecoveryPollInterval          time.Duration
	ProcessingReactionName        string
	Messenger                     platform.Messenger
	APIBaseURL                    string
}

// Gateway receives Slack Socket Mode events and routes them through shared code.
type Gateway struct {
	cfg         Config
	logger      *log.Logger
	client      *Client
	messenger   platform.Messenger
	handler     *inbound.Handler
	agent       *agent.Runner
	desktop     *desktop.Queue
	worker      *desktop.Worker
	memory      *slackmemory.Store
	recoverMode RecoverMode
	recovery    *RecoveryStore
	catchUpMu   sync.Mutex
}

const (
	slackSocketReconnectInitialDelay = time.Second
	slackSocketReconnectMaxDelay     = 30 * time.Second
	slackRecoveryPollInterval        = time.Minute
	slackRecoveryThreadTTL           = 24 * time.Hour
	slackThreadCatchUpLimit          = 15
	slackThreadClosedReaction        = "white_check_mark"
)

// NewGateway returns a Slack gateway service.
func NewGateway(cfg Config) *Gateway {
	logger := log.New(os.Stderr, "slack-gateway: ", log.LstdFlags)
	client := NewClient(ClientConfig{BotToken: cfg.BotToken, BaseURL: cfg.APIBaseURL})
	recoverMode := NormalizeRecoverMode(string(cfg.RecoverMode))
	messenger := cfg.Messenger
	if messenger == nil {
		messenger = client
	}
	queueRoot := strings.TrimSpace(cfg.DesktopQueueRoot)
	if queueRoot == "" {
		queueRoot = ".slack/desktop-tasks"
	}
	var memoryStore *slackmemory.Store
	if cfg.MemoryEnabled && strings.TrimSpace(cfg.MemoryRoot) != "" {
		memoryStore = slackmemory.NewStore(slackmemory.Config{Root: cfg.MemoryRoot})
		cfg.Agent.ContextProvider = memoryPromptProvider{
			store:                   memoryStore,
			maxSectionChars:         cfg.MemoryMaxSectionChars,
			includeThreadTranscript: cfg.MemoryIncludeThreadTranscript,
			maxTranscriptChars:      cfg.MemoryMaxTranscriptChars,
			maxTranscriptRecords:    cfg.MemoryMaxTranscriptRecords,
		}
		cfg.Agent.ReplyObserver = memoryReplyObserver{store: memoryStore}
	}
	var recovery *RecoveryStore
	if recoverMode != RecoverModeOff && strings.TrimSpace(cfg.MemoryRoot) != "" {
		recovery = NewRecoveryStore(filepath.Join(cfg.MemoryRoot, ".state", "recover-state.json"))
	}
	if cfg.Agent.Enabled && strings.TrimSpace(cfg.ProcessingReactionName) != "" {
		cfg.Agent.ProcessingObserver = processingReactionObserver{client: client, reactionName: cfg.ProcessingReactionName}
	}
	queue := desktop.NewQueueWithMessenger(queueRoot, messenger)
	return &Gateway{
		cfg:       cfg,
		logger:    logger,
		client:    client,
		messenger: messenger,
		handler: inbound.NewHandler(inbound.Config{
			EventLogPath:  cfg.EventLogPath,
			AutoReplyText: cfg.AutoReplyText,
			Messenger:     messenger,
		}, logger),
		agent:       agent.NewRunnerWithMessenger(cfg.Agent, logger, messenger),
		desktop:     queue,
		worker:      desktop.NewWorker(queue, logger, desktop.WorkerConfig{}),
		memory:      memoryStore,
		recoverMode: recoverMode,
		recovery:    recovery,
	}
}

// Serve starts the Slack Socket Mode gateway.
func (g *Gateway) Serve(ctx context.Context) error {
	if strings.TrimSpace(g.cfg.AppToken) == "" {
		return fmt.Errorf("SLACK_APP_TOKEN is required")
	}
	if strings.TrimSpace(g.cfg.BotToken) == "" && g.cfg.Messenger == nil {
		return fmt.Errorf("SLACK_BOT_TOKEN is required")
	}
	if strings.TrimSpace(g.cfg.EventLogPath) == "" {
		return fmt.Errorf("Slack event log path is required")
	}

	if strings.TrimSpace(g.cfg.BotUserID) == "" {
		auth, err := g.client.AuthTest(ctx)
		if err != nil {
			return err
		}
		g.cfg.BotUserID = auth.UserID
	}

	if g.cfg.DesktopWorker {
		go func() {
			if err := g.worker.Serve(ctx); err != nil {
				g.logger.Printf("desktop worker stopped with error: %v", err)
			}
		}()
	}

	if err := g.catchUp(ctx); err != nil {
		g.logger.Printf("Slack recovery catch-up failed: %v", err)
	}
	g.startRecoveryPoller(ctx)

	reconnectDelay := slackSocketReconnectInitialDelay
	for {
		connected, err := g.serveSocketConnection(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if connected {
				reconnectDelay = slackSocketReconnectInitialDelay
			}
			g.logger.Printf("Slack Socket Mode connection stopped: %v; reconnecting in %s", err, reconnectDelay)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(reconnectDelay):
				reconnectDelay *= 2
				if reconnectDelay > slackSocketReconnectMaxDelay {
					reconnectDelay = slackSocketReconnectMaxDelay
				}
			}
			continue
		}
		return nil
	}
}

func (g *Gateway) serveSocketConnection(ctx context.Context) (bool, error) {
	wsURL, err := g.openSocketConnection(ctx)
	if err != nil {
		return false, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return false, fmt.Errorf("connect Slack Socket Mode: %w", err)
	}
	defer conn.Close()
	g.logger.Printf("connected to Slack Socket Mode")
	if err := g.catchUp(ctx); err != nil {
		g.logger.Printf("Slack recovery catch-up failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		select {
		case <-ctx.Done():
			g.logger.Printf("gateway shutdown requested")
			return true, nil
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return true, fmt.Errorf("read Slack Socket Mode envelope: %w", err)
		}

		var envelope socketEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			g.logger.Printf("invalid Slack Socket Mode envelope: %v", err)
			continue
		}
		if envelope.EnvelopeID != "" {
			if err := conn.WriteJSON(map[string]string{"envelope_id": envelope.EnvelopeID}); err != nil {
				return true, fmt.Errorf("ack Slack Socket Mode envelope: %w", err)
			}
		}
		if envelope.Type != "events_api" {
			continue
		}
		if err := g.handleEvent(ctx, envelope.Payload); err != nil {
			g.logger.Printf("Slack event handling failed: %v", err)
		}
	}
}

func (g *Gateway) openSocketConnection(ctx context.Context) (string, error) {
	var response struct {
		slackResponse
		URL string `json:"url"`
	}
	if err := g.client.callWithToken(ctx, "apps.connections.open", map[string]string{}, &response, g.cfg.AppToken); err != nil {
		return "", err
	}
	if !response.OK {
		return "", fmt.Errorf("slack apps.connections.open failed: %s", response.Error)
	}
	if strings.TrimSpace(response.URL) == "" {
		return "", fmt.Errorf("slack apps.connections.open returned empty url")
	}
	return response.URL, nil
}

func (g *Gateway) handleEvent(ctx context.Context, payload json.RawMessage) error {
	entry, ok, err := NormalizeEvent(payload, g.cfg.BotUserID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return g.processEntry(ctx, entry)
}

func (g *Gateway) processEntry(ctx context.Context, entry platform.MessageEvent) error {
	_ = ctx
	if err := g.handler.Process(entry); err != nil {
		return err
	}
	if g.memory != nil {
		if err := g.memory.RecordInbound(entry); err != nil {
			return err
		}
	}
	if g.recovery != nil {
		key := recoveryThreadKey(entry)
		if err := g.recovery.MarkParticipating(key); err != nil {
			return err
		}
	}

	if request, ok := desktop.ExtractRequest(entry.MessageText); ok {
		task, err := g.desktop.Enqueue(entry, request)
		if err != nil {
			return err
		}
		ack := fmt.Sprintf("Desktop GUI task queued: %s. I will reply here when it finishes.", task.ID)
		if err := g.desktop.Reply(task, ack); err != nil {
			g.logger.Printf("failed to acknowledge desktop task %s: %v", task.ID, err)
		}
		return g.markEntryProcessed(entry)
	}

	g.agent.Dispatch(entry)
	return g.markEntryProcessed(entry)
}

func (g *Gateway) markEntryProcessed(entry platform.MessageEvent) error {
	if g.recovery == nil {
		return nil
	}
	return g.recovery.MarkProcessed(recoveryThreadKey(entry), entry.MessageID)
}

func (g *Gateway) catchUp(ctx context.Context) error {
	if g.recovery == nil || !g.recovery.Enabled() || g.recoverMode == RecoverModeOff {
		return nil
	}
	g.catchUpMu.Lock()
	defer g.catchUpMu.Unlock()

	threads, err := g.recovery.Threads()
	if err != nil {
		return err
	}
	for _, thread := range threads {
		if err := g.catchUpThread(ctx, thread); err != nil {
			g.logger.Printf(
				"Slack recovery catch-up failed for team=%s channel=%s thread=%s: %v",
				thread.Key.TeamID,
				thread.Key.ChannelID,
				thread.Key.ThreadTS,
				err,
			)
		}
	}
	return nil
}

func (g *Gateway) catchUpThread(ctx context.Context, thread RecoveryThreadRecord) error {
	key := thread.Key
	if recoveryThreadExpired(thread, time.Now(), slackRecoveryThreadTTL) {
		removed, err := g.recovery.RemoveThread(key)
		if err != nil {
			return err
		}
		if removed {
			g.logger.Printf("thread %s last seen at %s older than %s, stop tracking", key.ThreadTS, thread.LastSeenAt, slackRecoveryThreadTTL)
		}
		return nil
	}

	closed, tickUser, tickedAt, err := g.threadClosedByReaction(ctx, key)
	if err != nil {
		g.logger.Printf(
			"Slack recovery close reaction check failed for team=%s channel=%s thread=%s: %v",
			key.TeamID,
			key.ChannelID,
			key.ThreadTS,
			err,
		)
	} else if closed {
		removed, err := g.recovery.RemoveThread(key)
		if err != nil {
			return err
		}
		if removed {
			g.logger.Printf("thread %s, user %s ticked at %s, stop tracking", key.ThreadTS, tickUser, tickedAt)
		}
		return nil
	}

	messages, err := g.client.Thread(ctx, ThreadOptions{
		Channel:  key.ChannelID,
		ThreadTS: key.ThreadTS,
		Limit:    slackThreadCatchUpLimit,
		Oldest:   thread.LastProcessedTS,
	})
	if err != nil {
		return err
	}

	ordered := append([]Message(nil), messages.Messages...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].TS == ordered[j].TS {
			return false
		}
		return !slackTSAfter(ordered[i].TS, ordered[j].TS)
	})

	for _, message := range ordered {
		if !g.recovery.ShouldProcess(key, message.TS) {
			continue
		}

		entry, ok := NormalizeThreadMessageForCatchUp(
			key.TeamID,
			key.ChannelID,
			inferChannelType(key.ChannelID),
			key.ThreadTS,
			message,
			g.cfg.BotUserID,
			g.recoverMode,
		)
		if !ok {
			if err := g.recovery.MarkProcessed(key, message.TS); err != nil {
				return err
			}
			continue
		}
		if err := g.processEntry(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) threadClosedByReaction(ctx context.Context, key RecoveryThreadKey) (bool, string, string, error) {
	if g.client == nil || strings.TrimSpace(key.ChannelID) == "" || strings.TrimSpace(key.ThreadTS) == "" {
		return false, "", "", nil
	}
	reactions, err := g.client.GetReactions(ctx, ReactionGetOptions{
		Channel:   key.ChannelID,
		Timestamp: key.ThreadTS,
		Full:      true,
	})
	if err != nil {
		return false, "", "", err
	}
	for _, reaction := range reactions.Message.Reactions {
		if strings.Trim(reaction.Name, ":") != slackThreadClosedReaction {
			continue
		}
		user := ""
		if len(reaction.Users) > 0 {
			user = reaction.Users[0]
		}
		return true, user, time.Now().Format(time.RFC3339Nano), nil
	}
	return false, "", "", nil
}

func recoveryThreadExpired(thread RecoveryThreadRecord, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 || strings.TrimSpace(thread.LastSeenAt) == "" {
		return false
	}
	lastSeenAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(thread.LastSeenAt))
	if err != nil {
		return false
	}
	return now.Sub(lastSeenAt) > ttl
}

func (g *Gateway) startRecoveryPoller(ctx context.Context) {
	if g.recovery == nil || !g.recovery.Enabled() || g.recoverMode == RecoverModeOff {
		return
	}
	interval := g.cfg.RecoveryPollInterval
	if interval <= 0 {
		interval = slackRecoveryPollInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := g.catchUp(ctx); err != nil {
					g.logger.Printf("Slack recovery poll failed: %v", err)
				}
			}
		}
	}()
}

func recoveryThreadKey(entry platform.MessageEvent) RecoveryThreadKey {
	threadTS := strings.TrimSpace(entry.ThreadID)
	if threadTS == "" {
		threadTS = strings.TrimSpace(entry.MessageID)
	}
	return RecoveryThreadKey{
		TeamID:    entry.TeamID,
		ChannelID: entry.ChannelID,
		ThreadTS:  threadTS,
	}
}

type socketEnvelope struct {
	EnvelopeID string          `json:"envelope_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
}

type DefaultAgentConfigInput struct {
	Enabled        bool
	Backend        string
	Binary         string
	CodexBinary    string
	Workspace      string
	Model          string
	Args           []string
	AckText        string
	ResultMaxChars int
	TimeoutMinutes int
}

// DefaultAgentConfig builds the Slack local agent config from environment and config files.
func DefaultAgentConfig(input DefaultAgentConfigInput) agent.Config {
	if input.TimeoutMinutes <= 0 {
		input.TimeoutMinutes = 20
	}
	return agent.Config{
		Enabled:        input.Enabled,
		Backend:        input.Backend,
		Binary:         input.Binary,
		Args:           input.Args,
		CodexBinary:    input.CodexBinary,
		Workspace:      input.Workspace,
		Model:          input.Model,
		AckText:        input.AckText,
		ResultMaxChars: input.ResultMaxChars,
		Timeout:        time.Duration(input.TimeoutMinutes) * time.Minute,
	}
}

type memoryPromptProvider struct {
	store                   *slackmemory.Store
	maxSectionChars         int
	includeThreadTranscript bool
	maxTranscriptChars      int
	maxTranscriptRecords    int
}

func (p memoryPromptProvider) PromptContext(entry inbound.LoggedEvent) (string, error) {
	return slackmemory.BuildPromptContext(p.store, entry, slackmemory.ContextOptions{
		MaxSectionChars:         p.maxSectionChars,
		IncludeThreadTranscript: p.includeThreadTranscript,
		MaxTranscriptChars:      p.maxTranscriptChars,
		MaxTranscriptRecords:    p.maxTranscriptRecords,
	})
}

type memoryReplyObserver struct {
	store *slackmemory.Store
}

func (o memoryReplyObserver) ObserveReply(event platform.MessageEvent, text string) error {
	if o.store == nil {
		return nil
	}
	return o.store.RecordOutbound(event, text)
}

type processingReactionObserver struct {
	client       *Client
	reactionName string
}

func (o processingReactionObserver) ProcessingStarted(event platform.MessageEvent) error {
	if o.client == nil || strings.TrimSpace(o.reactionName) == "" || strings.TrimSpace(event.ChannelID) == "" || strings.TrimSpace(event.MessageID) == "" {
		return nil
	}
	_, err := o.client.AddReaction(context.Background(), ReactionOptions{
		Channel:   event.ChannelID,
		Timestamp: event.MessageID,
		Name:      o.reactionName,
	})
	return err
}

func (o processingReactionObserver) ProcessingFinished(event platform.MessageEvent) error {
	if o.client == nil || strings.TrimSpace(o.reactionName) == "" || strings.TrimSpace(event.ChannelID) == "" || strings.TrimSpace(event.MessageID) == "" {
		return nil
	}
	_, err := o.client.RemoveReaction(context.Background(), ReactionOptions{
		Channel:   event.ChannelID,
		Timestamp: event.MessageID,
		Name:      o.reactionName,
	})
	return err
}
