package slackmemory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yjwong/lark-cli/internal/platform"
)

const (
	directionInbound  = "inbound"
	directionOutbound = "outbound"

	eventsFileName        = "events.jsonl"
	memoryFileName        = "memory.md"
	summaryFileName       = "summary.md"
	dailyDateLayout       = "2006-01-02"
	recordedAtTimeLayout  = time.RFC3339Nano
	defaultTeamSegment    = "no-team"
	defaultUnknownSegment = "unknown"
)

type Config struct {
	Root string
}

type Store struct {
	root string
	mu   sync.Mutex
}

type ConversationRecord struct {
	Direction  string                `json:"direction"`
	RecordedAt string                `json:"recorded_at"`
	Event      platform.MessageEvent `json:"event"`
	Text       string                `json:"text,omitempty"`
}

func NewStore(config Config) *Store {
	return &Store{root: config.Root}
}

func (s *Store) Enabled() bool {
	return s != nil && strings.TrimSpace(s.root) != ""
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) RecordInbound(event platform.MessageEvent) error {
	if !s.Enabled() {
		return nil
	}

	record := ConversationRecord{
		Direction:  directionInbound,
		RecordedAt: time.Now().Format(recordedAtTimeLayout),
		Event:      event,
		Text:       event.MessageText,
	}

	if err := s.appendJSONL(s.threadEventsPath(event), record); err != nil {
		return err
	}
	return s.appendJSONL(s.dailyEventsPath(event), record)
}

func (s *Store) RecordOutbound(event platform.MessageEvent, text string) error {
	if !s.Enabled() {
		return nil
	}

	record := ConversationRecord{
		Direction:  directionOutbound,
		RecordedAt: time.Now().Format(recordedAtTimeLayout),
		Event:      event,
		Text:       text,
	}

	return s.appendJSONL(s.threadEventsPath(event), record)
}

func (s *Store) ChannelDir(event platform.MessageEvent) string {
	return filepath.Join(s.rootDir(), teamSegment(event.TeamID), sanitizeSegment(event.ChannelID))
}

func (s *Store) ThreadDir(event platform.MessageEvent) string {
	return filepath.Join(s.ChannelDir(event), "threads", sanitizeSegment(threadID(event)))
}

func (s *Store) ThreadSummaryPath(event platform.MessageEvent) string {
	return filepath.Join(s.ThreadDir(event), summaryFileName)
}

func (s *Store) ThreadMemoryPath(event platform.MessageEvent) string {
	return filepath.Join(s.ThreadDir(event), memoryFileName)
}

func (s *Store) ChannelMemoryPath(event platform.MessageEvent) string {
	return filepath.Join(s.ChannelDir(event), memoryFileName)
}

func (s *Store) rootDir() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) ReadMarkdown(path string, maxChars int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	text := string(data)
	if maxChars > 0 {
		runes := []rune(text)
		if len(runes) > maxChars {
			return string(runes[:maxChars]), nil
		}
	}
	return text, nil
}

func (s *Store) AppendMarkdown(path, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("markdown text is empty")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(text + "\n")
	return err
}

func (s *Store) threadEventsPath(event platform.MessageEvent) string {
	return filepath.Join(s.ThreadDir(event), eventsFileName)
}

func (s *Store) dailyEventsPath(event platform.MessageEvent) string {
	return filepath.Join(s.ChannelDir(event), "daily", dailyDate(event)+".jsonl")
}

func (s *Store) appendJSONL(path string, record ConversationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(data)
	return err
}

func dailyDate(event platform.MessageEvent) string {
	if event.ReceivedAt != "" {
		if receivedAt, err := time.Parse(time.RFC3339Nano, event.ReceivedAt); err == nil {
			return receivedAt.Format(dailyDateLayout)
		}
	}
	return time.Now().Format(dailyDateLayout)
}

func threadID(event platform.MessageEvent) string {
	if strings.TrimSpace(event.ThreadID) != "" {
		return event.ThreadID
	}
	return event.MessageID
}

func teamSegment(teamID string) string {
	if strings.TrimSpace(teamID) == "" {
		return defaultTeamSegment
	}
	return sanitizeSegment(teamID)
}

func sanitizeSegment(segment string) string {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return defaultUnknownSegment
	}
	if segment == "." {
		return "_"
	}

	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	segment = replacer.Replace(segment)
	for strings.Contains(segment, "__") {
		segment = strings.ReplaceAll(segment, "__", "_")
	}
	if segment == "" {
		return defaultUnknownSegment
	}
	return segment
}
