# Local Summarizer Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Optionally compress growing thread transcripts fed to Codex by summarizing outbound (LLM reply) records using a local Gemma 3 1B model via HTTP, controlled by config flags.

**Architecture:** A new `internal/summarizer` package provides a thin HTTP client for the local model's `POST /v1/summarize` endpoint behind a `Summarizer` interface. `slackmemory.BuildRecentTranscript` accepts an optional summarizer through `ContextOptions`/`TranscriptOptions` and applies it to outbound records that exceed a configurable character threshold. Config plumbing follows the existing `slack.memory.*` pattern under a new `slack.local_summarizer.*` block. The summarizer is fully optional — any error or unavailability falls back silently to the uncompressed record.

**Tech Stack:** Go 1.24, `net/http` (no new dependencies), local summarizer HTTP API at `POST /v1/summarize` returning `{"summary": "..."}`.

---

## File Map

| Action | Path |
|--------|------|
| Create | `internal/summarizer/client.go` |
| Create | `internal/summarizer/client_test.go` |
| Create | `internal/summarizer/quality_test.go` |
| Modify | `internal/config/config.go` |
| Modify | `internal/slackmemory/context.go` |
| Modify | `internal/slackmemory/context_test.go` |
| Modify | `internal/slack/gateway.go` |
| Modify | `internal/cmd/slack.go` |
| Modify | `config.example.yaml` |

---

## Task 0: Summarizer quality verification test against real thread data

Run this **before** implementing the integration to establish a baseline and confirm the local model is worth wiring in.

**Files:**
- Create: `internal/summarizer/quality_test.go`

This is an integration test gated by a build tag. It walks the real `SLACK_MEMORY_ROOT` directory, feeds actual outbound records to the running local model, and asserts quality thresholds. The output is human-readable so you can inspect quality directly.

**Run command:**
```bash
LOCAL_SUMMARIZER_URL=http://localhost:8080 \
SLACK_MEMORY_ROOT=~/.slack/conversations \
go test ./internal/summarizer/... -tags integration -run TestSummarizerQuality -v -count=1
```

**Quality thresholds** (configurable via env):
- Summary is non-empty
- Summary is shorter than original (compression happened)
- Compression ratio ≤ `QUALITY_MAX_RATIO` (default `0.85`) — summary is at most 85% of original length
- Repetition score = 0 — no 5-word n-gram appears 3+ times (catches model degeneration)

- [ ] **Step 1: Write `internal/summarizer/quality_test.go`**

