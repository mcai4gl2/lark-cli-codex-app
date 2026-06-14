package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RecoverMode string

const (
	RecoverModeThread    RecoverMode = "thread"
	RecoverModeMentionDM RecoverMode = "mention-dm"
	RecoverModeOff       RecoverMode = "off"
)

type RecoveryThreadKey struct {
	TeamID    string `json:"team_id"`
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
}

type RecoveryThreadRecord struct {
	Key             RecoveryThreadKey `json:"key"`
	LastProcessedTS string            `json:"last_processed_ts,omitempty"`
	LastSeenAt      string            `json:"last_seen_at,omitempty"`
}

type recoveryStateFile struct {
	Threads []RecoveryThreadRecord `json:"threads"`
}

type RecoveryStore struct {
	path string
	mu   sync.Mutex
}

func NormalizeRecoverMode(mode string) RecoverMode {
	switch RecoverMode(strings.TrimSpace(mode)) {
	case RecoverModeMentionDM:
		return RecoverModeMentionDM
	case RecoverModeOff:
		return RecoverModeOff
	case RecoverModeThread, "":
		return RecoverModeThread
	default:
		return RecoverModeThread
	}
}

func NewRecoveryStore(path string) *RecoveryStore {
	return &RecoveryStore{path: strings.TrimSpace(path)}
}

func (s *RecoveryStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *RecoveryStore) Enabled() bool {
	return s != nil && strings.TrimSpace(s.path) != ""
}

func (s *RecoveryStore) Threads() ([]RecoveryThreadRecord, error) {
	if !s.Enabled() {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	return append([]RecoveryThreadRecord(nil), state.Threads...), nil
}

func (s *RecoveryStore) MarkParticipating(key RecoveryThreadKey) error {
	if !s.Enabled() {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339Nano)
	idx := findRecoveryThread(state.Threads, key)
	if idx < 0 {
		state.Threads = append(state.Threads, RecoveryThreadRecord{Key: key, LastSeenAt: now})
	} else {
		state.Threads[idx].LastSeenAt = now
	}
	return s.saveLocked(state)
}

func (s *RecoveryStore) MarkProcessed(key RecoveryThreadKey, messageTS string) error {
	if !s.Enabled() {
		return nil
	}

	messageTS = strings.TrimSpace(messageTS)
	if messageTS == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339Nano)
	idx := findRecoveryThread(state.Threads, key)
	if idx < 0 {
		state.Threads = append(state.Threads, RecoveryThreadRecord{
			Key:             key,
			LastProcessedTS: messageTS,
			LastSeenAt:      now,
		})
	} else {
		if slackTSAfter(messageTS, state.Threads[idx].LastProcessedTS) {
			state.Threads[idx].LastProcessedTS = messageTS
		}
		state.Threads[idx].LastSeenAt = now
	}
	return s.saveLocked(state)
}

func (s *RecoveryStore) RemoveThread(key RecoveryThreadKey) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	idx := findRecoveryThread(state.Threads, key)
	if idx < 0 {
		return false, nil
	}
	state.Threads = append(state.Threads[:idx], state.Threads[idx+1:]...)
	return true, s.saveLocked(state)
}

func (s *RecoveryStore) Claim(key RecoveryThreadKey, messageTS string) (bool, error) {
	if !s.Enabled() {
		return true, nil
	}

	messageTS = strings.TrimSpace(messageTS)
	if messageTS == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return false, err
	}

	now := time.Now().Format(time.RFC3339Nano)
	idx := findRecoveryThread(state.Threads, key)
	if idx < 0 {
		state.Threads = append(state.Threads, RecoveryThreadRecord{
			Key:             key,
			LastProcessedTS: messageTS,
			LastSeenAt:      now,
		})
		if err := s.saveLocked(state); err != nil {
			return false, err
		}
		return true, nil
	}

	if !slackTSAfter(messageTS, state.Threads[idx].LastProcessedTS) {
		return false, nil
	}

	state.Threads[idx].LastProcessedTS = messageTS
	state.Threads[idx].LastSeenAt = now
	if err := s.saveLocked(state); err != nil {
		return false, err
	}
	return true, nil
}

func (s *RecoveryStore) ShouldProcess(key RecoveryThreadKey, messageTS string) bool {
	if !s.Enabled() {
		return true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return true
	}

	idx := findRecoveryThread(state.Threads, key)
	if idx < 0 {
		return slackTSAfter(messageTS, "")
	}
	return slackTSAfter(messageTS, state.Threads[idx].LastProcessedTS)
}

func (s *RecoveryStore) loadLocked() (recoveryStateFile, error) {
	var state recoveryStateFile
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func (s *RecoveryStore) saveLocked(state recoveryStateFile) error {
	sort.Slice(state.Threads, func(i, j int) bool {
		a := state.Threads[i].Key
		b := state.Threads[j].Key
		if a.TeamID != b.TeamID {
			return a.TeamID < b.TeamID
		}
		if a.ChannelID != b.ChannelID {
			return a.ChannelID < b.ChannelID
		}
		return a.ThreadTS < b.ThreadTS
	})

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".recover-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func findRecoveryThread(records []RecoveryThreadRecord, key RecoveryThreadKey) int {
	for i, record := range records {
		if record.Key == key {
			return i
		}
	}
	return -1
}

func slackTSAfter(candidate, previous string) bool {
	candidate = strings.TrimSpace(candidate)
	previous = strings.TrimSpace(previous)
	if candidate == "" {
		return false
	}
	if previous == "" {
		return true
	}
	candidateFloat, candidateErr := strconv.ParseFloat(candidate, 64)
	previousFloat, previousErr := strconv.ParseFloat(previous, 64)
	if candidateErr == nil && previousErr == nil {
		return candidateFloat > previousFloat
	}
	return candidate > previous
}
