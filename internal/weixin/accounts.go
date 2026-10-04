package weixin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Account holds the credentials issued for one bound Weixin bot.
type Account struct {
	AccountID  string `json:"account_id,omitempty"`
	Token      string `json:"token,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
	CDNBaseURL string `json:"cdn_base_url,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	SavedAt    string `json:"saved_at,omitempty"`
}

// Store persists accounts under <root>, which is normally
// <config dir>/weixin. The layout is multi-account from the start:
//
//	<root>/accounts.json                        index of registered ids
//	<root>/accounts/<id>.json                   credentials
//	<root>/accounts/<id>.sync.json              get_updates_buf cursor
//	<root>/accounts/<id>.context-tokens.json    per-user context tokens
type Store struct {
	root string
}

// NewStore returns an account store rooted at dir.
func NewStore(root string) *Store {
	return &Store{root: strings.TrimSpace(root)}
}

// Root returns the store's base directory.
func (s *Store) Root() string { return s.root }

// AccountsDir returns the directory holding per-account files.
func (s *Store) AccountsDir() string { return filepath.Join(s.root, "accounts") }

// IndexPath returns the account index file path.
func (s *Store) IndexPath() string { return filepath.Join(s.root, "accounts.json") }

// AccountPath returns the credential file path for an account.
func (s *Store) AccountPath(accountID string) string {
	return filepath.Join(s.AccountsDir(), accountID+".json")
}

// SyncBufPath returns the get_updates_buf cursor file path for an account.
func (s *Store) SyncBufPath(accountID string) string {
	return filepath.Join(s.AccountsDir(), accountID+".sync.json")
}

// ContextTokenPath returns the context-token file path for an account.
func (s *Store) ContextTokenPath(accountID string) string {
	return filepath.Join(s.AccountsDir(), accountID+".context-tokens.json")
}

// MediaDir returns the directory that inbound media is saved into.
func (s *Store) MediaDir() string { return filepath.Join(s.root, "media", "inbound") }

// NormalizeAccountID makes a Weixin bot id filesystem-safe, turning
// "b0f5860fdecb@im.bot" into "b0f5860fdecb-im-bot".
func NormalizeAccountID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// DeriveRawAccountID reverses NormalizeAccountID for the two known Weixin id
// suffixes, so files written by an older client can still be read.
func DeriveRawAccountID(normalized string) (string, bool) {
	switch {
	case strings.HasSuffix(normalized, "-im-bot"):
		return strings.TrimSuffix(normalized, "-im-bot") + "@im.bot", true
	case strings.HasSuffix(normalized, "-im-wechat"):
		return strings.TrimSuffix(normalized, "-im-wechat") + "@im.wechat", true
	default:
		return "", false
	}
}

// List returns the registered account ids, oldest registration first.
func (s *Store) List() ([]string, error) {
	data, err := os.ReadFile(s.IndexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("parse weixin account index: %w", err)
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out, nil
}

// Load reads one account's credentials, falling back to the legacy raw-id
// filename when the normalized one is absent.
func (s *Store) Load(accountID string) (Account, error) {
	account, err := readAccountFile(s.AccountPath(accountID))
	if err == nil {
		account.AccountID = accountID
		return account, nil
	}
	if !os.IsNotExist(err) {
		return Account{}, err
	}
	if rawID, ok := DeriveRawAccountID(accountID); ok {
		account, compatErr := readAccountFile(s.AccountPath(rawID))
		if compatErr == nil {
			account.AccountID = accountID
			return account, nil
		}
	}
	return Account{}, err
}

// Resolve returns the account to serve. An empty accountID selects the most
// recently registered account.
func (s *Store) Resolve(accountID string) (Account, error) {
	trimmed := strings.TrimSpace(accountID)
	if trimmed != "" {
		return s.Load(NormalizeAccountID(trimmed))
	}
	ids, err := s.List()
	if err != nil {
		return Account{}, err
	}
	if len(ids) == 0 {
		return Account{}, fmt.Errorf("no Weixin account registered; run `lark weixin login` first")
	}
	return s.Load(ids[len(ids)-1])
}

func readAccountFile(path string) (Account, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Account{}, err
	}
	var account Account
	if err := json.Unmarshal(data, &account); err != nil {
		return Account{}, fmt.Errorf("parse weixin account %s: %w", filepath.Base(path), err)
	}
	return account, nil
}