```go
//go:build integration

package summarizer_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yjwong/lark-cli/internal/summarizer"
)

const (
	qualityMaxSamples    = 5
	qualityMinChars      = 300
	qualityDefaultMaxRatio = 0.85
)

// conversationRecord mirrors slackmemory.ConversationRecord for JSON decoding.
type conversationRecord struct {
	Direction string `json:"direction"`
	Text      string `json:"text"`
}

func TestSummarizerQuality(t *testing.T) {
	url := os.Getenv("LOCAL_SUMMARIZER_URL")
	if url == "" {
		t.Skip("LOCAL_SUMMARIZER_URL not set; skipping integration quality test")
	}
	memoryRoot := os.Getenv("SLACK_MEMORY_ROOT")
	if memoryRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("SLACK_MEMORY_ROOT not set and cannot determine home dir")
		}
		memoryRoot = filepath.Join(home, ".slack", "conversations")
	}

	maxRatio := qualityDefaultMaxRatio
	if v := os.Getenv("QUALITY_MAX_RATIO"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			maxRatio = f
		}
	}

	client := summarizer.NewClient(summarizer.Config{
		URL:            url,
		MaxTokens:      128,
		TimeoutSeconds: 30,
	})

	probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !client.Available(probeCtx) {
		t.Skipf("local summarizer not reachable at %s", url)
	}

	samples := collectSamples(t, memoryRoot, qualityMaxSamples, qualityMinChars)
	if len(samples) == 0 {
		t.Skipf("no outbound records > %d chars found under %s", qualityMinChars, memoryRoot)
	}

	fmt.Printf("\n=== Summarizer Quality Report (%d samples, max_ratio=%.2f) ===\n\n", len(samples), maxRatio)

	for i, s := range samples {
		t.Run(fmt.Sprintf("sample_%d", i+1), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			summary, err := client.Summarize(ctx, s.text)
			origLen := len([]rune(s.text))

			fmt.Printf("--- Sample %d ---\n", i+1)
			fmt.Printf("Source : %s\n", s.file)
			fmt.Printf("Original (%d chars):\n  %s\n\n", origLen, qualityTruncate(s.text, 400))

			if err != nil {
				fmt.Printf("ERROR: %v\n\n", err)
				t.Fatalf("Summarize returned error: %v", err)
			}

			summaryLen := len([]rune(summary))
			ratio := float64(summaryLen) / float64(origLen)
			reps := qualityDetectRepetition(summary)

			fmt.Printf("Summary (%d chars, %.0f%% of original):\n  %s\n", summaryLen, ratio*100, summary)
			fmt.Printf("Repetition score: %d (0 = clean)\n\n", reps)

			if strings.TrimSpace(summary) == "" {
				t.Error("summary is empty")
			}
			if summaryLen >= origLen {
				t.Errorf("summary (%d chars) is not shorter than original (%d chars) — no compression", summaryLen, origLen)
			}
			if ratio > maxRatio {
				t.Errorf("compression ratio %.2f exceeds limit %.2f — summary barely shorter than original", ratio, maxRatio)
			}
			if reps > 0 {
				t.Errorf("detected %d repeated 5-gram(s) — possible model degeneration", reps)
			}
		})
	}

	fmt.Printf("=== End of quality report ===\n\n")
}

type qualitySample struct {
	file string
	text string
}

func collectSamples(t *testing.T, root string, maxSamples, minChars int) []qualitySample {
	t.Helper()
	var samples []qualitySample
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || info.Name() != "events.jsonl" {
			return nil
		}
		records, err := qualityLoadEventFile(path)
		if err != nil {
			return nil
		}
		for _, r := range records {
			if r.Direction == "outbound" && len([]rune(r.Text)) > minChars {
				samples = append(samples, qualitySample{file: path, text: r.Text})
				if len(samples) >= maxSamples {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return samples
}

func qualityLoadEventFile(path string) ([]conversationRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var records []conversationRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r conversationRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // skip malformed lines
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}

// qualityDetectRepetition counts distinct 5-word n-grams that appear 3+ times.
// A non-zero count indicates model degeneration (looping output).
func qualityDetectRepetition(text string) int {
	words := strings.Fields(text)
	if len(words) < 5 {
		return 0
	}
	counts := make(map[string]int, len(words))
	for i := 0; i+5 <= len(words); i++ {
		ngram := strings.Join(words[i:i+5], " ")
		counts[ngram]++
	}
	repeated := 0
	for _, c := range counts {
		if c >= 3 {
			repeated++
		}
	}
	return repeated
}

func qualityTruncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return strings.ReplaceAll(s, "\n", " ")
	}
	return strings.ReplaceAll(string(runes[:max]), "\n", " ") + "…"
}
```

- [ ] **Step 2: Verify the file compiles with the integration tag**

```bash
go build -tags integration ./internal/summarizer/...
```
Expected: no errors.

- [ ] **Step 3: Run against a live local summarizer (skip if not available)**

Start the local summarizer docker service, then:

```bash
LOCAL_SUMMARIZER_URL=http://localhost:8080 \
SLACK_MEMORY_ROOT=~/.slack/conversations \
go test ./internal/summarizer/... -tags integration -run TestSummarizerQuality -v -count=1
```

If the service is not running the test auto-skips. Review the printed report to judge summary quality before proceeding with integration.

- [ ] **Step 4: Optionally relax the compression threshold for short-but-verbose records**

If the model produces good summaries but ratio fails (e.g., the original was only 350 chars), re-run with a relaxed threshold:

