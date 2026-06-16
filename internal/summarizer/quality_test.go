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
	qualityMaxSamples      = 5
	qualityMinChars        = 300
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
			continue
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}

// qualityDetectRepetition counts distinct 5-word n-grams that appear 3+ times.
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
