package weixin

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/desktop"
	"github.com/yjwong/lark-cli/internal/inbound"
	"github.com/yjwong/lark-cli/internal/platform"
	"github.com/yjwong/lark-cli/internal/slackmemory"
	"github.com/yjwong/lark-cli/internal/summarizer"
)

// AgentConfig aliases the shared agent config for Weixin gateway callers.
type AgentConfig = agent.Config

// Poll loop pacing, mirroring the reference plugin.
const (
	maxConsecutivePollFailures = 3
	pollBackoffDelay           = 30 * time.Second
	pollRetryDelay             = 2 * time.Second
	typingKeepAliveInterval    = 5 * time.Second
	// minPollInterval floors the gap between poll starts. A healthy server
	// holds a long-poll open for tens of seconds so this never engages, but a
	// server that answers immediately and empty would otherwise spin the loop
	// at thousands of requests per second.
	minPollInterval = time.Second
)

// AllowAllSenders is the explicit opt-out from the sender allow-list.
const AllowAllSenders = "*"

// Config configures the Weixin long-poll gateway.
type Config struct {
	AccountID  string
	Token      string
	BaseURL    string
	CDNBaseURL string
	// StateRoot holds accounts, cursors, context tokens, and inbound media.
	StateRoot      string
	AppID          string
	BotAgent       string
	ChannelVersion string
	RouteTag       string

	// LoginUserID is the user captured at QR login. It is the default
	// allow-list, and is what keeps the gateway from running the agent for
	// strangers when the operator has configured nothing.
	LoginUserID     string
	AllowFrom       []string
	LongPollTimeout time.Duration
	Typing          bool
	// RetryDelay is the pause after a single failed poll.
	// Zero uses pollRetryDelay.
	RetryDelay time.Duration
	// BackoffDelay is the longer pause once failures come in a row.
	// Zero uses pollBackoffDelay.
	BackoffDelay time.Duration
	// MinPollInterval floors the gap between poll starts.
	// Zero uses minPollInterval; a negative value disables the floor.
	MinPollInterval time.Duration

	EventLogPath  string
	AutoReplyText string
	Agent         AgentConfig

	DesktopWorker    bool
	DesktopQueueRoot string

	MemoryEnabled                 bool
	MemoryRoot                    string
	MemoryMaxSectionChars         int
	MemoryIncludeThreadTranscript bool
	MemoryMaxTranscriptChars      int
	MemoryMaxTranscriptRecords    int
	LocalSummarizer               *summarizer.Client
	LocalSummarizerMinChars       int

	// Messenger overrides the default Weixin messenger; used by tests.
	Messenger  platform.Messenger
	HTTPClient *http.Client
	// MediaDownload enables inbound CDN media download and decryption.
	MediaDownload bool
}

// Gateway long-polls Weixin for messages and routes them through shared code.
type Gateway struct {
	cfg           Config
	logger        *log.Logger
	client        *Client
	messenger     platform.Messenger
	handler       *inbound.Handler
	agent         *agent.Runner
	desktop       *desktop.Queue
	worker        *desktop.Worker
	memory        *slackmemory.Store
	sync          *SyncStore
	contextTokens *ContextTokenStore
	guard         *SessionGuard
	configCache   *ConfigCache
	media         *MediaDownloader
	allowFrom     map[string]bool
	allowAll      bool

	stateMu sync.Mutex
	state   SyncState
}

