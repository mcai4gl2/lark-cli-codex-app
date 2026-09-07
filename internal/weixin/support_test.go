package weixin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionGuardPausesAndExpires(t *testing.T) {
	guard := NewSessionGuard()
	if guard.Paused() || guard.AssertActive() != nil {
		t.Fatalf("a fresh guard must be active")
	}

	now := time.Now()
	guard.now = func() time.Time { return now }
	if got := guard.Pause(); got != StaleTokenPause {
		t.Fatalf("Pause() = %s, want %s", got, StaleTokenPause)
	}
	if !guard.Paused() {
		t.Fatalf("the guard should be paused")
	}
	err := guard.AssertActive()
	if err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("AssertActive() error = %v", err)
	}
	if !strings.Contains(err.Error(), "60 min") {
		t.Fatalf("AssertActive() should report the remaining minutes, got %v", err)
	}

	// Once the window passes the guard clears itself.
	now = now.Add(StaleTokenPause + time.Second)
	if guard.Paused() || guard.AssertActive() != nil {
		t.Fatalf("the guard should have expired")
	}
	if got := guard.Remaining(); got != 0 {
		t.Fatalf("Remaining() = %s, want 0", got)
	}
}

func TestSessionGuardNilIsInert(t *testing.T) {
	var guard *SessionGuard
	if guard.Paused() || guard.AssertActive() != nil || guard.Pause() != 0 {
		t.Fatalf("a nil guard must never block a send")
	}
}

func TestConfigCacheCachesAndRefreshes(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeJSON(t, w, GetConfigResp{Ret: 0, TypingTicket: "ticket-1"})
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()})
	cache := NewConfigCache(client, nil)
	// Pin the jitter so the refresh window is deterministic.
	cache.jitter = func() float64 { return 0.5 }

	if got := cache.TypingTicket(context.Background(), "u1", "ctx"); got != "ticket-1" {
		t.Fatalf("TypingTicket() = %q", got)
	}
	if got := cache.TypingTicket(context.Background(), "u1", "ctx"); got != "ticket-1" {
		t.Fatalf("TypingTicket() = %q", got)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("getconfig was called %d times, want 1 (the result should be cached)", calls)
	}

	// A second user is fetched separately.
	if got := cache.TypingTicket(context.Background(), "u2", "ctx"); got != "ticket-1" {
		t.Fatalf("TypingTicket(u2) = %q", got)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("getconfig was called %d times, want 2", calls)
	}
}

func TestConfigCacheBacksOffOnFailure(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Token: "tok", Client: server.Client()})
	cache := NewConfigCache(client, nil)

	now := time.Now()
	cache.now = func() time.Time { return now }

	// The typing indicator is cosmetic, so a failure yields an empty ticket
	// rather than an error.
	if got := cache.TypingTicket(context.Background(), "u1", ""); got != "" {
		t.Fatalf("TypingTicket() = %q, want empty on failure", got)
	}
	if got := cache.TypingTicket(context.Background(), "u1", ""); got != "" {
		t.Fatalf("TypingTicket() = %q", got)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("getconfig was called %d times; the retry delay was not honored", calls)
	}

	// Past the retry window it tries again, and the delay grows.
	now = now.Add(configCacheInitialRetry + time.Millisecond)
	cache.TypingTicket(context.Background(), "u1", "")
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("getconfig was called %d times, want 2", calls)
	}
	now = now.Add(configCacheInitialRetry + time.Millisecond)
	cache.TypingTicket(context.Background(), "u1", "")
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("the retry delay should have doubled, but a fetch happened (%d calls)", calls)
	}
}

func TestConfigCacheNilIsInert(t *testing.T) {
	var cache *ConfigCache
	if got := cache.TypingTicket(context.Background(), "u1", ""); got != "" {
		t.Fatalf("TypingTicket() = %q", got)
	}
}

func TestContextTokenStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	store := NewContextTokenStore(path)

	if _, ok := store.Get("u1"); ok {
		t.Fatalf("an empty store should not resolve a token")
	}
	if err := store.Set("u1", "ctx-1"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Set("u2", "ctx-2"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if token, ok := store.Get("u1"); !ok || token != "ctx-1" {
		t.Fatalf("Get(u1) = %q, %v", token, ok)
	}
	if store.Len() != 2 {
		t.Fatalf("Len() = %d", store.Len())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 600", perm)
	}

	restored := NewContextTokenStore(path)
	count, err := restored.Restore()
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("Restore() = %d, want 2", count)
	}
	if token, _ := restored.Get("u2"); token != "ctx-2" {
		t.Fatalf("restored token = %q", token)
	}
}