```bash
LOCAL_SUMMARIZER_URL=http://localhost:8080 \
SLACK_MEMORY_ROOT=~/.slack/conversations \
QUALITY_MAX_RATIO=0.95 \
go test ./internal/summarizer/... -tags integration -run TestSummarizerQuality -v -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/quality_test.go
git commit -m "test: add integration quality test for local summarizer against real thread data"
```

---

## Task 1: `internal/summarizer` package — interface and HTTP client

**Files:**
- Create: `internal/summarizer/client.go`
- Create: `internal/summarizer/client_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/summarizer/client_test.go
package summarizer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yjwong/lark-cli/internal/summarizer"
)

func TestClient_Summarize_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/summarize" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "abc",
			"object":  "summary",
			"model":   "local-summarizer",
			"summary": "Compressed reply.",
		})
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL, MaxTokens: 64})
	result, err := client.Summarize(context.Background(), "This is a long LLM reply that should be compressed.")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result != "Compressed reply." {
		t.Errorf("unexpected summary: %q", result)
	}
}

func TestClient_Summarize_emptyText(t *testing.T) {
	client := summarizer.NewClient(summarizer.Config{URL: "http://unused"})
	result, err := client.Summarize(context.Background(), "   ")
	if err != nil {
		t.Fatalf("expected no error for empty input, got %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result for empty input, got %q", result)
	}
}

func TestClient_Summarize_upstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL})
	_, err := client.Summarize(context.Background(), "some text")
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestClient_Available_healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL})
	if !client.Available(context.Background()) {
		t.Error("expected Available to return true for healthy server")
	}
}

func TestClient_Available_unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "loading", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := summarizer.NewClient(summarizer.Config{URL: srv.URL})
	if client.Available(context.Background()) {
		t.Error("expected Available to return false for unhealthy server")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd /home/ligeng/Codes/lark-cli-codex-app
go test ./internal/summarizer/... 2>&1
```
Expected: `cannot find package` or `no Go files in ...`

- [ ] **Step 3: Implement `internal/summarizer/client.go`**

```go
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
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/summarizer/... -v
```
Expected: all 5 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/summarizer/client.go internal/summarizer/client_test.go
git commit -m "feat: add internal/summarizer HTTP client for local Gemma model"
```

---

## Task 2: Config additions for `slack.local_summarizer.*`

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add struct and getters — find the `Slack` struct in config.go (around line 39) and append `LocalSummarizer` block**

In the `Slack` struct inside `Config`, after the `Agent` sub-struct, add:

```go
		LocalSummarizer struct {
			Enabled        bool   `mapstructure:"enabled"`
			URL            string `mapstructure:"url"`
			MaxTokens      int    `mapstructure:"max_tokens"`
			TimeoutSeconds int    `mapstructure:"timeout_seconds"`
			MinChars       int    `mapstructure:"min_chars"`
		} `mapstructure:"local_summarizer"`
```

- [ ] **Step 2: Add defaults — in the `Init()` function after the existing slack defaults (around line 133)**

```go
	viper.SetDefault("slack.local_summarizer.enabled", false)
	viper.SetDefault("slack.local_summarizer.url", "http://localhost:8080")
	viper.SetDefault("slack.local_summarizer.max_tokens", 128)
	viper.SetDefault("slack.local_summarizer.timeout_seconds", 30)
	viper.SetDefault("slack.local_summarizer.min_chars", 300)
```

- [ ] **Step 3: Add env bindings — after the existing `slack.memory.*` bindings (around line 190)**

```go
	viper.BindEnv("slack.local_summarizer.enabled", "SLACK_LOCAL_SUMMARIZER_ENABLED")
	viper.BindEnv("slack.local_summarizer.url", "SLACK_LOCAL_SUMMARIZER_URL")
	viper.BindEnv("slack.local_summarizer.max_tokens", "SLACK_LOCAL_SUMMARIZER_MAX_TOKENS")
	viper.BindEnv("slack.local_summarizer.timeout_seconds", "SLACK_LOCAL_SUMMARIZER_TIMEOUT_SECONDS")
	viper.BindEnv("slack.local_summarizer.min_chars", "SLACK_LOCAL_SUMMARIZER_MIN_CHARS")
