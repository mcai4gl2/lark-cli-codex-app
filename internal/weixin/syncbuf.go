package weixin

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

// SyncState is the durable poll cursor for one account.
//
// GetUpdatesBuf is the server's only de-duplication mechanism, so it must
// survive a crash. LastSeq and LastMessageID additionally guard the window
// between "poll succeeded" and "message handled": a message at or below
// LastSeq has already been dispatched and must not run again.
type SyncState struct {
	GetUpdatesBuf string `json:"get_updates_buf"`
	LastSeq       int64  `json:"last_seq,omitempty"`
	LastMessageID int64  `json:"last_message_id,omitempty"`
}

// SyncStore reads and writes an account's SyncState durably.
type SyncStore struct {
	path string
	mu   sync.Mutex
}

// NewSyncStore returns a cursor store backed by path.
func NewSyncStore(path string) *SyncStore {
	return &SyncStore{path: strings.TrimSpace(path)}
}

// Path returns the backing file path.
func (s *SyncStore) Path() string { return s.path }

// Load reads the persisted state. A missing or unreadable file yields a zero
// state, which restarts the cursor from scratch.
func (s *SyncStore) Load() SyncState {
	if s == nil || s.path == "" {
		return SyncState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return SyncState{}
	}
	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		return SyncState{}
	}
	return state
}

// Save persists the state atomically (temp file plus rename, mode 0600).
func (s *SyncStore) Save(state SyncState) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(payload, '\n'))
}