func TestContextTokenStoreIgnoresBlanksAndMissingFiles(t *testing.T) {
	store := NewContextTokenStore(filepath.Join(t.TempDir(), "missing.json"))
	if count, err := store.Restore(); err != nil || count != 0 {
		t.Fatalf("Restore() = %d, %v", count, err)
	}
	if err := store.Set("", "ctx"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Set("u1", "  "); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("blank entries were stored: Len() = %d", store.Len())
	}
}

func TestContextTokenStoreRestoreRejectsCorruptFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewContextTokenStore(path).Restore(); err == nil {
		t.Fatalf("Restore() should report a corrupt token file")
	}
}

func TestSanitizeBotAgent(t *testing.T) {
	cases := map[string]string{
		"":                         DefaultBotAgent,
		"   ":                      DefaultBotAgent,
		"lark-cli/1.2.3":           "lark-cli/1.2.3",
		"lark-cli/1.2.3 (linux)":   "lark-cli/1.2.3 (linux)",
		"lark-cli/1.2.3 codex/0.9": "lark-cli/1.2.3 codex/0.9",
		// Tokens that do not parse are dropped rather than passed through.
		"not a product":               DefaultBotAgent,
		"lark-cli/1.2.3 garbage here": "lark-cli/1.2.3",
	}
	for input, want := range cases {
		if got := SanitizeBotAgent(input); got != want {
			t.Fatalf("SanitizeBotAgent(%q) = %q, want %q", input, got, want)
		}
	}

	long := strings.TrimSpace(strings.Repeat("name/1.0 ", 60))
	got := SanitizeBotAgent(long)
	if len(got) > botAgentMaxLen {
		t.Fatalf("SanitizeBotAgent() returned %d bytes, over the %d cap", len(got), botAgentMaxLen)
	}
	if got == DefaultBotAgent {
		t.Fatalf("a long but valid agent should be truncated, not discarded")
	}
}

func TestRedactHelpers(t *testing.T) {
	if got := RedactToken(""); got != "(none)" {
		t.Fatalf("RedactToken(\"\") = %q", got)
	}
	if got := RedactToken("abc"); !strings.HasPrefix(got, "****") {
		t.Fatalf("a short token must be fully masked, got %q", got)
	}
	full := RedactToken("supersecrettoken")
	if strings.Contains(full, "secrettoken") {
		t.Fatalf("RedactToken leaked the token: %q", full)
	}

	body := `{"bot_token":"abc","context_token":"xyz","text":"hello"}`
	redacted := RedactBody(body, 500)
	if strings.Contains(redacted, "abc") || strings.Contains(redacted, "xyz") {
		t.Fatalf("RedactBody leaked a secret: %q", redacted)
	}
	if !strings.Contains(redacted, "hello") {
		t.Fatalf("RedactBody dropped non-sensitive content: %q", redacted)
	}
	if got := RedactBody("  ", 10); got != "(empty)" {
		t.Fatalf("RedactBody(blank) = %q", got)
	}

	if got := RedactURL("https://cdn.test/download?encrypted_query_param=secret"); strings.Contains(got, "secret") {
		t.Fatalf("RedactURL leaked the query string: %q", got)
	}
	if got := RedactURL("https://cdn.test/path"); got != "https://cdn.test/path" {
		t.Fatalf("RedactURL(%q) = %q", "https://cdn.test/path", got)
	}
	if got := RedactURL("not a url"); got == "" {
		t.Fatalf("RedactURL should fall back to a truncated string")
	}

	if got := TruncateForLog("abcdef", 3); got != "abc…(len=6)" {
		t.Fatalf("TruncateForLog() = %q", got)
	}
	if got := TruncateForLog("abc", 10); got != "abc" {
		t.Fatalf("TruncateForLog() = %q", got)
	}
}

func TestGetUpdatesRequestBodyShape(t *testing.T) {
	// A regression guard on the wire contract: the server reads get_updates_buf
	// even when it is empty, so the field must not be omitted.
	payload, err := json.Marshal(GetUpdatesReq{GetUpdatesBuf: ""})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"get_updates_buf":""`) {
		t.Fatalf("get_updates_buf must always be present, got %s", payload)
	}
}