```

- [ ] **Step 4: Add getter functions — after `GetSlackMemoryMaxTranscriptRecords` (around line 431)**

```go
// GetSlackLocalSummarizerEnabled returns whether the local summarizer is enabled for Slack transcripts.
func GetSlackLocalSummarizerEnabled() bool {
	return viper.GetBool("slack.local_summarizer.enabled")
}

// GetSlackLocalSummarizerURL returns the base URL of the local summarizer service.
func GetSlackLocalSummarizerURL() string {
	return strings.TrimSpace(viper.GetString("slack.local_summarizer.url"))
}

// GetSlackLocalSummarizerMaxTokens returns the token budget for each summarization call.
func GetSlackLocalSummarizerMaxTokens() int {
	v := viper.GetInt("slack.local_summarizer.max_tokens")
	if v <= 0 {
		return 128
	}
	return v
}

// GetSlackLocalSummarizerTimeoutSeconds returns the HTTP timeout for summarization calls.
func GetSlackLocalSummarizerTimeoutSeconds() int {
	v := viper.GetInt("slack.local_summarizer.timeout_seconds")
	if v <= 0 {
		return 30
	}
	return v
}

// GetSlackLocalSummarizerMinChars returns the minimum outbound record length before summarization is attempted.
func GetSlackLocalSummarizerMinChars() int {
	v := viper.GetInt("slack.local_summarizer.min_chars")
	if v <= 0 {
		return 300
	}
	return v
}
```

- [ ] **Step 5: Verify the project still compiles**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add slack.local_summarizer config block with URL, token, timeout, and min_chars"
```

---

## Task 3: Thread summarizer through `slackmemory.ContextOptions` and `BuildRecentTranscript`

**Files:**
- Modify: `internal/slackmemory/context.go`
- Modify: `internal/slackmemory/context_test.go`

- [ ] **Step 1: Write the failing tests — append to `context_test.go`**

First check what's already in the test file:

```bash
cat internal/slackmemory/context_test.go
```

Then add these test cases (or create the file if it only has a package declaration):