// Save writes an account's credentials, merging into any existing record, and
// registers the id in the index.
func (s *Store) Save(accountID string, update Account) error {
	if strings.TrimSpace(accountID) == "" {
		return fmt.Errorf("weixin account id is required")
	}
	existing, err := s.Load(accountID)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	merged := existing
	merged.AccountID = accountID
	if strings.TrimSpace(update.Token) != "" {
		merged.Token = strings.TrimSpace(update.Token)
		merged.SavedAt = time.Now().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(update.BaseURL) != "" {
		merged.BaseURL = strings.TrimSpace(update.BaseURL)
	}
	if strings.TrimSpace(update.CDNBaseURL) != "" {
		merged.CDNBaseURL = strings.TrimSpace(update.CDNBaseURL)
	}
	if strings.TrimSpace(update.UserID) != "" {
		merged.UserID = strings.TrimSpace(update.UserID)
	}
	if merged.SavedAt == "" {
		merged.SavedAt = time.Now().Format(time.RFC3339Nano)
	}

	payload, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.AccountPath(accountID), append(payload, '\n')); err != nil {
		return err
	}
	return s.register(accountID)
}

func (s *Store) register(accountID string) error {
	ids, err := s.List()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == accountID {
			return nil
		}
	}
	ids = append(ids, accountID)
	return s.writeIndex(ids)
}

func (s *Store) unregister(accountID string) error {
	ids, err := s.List()
	if err != nil {
		return err
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != accountID {
			kept = append(kept, id)
		}
	}
	if len(kept) == len(ids) {
		return nil
	}
	return s.writeIndex(kept)
}

func (s *Store) writeIndex(ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	payload, err := json.MarshalIndent(ids, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.IndexPath(), append(payload, '\n'))
}

// Remove deletes every file belonging to an account and drops it from the index.
func (s *Store) Remove(accountID string) error {
	paths := []string{
		s.AccountPath(accountID),
		s.SyncBufPath(accountID),
		s.ContextTokenPath(accountID),
	}
	if rawID, ok := DeriveRawAccountID(accountID); ok {
		paths = append(paths,
			s.AccountPath(rawID),
			s.SyncBufPath(rawID),
			s.ContextTokenPath(rawID),
		)
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return s.unregister(accountID)
}

// RemoveStaleForUserID drops any other account bound to the same Weixin user,
// so context-token lookups for that user stay unambiguous.
func (s *Store) RemoveStaleForUserID(currentAccountID, userID string) ([]string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, nil
	}
	ids, err := s.List()
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0)
	for _, id := range ids {
		if id == currentAccountID {
			continue
		}
		account, loadErr := s.Load(id)
		if loadErr != nil {
			continue
		}
		if strings.TrimSpace(account.UserID) != userID {
			continue
		}
		if err := s.Remove(id); err != nil {
			return removed, err
		}
		removed = append(removed, id)
	}
	return removed, nil
}

// Tokens returns the stored bot tokens, newest registration first, capped at
// limit. It feeds get_bot_qrcode's local_token_list.
func (s *Store) Tokens(limit int) []string {
	ids, err := s.List()
	if err != nil {
		return nil
	}
	tokens := make([]string, 0, len(ids))
	for i := len(ids) - 1; i >= 0 && (limit <= 0 || len(tokens) < limit); i-- {
		account, loadErr := s.Load(ids[i])
		if loadErr != nil {
			continue
		}
		if token := strings.TrimSpace(account.Token); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// writeFileAtomic writes data via a temp file plus rename, at mode 0600.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
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
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
