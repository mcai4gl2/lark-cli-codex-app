package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type SessionKey struct {
	Provider  string `json:"provider"`
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
}

type SessionRecord struct {
	Key       SessionKey `json:"key"`
	Backend   string     `json:"backend,omitempty"`
	SessionID string     `json:"session_id"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
}

type sessionStateFile struct {
	Sessions []SessionRecord `json:"sessions"`
}

type SessionStore struct {
	path string
	mu   sync.Mutex
}

func NewSessionStore(path string) *SessionStore {
	return &SessionStore{path: strings.TrimSpace(path)}
}

func (s *SessionStore) Enabled() bool {
	return s != nil && strings.TrimSpace(s.path) != ""
}

func (s *SessionStore) LookupRecord(key SessionKey) (SessionRecord, bool, error) {
	if !s.Enabled() {
		return SessionRecord{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return SessionRecord{}, false, err
	}
	idx := findSession(state.Sessions, key)
	if idx < 0 {
		return SessionRecord{}, false, nil
	}
	return state.Sessions[idx], true, nil
}

func (s *SessionStore) Lookup(key SessionKey) (string, error) {
	rec, ok, err := s.LookupRecord(key)
	if err != nil || !ok {
		return "", err
	}
	return rec.SessionID, nil
}

// Put upserts the record. Persists when backend or sessionID is non-empty after trim.
// Empty sessionID clears any previous session id while keeping/updating backend.
func (s *SessionStore) Put(key SessionKey, backend, sessionID string) error {
	if !s.Enabled() {
		return nil
	}
	backend = strings.TrimSpace(backend)
	sessionID = strings.TrimSpace(sessionID)
	if backend == "" && sessionID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339Nano)
	idx := findSession(state.Sessions, key)
	if idx < 0 {
		state.Sessions = append(state.Sessions, SessionRecord{
			Key:       key,
			Backend:   backend,
			SessionID: sessionID,
			CreatedAt: now,
			UpdatedAt: now,
		})
	} else {
		if backend != "" {
			state.Sessions[idx].Backend = backend
		}
		// Always write sessionID (may clear on backend switch).
		state.Sessions[idx].SessionID = sessionID
		state.Sessions[idx].UpdatedAt = now
	}
	return s.saveLocked(state)
}

func (s *SessionStore) Remove(key SessionKey) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	idx := findSession(state.Sessions, key)
	if idx < 0 {
		return false, nil
	}
	state.Sessions = append(state.Sessions[:idx], state.Sessions[idx+1:]...)
	return true, s.saveLocked(state)
}

func (s *SessionStore) loadLocked() (sessionStateFile, error) {
	var state sessionStateFile
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

func (s *SessionStore) saveLocked(state sessionStateFile) error {
	sort.Slice(state.Sessions, func(i, j int) bool {
		a := state.Sessions[i].Key
		b := state.Sessions[j].Key
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
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

	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
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

func findSession(records []SessionRecord, key SessionKey) int {
	for i, record := range records {
		if record.Key == key {
			return i
		}
	}
	return -1
}