```go
// internal/slackmemory/context_test.go  (add to existing file)
package slackmemory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yjwong/lark-cli/internal/platform"
	"github.com/yjwong/lark-cli/internal/slackmemory"
)

// stubSummarizer implements slackmemory.Summarizer for tests.
type stubSummarizer struct {
	called int
	result string
	err    error
}

func (s *stubSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	s.called++
	return s.result, s.err
}

func TestBuildRecentTranscript_summarizesLongOutbound(t *testing.T) {
	dir := t.TempDir()
	store := slackmemory.NewStore(slackmemory.Config{Root: dir})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T1",
		ChannelID: "C1",
		ThreadID:  "thread1",
		MessageID: "msg2",
	}

	// Record a short inbound and a long outbound
	inboundEvent := event
	inboundEvent.MessageID = "msg1"
	inboundEvent.MessageText = "Short user question"
	_ = store.RecordInbound(inboundEvent)
	_ = store.RecordOutbound(inboundEvent, "This is a very long LLM reply that exceeds the min_chars threshold and should be summarized by the local model.")

	stub := &stubSummarizer{result: "Summarized reply."}
	transcript, err := slackmemory.BuildRecentTranscript(store, event, slackmemory.TranscriptOptions{
		MaxChars:          4000,
		MaxRecords:        10,
		Summarizer:        stub,
		SummarizeMinChars: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.called != 1 {
		t.Errorf("expected Summarize called once, got %d", stub.called)
	}
	if !contains(transcript, "Summarized reply.") {
		t.Errorf("expected summarized text in transcript, got:\n%s", transcript)
	}
}

func TestBuildRecentTranscript_skipsShortOutbound(t *testing.T) {
	dir := t.TempDir()
	store := slackmemory.NewStore(slackmemory.Config{Root: dir})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T1",
		ChannelID: "C1",
		ThreadID:  "thread2",
		MessageID: "msg2",
	}

	inboundEvent := event
	inboundEvent.MessageID = "msg1"
	inboundEvent.MessageText = "Short user question"
	_ = store.RecordInbound(inboundEvent)
	_ = store.RecordOutbound(inboundEvent, "Short reply.")

	stub := &stubSummarizer{result: "Should not appear."}
	_, err := slackmemory.BuildRecentTranscript(store, event, slackmemory.TranscriptOptions{
		MaxChars:          4000,
		MaxRecords:        10,
		Summarizer:        stub,
		SummarizeMinChars: 500, // threshold far above "Short reply." length
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.called != 0 {
		t.Errorf("expected Summarize not called for short outbound, got %d calls", stub.called)
	}
}

func TestBuildRecentTranscript_fallsBackOnSummarizerError(t *testing.T) {
	dir := t.TempDir()
	store := slackmemory.NewStore(slackmemory.Config{Root: dir})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T1",
		ChannelID: "C1",
		ThreadID:  "thread3",
		MessageID: "msg2",
	}

	longText := "This is a long outbound message that definitely exceeds fifty characters in length."
	inboundEvent := event
	inboundEvent.MessageID = "msg1"
	inboundEvent.MessageText = "User question"
	_ = store.RecordInbound(inboundEvent)
	_ = store.RecordOutbound(inboundEvent, longText)

	stub := &stubSummarizer{err: os.ErrDeadlineExceeded}
	transcript, err := slackmemory.BuildRecentTranscript(store, event, slackmemory.TranscriptOptions{
		MaxChars:          4000,
		MaxRecords:        10,
		Summarizer:        stub,
		SummarizeMinChars: 50,
	})
	if err != nil {
		t.Fatalf("fallback should not return error, got: %v", err)
	}
	if !contains(transcript, longText) {
		t.Errorf("expected original text on error fallback, got:\n%s", transcript)
	}
}

func TestBuildRecentTranscript_doesNotSummarizeInbound(t *testing.T) {
	dir := t.TempDir()
	store := slackmemory.NewStore(slackmemory.Config{Root: dir})
	event := platform.MessageEvent{
		Provider:  "slack",
		TeamID:    "T1",
		ChannelID: "C1",
		ThreadID:  "thread4",
		MessageID: "msg2",
	}

	longInbound := "This is a long user message that definitely exceeds the threshold for summarization consideration."
	inboundEvent := event
	inboundEvent.MessageID = "msg1"
	inboundEvent.MessageText = longInbound
	_ = store.RecordInbound(inboundEvent)

	stub := &stubSummarizer{result: "Should not appear."}
	_, err := slackmemory.BuildRecentTranscript(store, event, slackmemory.TranscriptOptions{
		MaxChars:          4000,
		MaxRecords:        10,
		Summarizer:        stub,
		SummarizeMinChars: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.called != 0 {
		t.Errorf("inbound messages must not be summarized, but Summarize called %d times", stub.called)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i+len(substr) <= len(s); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/slackmemory/... -run TestBuildRecentTranscript_summarize -v 2>&1
```
Expected: compilation errors because `Summarizer` field and `SummarizeMinChars` don't exist yet.

- [ ] **Step 3: Add `Summarizer` interface and fields to `context.go`**

At the top of `internal/slackmemory/context.go`, add the import and interface. The `Summarizer` interface is defined in the `slackmemory` package to avoid an import cycle (the `summarizer` package has no dependency on `slackmemory`; this interface is satisfied by `*summarizer.Client`).

Add to the import block:
```go
import (
	"context"     // add this
	"fmt"
	"strings"

	"github.com/yjwong/lark-cli/internal/platform"
)
```

Add the interface definition right after the package declaration and before `const defaultMaxSectionChars`:

```go
// Summarizer compresses text. The local Gemma model client satisfies this interface.
type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}
```