// NewGateway returns a Weixin gateway service.
func NewGateway(cfg Config) *Gateway {
	logger := log.New(os.Stderr, "weixin-gateway: ", log.LstdFlags)

	client := NewClient(ClientConfig{
		BaseURL:        cfg.BaseURL,
		Token:          cfg.Token,
		AppID:          cfg.AppID,
		ChannelVersion: cfg.ChannelVersion,
		BotAgent:       cfg.BotAgent,
		RouteTag:       cfg.RouteTag,
		Client:         cfg.HTTPClient,
	})

	store := NewStore(cfg.StateRoot)
	contextTokens := NewContextTokenStore(store.ContextTokenPath(cfg.AccountID))
	guard := NewSessionGuard()

	messenger := cfg.Messenger
	if messenger == nil {
		messenger = NewMessenger(MessengerConfig{
			Client:        client,
			ContextTokens: contextTokens,
			Guard:         guard,
			CDNBaseURL:    cfg.CDNBaseURL,
			Logger:        logger,
		})
	}

	queueRoot := strings.TrimSpace(cfg.DesktopQueueRoot)
	if queueRoot == "" {
		queueRoot = ".weixin/desktop-tasks"
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
			summarizer:              cfg.LocalSummarizer,
			summarizeMinChars:       cfg.LocalSummarizerMinChars,
		}
		cfg.Agent.ReplyObserver = memoryReplyObserver{store: memoryStore}
	}
	// SessionStore holds sticky backend pins even when session resume is off.
	if strings.TrimSpace(cfg.MemoryRoot) != "" {
		cfg.Agent.SessionStore = agent.NewSessionStore(
			filepath.Join(cfg.MemoryRoot, ".state", "sessions.json"),
		)
	}

	configCache := NewConfigCache(client, logger)
	if cfg.Agent.Enabled && cfg.Typing {
		cfg.Agent.ProcessingObserver = &typingObserver{
			client:      client,
			configCache: configCache,
			tokens:      contextTokens,
			guard:       guard,
			logger:      logger,
			running:     make(map[string]context.CancelFunc),
		}
	}

	queue := desktop.NewQueueWithMessenger(queueRoot, messenger)

	gateway := &Gateway{
		cfg:       cfg,
		logger:    logger,
		client:    client,
		messenger: messenger,
		handler: inbound.NewHandler(inbound.Config{
			EventLogPath:  cfg.EventLogPath,
			AutoReplyText: cfg.AutoReplyText,
			Messenger:     messenger,
		}, logger),
		agent:         agent.NewRunnerWithMessenger(cfg.Agent, logger, messenger),
		desktop:       queue,
		worker:        desktop.NewWorker(queue, logger, desktop.WorkerConfig{}),
		memory:        memoryStore,
		sync:          NewSyncStore(store.SyncBufPath(cfg.AccountID)),
		contextTokens: contextTokens,
		guard:         guard,
		configCache:   configCache,
	}
	if cfg.MediaDownload {
		gateway.media = NewMediaDownloader(MediaDownloaderConfig{
			CDNBaseURL: cfg.CDNBaseURL,
			Dir:        store.MediaDir(),
			Client:     cfg.HTTPClient,
			Logger:     logger,
		})
	}
	gateway.allowFrom, gateway.allowAll = buildAllowList(cfg.AllowFrom, cfg.LoginUserID)
	return gateway
}

// buildAllowList resolves the effective sender allow-list. An empty
// configuration falls back to the login user; only an explicit "*" opts out.
func buildAllowList(configured []string, loginUserID string) (map[string]bool, bool) {
	allow := make(map[string]bool)
	for _, entry := range configured {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		if trimmed == AllowAllSenders {
			return nil, true
		}
		allow[trimmed] = true
	}
	if len(allow) == 0 {
		if trimmed := strings.TrimSpace(loginUserID); trimmed != "" {
			allow[trimmed] = true
		}
	}
	return allow, false
}

// AllowList returns the effective sender allow-list and whether every sender is
// accepted. It reflects the login-user fallback, not just what was configured.
func (g *Gateway) AllowList() ([]string, bool) {
	if g.allowAll {
		return nil, true
	}
	senders := make([]string, 0, len(g.allowFrom))
	for sender := range g.allowFrom {
		senders = append(senders, sender)
	}
	sort.Strings(senders)
	return senders, false
}

// Allowed reports whether a sender may reach the agent.
func (g *Gateway) Allowed(userID string) bool {
	if g.allowAll {
		return true
	}
	return g.allowFrom[strings.TrimSpace(userID)]
}

// Serve runs the long-poll loop until the context is cancelled.
func (g *Gateway) Serve(ctx context.Context) error {
	if strings.TrimSpace(g.cfg.Token) == "" && g.cfg.Messenger == nil {
		return fmt.Errorf("a Weixin bot token is required; run `lark weixin login` first")
	}
	if strings.TrimSpace(g.cfg.AccountID) == "" {
		return fmt.Errorf("a Weixin account id is required")
	}
	if strings.TrimSpace(g.cfg.EventLogPath) == "" {
		return fmt.Errorf("Weixin event log path is required")
	}
	if !g.allowAll && len(g.allowFrom) == 0 {
		return fmt.Errorf(
			"no Weixin sender allow-list: set weixin.gateway.allow_from, or re-run `lark weixin login` so the bound user id is recorded",
		)
	}

	if restored, err := g.contextTokens.Restore(); err != nil {
		g.logger.Printf("failed to restore context tokens for account=%s: %v", g.cfg.AccountID, err)
	} else if restored > 0 {
		g.logger.Printf("restored %d context tokens for account=%s", restored, g.cfg.AccountID)
	}

	if g.cfg.DesktopWorker {
		go func() {
			if err := g.worker.Serve(ctx); err != nil {
				g.logger.Printf("desktop worker stopped with error: %v", err)
			}
		}()
	}

	if err := g.client.NotifyStart(ctx); err != nil {
		g.logger.Printf("notifystart failed (continuing): %v", err)
	}
	defer func() {
		// The run context is already cancelled by the time we get here, so the
		// shutdown notice needs a fresh one.
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := g.client.NotifyStop(stopCtx); err != nil {
			g.logger.Printf("notifystop failed: %v", err)
		}
	}()

	g.stateMu.Lock()
	g.state = g.sync.Load()
	resumed := g.state.GetUpdatesBuf != ""
	g.stateMu.Unlock()
	if resumed {
		g.logger.Printf("resuming from the stored poll cursor for account=%s", g.cfg.AccountID)
	} else {
		g.logger.Printf("no stored poll cursor for account=%s, starting fresh", g.cfg.AccountID)
	}

	return g.pollLoop(ctx)
}

