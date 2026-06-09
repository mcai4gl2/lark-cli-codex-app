package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yjwong/lark-cli/internal/agent"
	"github.com/yjwong/lark-cli/internal/desktop"
	"github.com/yjwong/lark-cli/internal/inbound"
	"github.com/yjwong/lark-cli/internal/platform"
)

// AgentConfig aliases the shared Codex agent config for Slack gateway callers.
type AgentConfig = agent.Config

// Config configures the Slack Socket Mode gateway.
type Config struct {
	AppToken         string
	BotToken         string
	BotUserID        string
	EventLogPath     string
	AutoReplyText    string
	Agent            AgentConfig
	DesktopWorker    bool
	DesktopQueueRoot string
	Messenger        platform.Messenger
	APIBaseURL       string
}

// Gateway receives Slack Socket Mode events and routes them through shared code.
type Gateway struct {
	cfg       Config
	logger    *log.Logger
	client    *Client
	messenger platform.Messenger
	handler   *inbound.Handler
	agent     *agent.Runner
	desktop   *desktop.Queue
	worker    *desktop.Worker
}

// NewGateway returns a Slack gateway service.
func NewGateway(cfg Config) *Gateway {
	logger := log.New(os.Stderr, "slack-gateway: ", log.LstdFlags)
	client := NewClient(ClientConfig{BotToken: cfg.BotToken, BaseURL: cfg.APIBaseURL})
	messenger := cfg.Messenger
	if messenger == nil {
		messenger = client
	}
	queueRoot := strings.TrimSpace(cfg.DesktopQueueRoot)
	if queueRoot == "" {
		queueRoot = ".slack/desktop-tasks"
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
		agent:   agent.NewRunnerWithMessenger(cfg.Agent, logger, messenger),
		desktop: queue,
		worker:  desktop.NewWorker(queue, logger, desktop.WorkerConfig{}),
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

	wsURL, err := g.openSocketConnection(ctx)
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect Slack Socket Mode: %w", err)
	}
	defer conn.Close()

	for {
		select {
		case <-ctx.Done():
			g.logger.Printf("gateway shutdown requested")
			return nil
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read Slack Socket Mode envelope: %w", err)
		}

		var envelope socketEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			g.logger.Printf("invalid Slack Socket Mode envelope: %v", err)
			continue
		}
		if envelope.EnvelopeID != "" {
			if err := conn.WriteJSON(map[string]string{"envelope_id": envelope.EnvelopeID}); err != nil {
				return fmt.Errorf("ack Slack Socket Mode envelope: %w", err)
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
	_ = ctx
	entry, ok, err := NormalizeEvent(payload, g.cfg.BotUserID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := g.handler.Process(entry); err != nil {
		return err
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
		return nil
	}

	g.agent.Dispatch(entry)
	return nil
}

type socketEnvelope struct {
	EnvelopeID string          `json:"envelope_id"`
	Type       string          `json:"type"`
	Payload    json.RawMessage `json:"payload"`
}

// DefaultAgentConfig builds the Slack Codex agent config from environment and config files.
func DefaultAgentConfig(enabled bool, codexBinary, workspace, model, ackText string, resultMaxChars, timeoutMinutes int) agent.Config {
	if timeoutMinutes <= 0 {
		timeoutMinutes = 20
	}
	return agent.Config{
		Enabled:        enabled,
		CodexBinary:    codexBinary,
		Workspace:      workspace,
		Model:          model,
		AckText:        ackText,
		ResultMaxChars: resultMaxChars,
		Timeout:        time.Duration(timeoutMinutes) * time.Minute,
	}
}
