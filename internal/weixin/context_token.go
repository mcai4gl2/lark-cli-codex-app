package weixin

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
)

// ContextTokenStore caches the per-user context_token for one account.
//
// The server issues a context_token with every inbound message, and it must be
// echoed verbatim on every outbound send to that user. The in-memory map is the
// lookup path; the disk file (one per account, so the user id alone keys it)
// lets tokens survive a gateway restart.
type ContextTokenStore struct {
	path   string
	mu     sync.RWMutex
	tokens map[string]string
}

// NewContextTokenStore returns a token cache backed by path.
func NewContextTokenStore(path string) *ContextTokenStore {
	return &ContextTokenStore{
		path:   strings.TrimSpace(path),
		tokens: make(map[string]string),
	}
}

// Restore loads persisted tokens into memory. A missing or unreadable file is
// not an error: tokens are refreshed by the next inbound message anyway.
func (s *ContextTokenStore) Restore() (int, error) {
	if s == nil || s.path == "" {
		return 0, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var tokens map[string]string
	if err := json.Unmarshal(data, &tokens); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for userID, token := range tokens {
		if strings.TrimSpace(userID) == "" || strings.TrimSpace(token) == "" {
			continue
		}
		s.tokens[userID] = token
		count++
	}
	return count, nil
}

// Set records a user's context token and persists the account's token file.
func (s *ContextTokenStore) Set(userID, token string) error {
	userID = strings.TrimSpace(userID)
	token = strings.TrimSpace(token)
	if s == nil || userID == "" || token == "" {
		return nil
	}

	s.mu.Lock()
	if s.tokens[userID] == token {
		s.mu.Unlock()
		return nil
	}
	s.tokens[userID] = token
	snapshot := make(map[string]string, len(s.tokens))
	for k, v := range s.tokens {
		snapshot[k] = v
	}
	s.mu.Unlock()

	if s.path == "" {
		return nil
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(payload, '\n'))
}

// Get returns the cached context token for a user.
func (s *ContextTokenStore) Get(userID string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.tokens[strings.TrimSpace(userID)]
	return token, ok
}

// Len returns how many users have a cached token.
func (s *ContextTokenStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens)
}