func (g *Gateway) pollLoop(ctx context.Context) error {
	nextTimeout := g.cfg.LongPollTimeout
	if nextTimeout <= 0 {
		nextTimeout = DefaultLongPollTimeout
	}
	failures := 0

	for {
		if ctx.Err() != nil {
			return nil
		}
		if remaining := g.guard.Remaining(); remaining > 0 {
			g.logger.Printf("session paused for %s after a stale-token error", remaining.Round(time.Second))
			if !sleepCtx(ctx, remaining) {
				return nil
			}
			continue
		}

		startedAt := time.Now()
		resp, err := g.client.GetUpdates(ctx, g.cursor(), nextTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			kind, description := ClassifyNetworkError(err)
			failures++
			g.logger.Printf("getupdates error (%d/%d): %v type=%s description=%s",
				failures, maxConsecutivePollFailures, err, kind, description)
			if !g.backoff(ctx, &failures) {
				return nil
			}
			continue
		}

		if resp.LongPollingTimeoutMS > 0 {
			nextTimeout = time.Duration(resp.LongPollingTimeoutMS) * time.Millisecond
		}

		if resp.Ret != 0 || resp.ErrCode != 0 {
			if resp.ErrCode == StaleTokenErrCode || resp.Ret == StaleTokenErrCode {
				paused := g.guard.Pause()
				g.logger.Printf(
					"getupdates reports a stale token for account=%s; pausing all requests for %s. Re-run `lark weixin login` to re-bind.",
					g.cfg.AccountID, paused,
				)
				failures = 0
				continue
			}
			failures++
			g.logger.Printf("getupdates failed (%d/%d): ret=%d errcode=%d errmsg=%s",
				failures, maxConsecutivePollFailures, resp.Ret, resp.ErrCode, resp.ErrMsg)
			if !g.backoff(ctx, &failures) {
				return nil
			}
			continue
		}

		failures = 0
		if resp.GetUpdatesBuf != "" {
			g.saveCursor(resp.GetUpdatesBuf)
		}

		if len(resp.Msgs) == 0 {
			// Nothing to do: make sure an immediately-returning server cannot
			// turn the loop into a busy wait.
			if !g.throttleEmptyPoll(ctx, startedAt) {
				return nil
			}
			continue
		}

		for _, msg := range resp.Msgs {
			if ctx.Err() != nil {
				return nil
			}
			if g.alreadyProcessed(msg) {
				g.logger.Printf("skipping already-processed message seq=%d", msg.Seq)
				continue
			}
			g.processMessage(ctx, msg)
			g.markProcessed(msg)
		}
	}
}

// throttleEmptyPoll sleeps out the remainder of the minimum poll interval when
// a poll returned nothing faster than that floor.
func (g *Gateway) throttleEmptyPoll(ctx context.Context, startedAt time.Time) bool {
	floor := g.cfg.MinPollInterval
	if floor == 0 {
		floor = minPollInterval
	}
	if floor <= 0 {
		return ctx.Err() == nil
	}
	if remaining := floor - time.Since(startedAt); remaining > 0 {
		return sleepCtx(ctx, remaining)
	}
	return ctx.Err() == nil
}