- [ ] **Step 4: Extend `ContextOptions` and `TranscriptOptions`**

Change `ContextOptions` to:
```go
type ContextOptions struct {
	MaxSectionChars         int
	IncludeThreadTranscript bool
	MaxTranscriptChars      int
	MaxTranscriptRecords    int
	Summarizer              Summarizer
	SummarizeMinChars       int
}
```

Change `TranscriptOptions` to:
```go
type TranscriptOptions struct {
	MaxChars          int
	MaxRecords        int
	Summarizer        Summarizer
	SummarizeMinChars int
}
```

- [ ] **Step 5: Pass summarizer fields through `BuildPromptContext` into `BuildRecentTranscript`**

In `BuildPromptContext`, update the `BuildRecentTranscript` call (around line 53):

```go
	if opts.IncludeThreadTranscript {
		transcript, err := BuildRecentTranscript(store, event, TranscriptOptions{
			MaxChars:          opts.MaxTranscriptChars,
			MaxRecords:        opts.MaxTranscriptRecords,
			Summarizer:        opts.Summarizer,
			SummarizeMinChars: opts.SummarizeMinChars,
		})
```

- [ ] **Step 6: Add summarization logic in `BuildRecentTranscript`**

Inside the loop in `BuildRecentTranscript`, replace the existing `rendered := renderTranscriptRecord(record)` section with:

```go
		rendered := renderTranscriptRecord(record)
		if opts.Summarizer != nil && record.Direction == directionOutbound {
			minChars := opts.SummarizeMinChars
			if minChars <= 0 {
				minChars = 300
			}
			if len([]rune(record.Text)) > minChars {
				if summary, err := opts.Summarizer.Summarize(context.Background(), record.Text); err == nil && strings.TrimSpace(summary) != "" {
					summarizedRecord := record
					summarizedRecord.Text = "[Summary] " + summary
					rendered = renderTranscriptRecord(summarizedRecord)
				}
			}
		}
```

- [ ] **Step 7: Run tests to confirm they pass**

```bash
go test ./internal/slackmemory/... -v
```
Expected: all tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/slackmemory/context.go internal/slackmemory/context_test.go
git commit -m "feat: slackmemory transcript now optionally summarizes long outbound records"
```

---

## Task 4: Wire summarizer into `slack/gateway.go`

**Files:**
- Modify: `internal/slack/gateway.go`

- [ ] **Step 1: Add fields to `Config` struct**

In `internal/slack/gateway.go`, add two fields to the `Config` struct after `ProcessingReactionName`:

```go
	LocalSummarizer      *summarizer.Client
	LocalSummarizerMinChars int
```

Add the import at the top of the file:

```go
"github.com/yjwong/lark-cli/internal/summarizer"
```

- [ ] **Step 2: Update `memoryPromptProvider` to carry the summarizer**

Change `memoryPromptProvider` struct to:

```go
type memoryPromptProvider struct {
	store                   *slackmemory.Store
	maxSectionChars         int
	includeThreadTranscript bool
	maxTranscriptChars      int
	maxTranscriptRecords    int
	summarizer              slackmemory.Summarizer
	summarizeMinChars       int
}
```

Update `PromptContext` on `memoryPromptProvider`:

```go
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
```

- [ ] **Step 3: Pass `LocalSummarizer` when constructing `memoryPromptProvider` in `NewGateway`**

Update the `cfg.Agent.ContextProvider = memoryPromptProvider{...}` block:

```go
		cfg.Agent.ContextProvider = memoryPromptProvider{
			store:                   memoryStore,
			maxSectionChars:         cfg.MemoryMaxSectionChars,
			includeThreadTranscript: cfg.MemoryIncludeThreadTranscript,
			maxTranscriptChars:      cfg.MemoryMaxTranscriptChars,
			maxTranscriptRecords:    cfg.MemoryMaxTranscriptRecords,
			summarizer:              cfg.LocalSummarizer,
			summarizeMinChars:       cfg.LocalSummarizerMinChars,
		}
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./internal/slack/...
```
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/slack/gateway.go
git commit -m "feat: wire local summarizer into Slack gateway memory prompt provider"
```

