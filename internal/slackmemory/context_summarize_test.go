package slackmemory_test

import (
	"context"
	"os"
	"strings"
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
	if !strings.Contains(transcript, "Summarized reply.") {
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
		SummarizeMinChars: 500,
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
	if !strings.Contains(transcript, longText) {
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