// backoff waits after a failed poll: a short retry, escalating to a long pause
// once the failures come in a row.
func (g *Gateway) backoff(ctx context.Context, failures *int) bool {
	delay := g.cfg.RetryDelay
	if delay <= 0 {
		delay = pollRetryDelay
	}
	if *failures >= maxConsecutivePollFailures {
		longDelay := g.cfg.BackoffDelay
		if longDelay <= 0 {
			longDelay = pollBackoffDelay
		}
		g.logger.Printf("%d consecutive getupdates failures, backing off %s", maxConsecutivePollFailures, longDelay)
		delay = longDelay
		*failures = 0
	}
	return sleepCtx(ctx, delay)
}

func (g *Gateway) cursor() string {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	return g.state.GetUpdatesBuf
}

func (g *Gateway) saveCursor(buf string) {
	g.stateMu.Lock()
	if g.state.GetUpdatesBuf == buf {
		g.stateMu.Unlock()
		return
	}
	g.state.GetUpdatesBuf = buf
	state := g.state
	g.stateMu.Unlock()

	if err := g.sync.Save(state); err != nil {
		g.logger.Printf("failed to persist the poll cursor: %v", err)
	}
}

// alreadyProcessed reports whether a message was handled before the cursor was
// last written, which is what makes a crash mid-batch non-duplicating.
func (g *Gateway) alreadyProcessed(msg Message) bool {
	if msg.Seq == 0 {
		return false
	}
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	return msg.Seq <= g.state.LastSeq
}

func (g *Gateway) markProcessed(msg Message) {
	if msg.Seq == 0 {
		return
	}
	g.stateMu.Lock()
	if msg.Seq <= g.state.LastSeq {
		g.stateMu.Unlock()
		return
	}
	g.state.LastSeq = msg.Seq
	g.state.LastMessageID = msg.MessageID
	state := g.state
	g.stateMu.Unlock()

	if err := g.sync.Save(state); err != nil {
		g.logger.Printf("failed to persist the processed message marker: %v", err)
	}
}

// processMessage runs one inbound message through the shared pipeline.
func (g *Gateway) processMessage(ctx context.Context, msg Message) {
	sender := strings.TrimSpace(msg.FromUserID)
	if msg.MessageType == MessageTypeBot {
		return
	}
	if !g.Allowed(sender) {
		g.logger.Printf("dropping message from unauthorized sender user_id=%s", sender)
		return
	}

	// The context token must be recorded before any reply is attempted.
	if err := g.contextTokens.Set(sender, msg.ContextToken); err != nil {
		g.logger.Printf("failed to persist the context token for user_id=%s: %v", sender, err)
	}

	entry, ok := NormalizeMessage(msg, g.cfg.AccountID)
	if !ok {
		if HasUnsupportedVoice(msg.ItemList) {
			g.notice(ctx, sender, "暂不支持语音消息（未收到语音转文字结果），请改用文字。")
		}
		return
	}

	if g.media != nil {
		if attachments := g.media.Download(ctx, msg.ItemList); len(attachments) > 0 {
			entry.MessageText = appendAttachments(entry.MessageText, attachments)
			entry.RawContent = entry.MessageText
		}
	}

	if err := g.handler.Process(entry); err != nil {
		g.logger.Printf("failed to persist the inbound event: %v", err)
		return
	}
	if g.memory != nil {
		if err := g.memory.RecordInbound(entry); err != nil {
			g.logger.Printf("failed to record inbound memory: %v", err)
		}
	}

	handled, err := HandleCommand(ctx, entry.MessageText, CommandContext{
		Event:          entry,
		Reply:          func(ctx context.Context, text string) error { return g.messenger.Reply(ctx, entry, text) },
		SessionStore:   g.cfg.Agent.SessionStore,
		DefaultBackend: g.cfg.Agent.Backend,
		Workspace:      g.cfg.Agent.Workspace,
		AgentEnabled:   g.cfg.Agent.Enabled,
	})
	if err != nil {
		g.logger.Printf("slash command failed for message_id=%s: %v", entry.MessageID, err)
	}
	if handled {
		return
	}

	if request, ok := desktop.ExtractRequest(entry.MessageText); ok {
		task, err := g.desktop.Enqueue(entry, request)
		if err != nil {
			g.logger.Printf("failed to enqueue the desktop task: %v", err)
			g.notice(ctx, sender, "⚠️ 桌面任务入队失败："+err.Error())
			return
		}
		ack := fmt.Sprintf("桌面 GUI 任务已排队：%s，完成后会在这里回复。", task.ID)
		if err := g.desktop.Reply(task, ack); err != nil {
			g.logger.Printf("failed to acknowledge desktop task %s: %v", task.ID, err)
		}
		return
	}

	g.agent.Dispatch(entry)
}