---

## Task 5: Wire config into `internal/cmd/slack.go`

**Files:**
- Modify: `internal/cmd/slack.go`

- [ ] **Step 1: Import the summarizer package**

Add to the import block in `internal/cmd/slack.go`:

```go
"github.com/yjwong/lark-cli/internal/summarizer"
```

- [ ] **Step 2: Instantiate the client and populate `cfg` in `slackGatewayServeCmd.Run`**

After the `agentCfg` block and before `cfg := slackgateway.Config{...}`, add:

```go
		var localSummarizer *summarizer.Client
		if config.GetSlackLocalSummarizerEnabled() {
			url := config.GetSlackLocalSummarizerURL()
			if strings.TrimSpace(url) != "" {
				localSummarizer = summarizer.NewClient(summarizer.Config{
					URL:            url,
					MaxTokens:      config.GetSlackLocalSummarizerMaxTokens(),
					TimeoutSeconds: config.GetSlackLocalSummarizerTimeoutSeconds(),
				})
			}
		}
```

Add two fields to the `cfg` literal:

```go
			LocalSummarizer:         localSummarizer,
			LocalSummarizerMinChars: config.GetSlackLocalSummarizerMinChars(),
```

Also add to the startup log output JSON map:

```go
			"local_summarizer_enabled": localSummarizer != nil,
			"local_summarizer_url":     config.GetSlackLocalSummarizerURL(),
			"local_summarizer_min_chars": config.GetSlackLocalSummarizerMinChars(),
```

- [ ] **Step 3: Verify the full project builds**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 4: Run all tests**

```bash
go test ./... 2>&1
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/slack.go
git commit -m "feat: instantiate local summarizer from config and pass to Slack gateway"
```

---

## Task 6: Document new config in `config.example.yaml`

**Files:**
- Modify: `config.example.yaml`

- [ ] **Step 1: Add the `local_summarizer` block inside the `slack:` section**

After the `slack.memory:` block (after line 73) and before `slack.agent:`, add:

```yaml
  local_summarizer:
    # Enable summarization of long outbound (LLM reply) records in thread transcripts.
    # Requires a running local-summarizer service (Gemma 3 1B via llama.cpp).
    enabled: false
    # Base URL of the local summarizer gateway.
    url: "http://localhost:8080"
    # Maximum tokens for each summary response (kept small for a 1B model).
    max_tokens: 128
    # HTTP timeout for each summarization call in seconds.
    timeout_seconds: 30
    # Only summarize outbound records longer than this many characters.
    # Short replies are passed through unchanged.
    min_chars: 300
```

- [ ] **Step 2: Verify the project still builds**

```bash
go build ./...
```

- [ ] **Step 3: Commit**

```bash
git add config.example.yaml
git commit -m "docs: add slack.local_summarizer config block to example"
```

---

## Self-review

**Spec coverage:**

| Requirement | Task |
|---|---|
| Quality verified on real data before integrating | Task 0 |
| Local Gemma model HTTP client | Task 1 |
| Optional via config + availability check | Tasks 2 & 5 (enabled flag; `Available()` can be called at startup) |
| Summarize only LLM (outbound) replies | Task 3 (directionOutbound guard) |
| Graceful fallback when unavailable or error | Task 3 (error fallback preserves original) |
| Config knobs: enabled, URL, tokens, timeout, min_chars | Task 2 |
| Wired into Slack thread transcript path | Tasks 3–5 |
| Documented in config.example.yaml | Task 6 |

**Gaps / follow-up not in scope:**
- Lark/Feishu `gateway` path: the Lark gateway does not currently use `slackmemory` at all; the same client could be threaded through `internal/gateway` when that path adopts memory context — deferred.
- Startup availability probe (`client.Available(ctx)`) is implemented but not called at gateway startup; callers can add a warning log if desired.
