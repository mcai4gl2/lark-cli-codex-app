package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Summarizer compresses text. Implementations must be safe for concurrent use.
type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}

// Config configures the local summarizer HTTP client.
type Config struct {
	URL            string
	MaxTokens      int
	TimeoutSeconds int
}

// Client calls the local summarizer gateway's /v1/summarize endpoint.
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient returns a Client. TimeoutSeconds defaults to 30.
func NewClient(cfg Config) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Available returns true if the upstream /health endpoint responds 200.
func (c *Client) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.cfg.URL, "/")+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type summarizeRequest struct {
	Text      string `json:"text"`
	Style     string `json:"style"`
	Format    string `json:"format"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type summarizeResponse struct {
	Summary string `json:"summary"`
}

// Summarize calls the local model to compress text. Returns ("", nil) for blank input.
// Any upstream error is returned as a non-nil error so callers can fall back gracefully.
func (c *Client) Summarize(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	maxTokens := c.cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 128
	}
	payload, err := json.Marshal(summarizeRequest{
		Text:      text,
		Style:     "concise",
		Format:    "paragraph",
		MaxTokens: maxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("marshal summarize request: %w", err)
	}
	url := strings.TrimRight(c.cfg.URL, "/") + "/v1/summarize"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create summarize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("summarize request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("summarize returned HTTP %d", resp.StatusCode)
	}

	var result summarizeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode summarize response: %w", err)
	}
	result.Summary = strings.TrimSpace(result.Summary)
	if result.Summary == "" {
		return "", fmt.Errorf("summarize response was empty")
	}
	return result.Summary, nil
}