// notice sends a user-visible warning. Failures are logged, never propagated:
// a notice is best-effort by definition.
func (g *Gateway) notice(ctx context.Context, userID, text string) {
	if strings.TrimSpace(userID) == "" || g.messenger == nil {
		return
	}
	target := platform.MessageTarget{Provider: Provider, TeamID: g.cfg.AccountID, ChannelID: userID, UserID: userID}
	if err := g.messenger.Send(ctx, target, text); err != nil {
		g.logger.Printf("failed to send a notice to user_id=%s: %v", userID, err)
	}
}

// appendAttachments lists saved inbound files under the message text so the
// agent can open them.
func appendAttachments(text string, paths []string) string {
	lines := make([]string, 0, len(paths)+1)
	if strings.TrimSpace(text) != "" {
		lines = append(lines, text)
	}
	for _, path := range paths {
		lines = append(lines, "[附件: "+path+"]")
	}
	return strings.Join(lines, "\n")
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// typingObserver drives the WeChat typing indicator around an agent run.
type typingObserver struct {
	client      *Client
	configCache *ConfigCache
	tokens      *ContextTokenStore
	guard       *SessionGuard
	logger      *log.Logger

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func (o *typingObserver) ProcessingStarted(event platform.MessageEvent) error {
	userID := strings.TrimSpace(event.ChannelID)
	if userID == "" || o.client == nil {
		return nil
	}
	if err := o.guard.AssertActive(); err != nil {
		return nil
	}

	contextToken, _ := o.tokens.Get(userID)
	ticket := o.configCache.TypingTicket(context.Background(), userID, contextToken)
	if err := o.client.SendTyping(context.Background(), userID, ticket, TypingStatusTyping); err != nil {
		return err
	}

	// WeChat clears the indicator on its own, so it has to be refreshed while
	// the agent is still working.
	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	if previous, ok := o.running[userID]; ok {
		previous()
	}
	o.running[userID] = cancel
	o.mu.Unlock()

	go func() {
		ticker := time.NewTicker(typingKeepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if o.guard.AssertActive() != nil {
					return
				}
				if err := o.client.SendTyping(ctx, userID, ticket, TypingStatusTyping); err != nil {
					return
				}
			}
		}
	}()
	return nil
}

func (o *typingObserver) ProcessingFinished(event platform.MessageEvent) error {
	userID := strings.TrimSpace(event.ChannelID)
	if userID == "" || o.client == nil {
		return nil
	}

	o.mu.Lock()
	if cancel, ok := o.running[userID]; ok {
		cancel()
		delete(o.running, userID)
	}
	o.mu.Unlock()

	if err := o.guard.AssertActive(); err != nil {
		return nil
	}
	contextToken, _ := o.tokens.Get(userID)
	ticket := o.configCache.TypingTicket(context.Background(), userID, contextToken)
	return o.client.SendTyping(context.Background(), userID, ticket, TypingStatusCancel)
}

type memoryPromptProvider struct {
	store                   *slackmemory.Store
	maxSectionChars         int
	includeThreadTranscript bool
	maxTranscriptChars      int
	maxTranscriptRecords    int
	summarizer              slackmemory.Summarizer
	summarizeMinChars       int
}

func (p memoryPromptProvider) PromptContext(entry inbound.LoggedEvent) (string, error) {
	return slackmemory.BuildPromptContext(p.store, entry, slackmemory.ContextOptions{
		MaxSectionChars:         p.maxSectionChars,
		IncludeThreadTranscript: p.includeThreadTranscript,
		MaxTranscriptChars:      p.maxTranscriptChars,
		MaxTranscriptRecords:    p.maxTranscriptRecords,
		Summarizer:              p.summarizer,
		SummarizeMinChars:       p.summarizeMinChars,
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

// DefaultAgentConfigInput mirrors the Slack gateway's agent config surface.
type DefaultAgentConfigInput struct {
	Enabled        bool
	Backend        string
	Binary         string
	CodexBinary    string
	GrokBinary     string
	Workspace      string
	Model          string
	Args           []string
	AckText        string
	ResultMaxChars int
	TimeoutMinutes int
	SessionResume  bool
}

// DefaultAgentConfig builds the Weixin local agent config from configuration.
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
		GrokBinary:     input.GrokBinary,
		Workspace:      input.Workspace,
		Model:          input.Model,
		AckText:        input.AckText,
		ResultMaxChars: input.ResultMaxChars,
		Timeout:        time.Duration(input.TimeoutMinutes) * time.Minute,
		SessionResume:  input.SessionResume,
	}
}
