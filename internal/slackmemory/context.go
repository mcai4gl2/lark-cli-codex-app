package slackmemory

import (
	"context"
	"fmt"
	"strings"

	"github.com/yjwong/lark-cli/internal/platform"
)

const defaultMaxSectionChars = 2000
const defaultMaxTranscriptChars = 8000
const defaultMaxTranscriptRecords = 30

// Summarizer compresses text. The local Gemma model client satisfies this interface.
type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}

type ContextOptions struct {
	MaxSectionChars         int
	IncludeThreadTranscript bool
	MaxTranscriptChars      int
	MaxTranscriptRecords    int
	Summarizer              Summarizer
	SummarizeMinChars       int
}

func BuildPromptContext(store *Store, event platform.MessageEvent, opts ContextOptions) (string, error) {
	if store == nil || !store.Enabled() {
		return "", nil
	}

	max := opts.MaxSectionChars
	if max <= 0 {
		max = defaultMaxSectionChars
	}

	sections := []struct {
		title string
		path  string
	}{
		{title: "Slack channel memory", path: store.ChannelMemoryPath(event)},
		{title: "Slack thread memory", path: store.ThreadMemoryPath(event)},
		{title: "Slack thread summary", path: store.ThreadSummaryPath(event)},
	}

	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		text, err := store.ReadMarkdown(section.path, max)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", section.title, err)
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, "## "+section.title+"\n"+text)
	}

	if opts.IncludeThreadTranscript {
		transcript, err := BuildRecentTranscript(store, event, TranscriptOptions{
			MaxChars:          opts.MaxTranscriptChars,
			MaxRecords:        opts.MaxTranscriptRecords,
			Summarizer:        opts.Summarizer,
			SummarizeMinChars: opts.SummarizeMinChars,
		})
		if err != nil {
			return "", fmt.Errorf("build Slack recent thread transcript: %w", err)
		}
		if strings.TrimSpace(transcript) != "" {
			parts = append(parts, "## Slack recent thread transcript\n"+transcript)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

type TranscriptOptions struct {
	MaxChars          int
	MaxRecords        int
	Summarizer        Summarizer
	SummarizeMinChars int
}

func BuildRecentTranscript(store *Store, event platform.MessageEvent, opts TranscriptOptions) (string, error) {
	if store == nil || !store.Enabled() {
		return "", nil
	}

	maxChars := opts.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxTranscriptChars
	}
	maxRecords := opts.MaxRecords
	if maxRecords <= 0 {
		maxRecords = defaultMaxTranscriptRecords
	}

	records, err := store.ThreadRecords(event)
	if err != nil {
		return "", err
	}

	selected := make([]string, 0, maxRecords)
	usedChars := 0
	omitted := false
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if skipTranscriptRecord(record, event) {
			continue
		}
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
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		addedChars := len([]rune(rendered))
		if len(selected) > 0 {
			addedChars += 2
		}
		if len(selected) >= maxRecords || usedChars+addedChars > maxChars {
			omitted = true
			continue
		}
		selected = append(selected, rendered)
		usedChars += addedChars
	}

	reverseStrings(selected)
	transcript := strings.Join(selected, "\n\n")
	if omitted && transcript != "" {
		transcript = "[Older thread messages omitted; see Slack thread summary and memory above.]\n\n" + transcript
	}
	return transcript, nil
}

func skipTranscriptRecord(record ConversationRecord, current platform.MessageEvent) bool {
	if strings.TrimSpace(record.Text) == "" {
		return true
	}
	return record.Direction == directionInbound &&
		strings.TrimSpace(record.Event.MessageID) != "" &&
		record.Event.MessageID == current.MessageID
}

func renderTranscriptRecord(record ConversationRecord) string {
	text := strings.TrimSpace(record.Text)
	if text == "" {
		return ""
	}

	timestamp := strings.TrimSpace(record.RecordedAt)
	if timestamp == "" {
		timestamp = strings.TrimSpace(record.Event.ReceivedAt)
	}
	messageID := strings.TrimSpace(record.Event.MessageID)
	userID := strings.TrimSpace(record.Event.UserID)

	metadata := []string{strings.TrimSpace(record.Direction)}
	if timestamp != "" {
		metadata = append(metadata, timestamp)
	}
	if record.Direction == directionInbound && userID != "" {
		metadata = append(metadata, "user="+userID)
	}
	if messageID != "" {
		metadata = append(metadata, "message="+messageID)
	}

	speaker := "Message"
	switch record.Direction {
	case directionInbound:
		speaker = "User"
	case directionOutbound:
		speaker = "Codex"
	}

	return fmt.Sprintf("[%s]\n%s: %s", strings.Join(metadata, " "), speaker, text)
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
