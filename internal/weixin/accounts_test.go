package weixin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeAndDeriveAccountID(t *testing.T) {
	cases := map[string]string{
		"b0f5860fdecb@im.bot":    "b0f5860fdecb-im-bot",
		"b0f5860fdecb@im.wechat": "b0f5860fdecb-im-wechat",
		"plain":                  "plain",
		"a/b:c":                  "a-b-c",
	}
	for raw, want := range cases {
		if got := NormalizeAccountID(raw); got != want {
			t.Fatalf("NormalizeAccountID(%q) = %q, want %q", raw, got, want)
		}
	}

	if got, ok := DeriveRawAccountID("b0f5860fdecb-im-bot"); !ok || got != "b0f5860fdecb@im.bot" {
		t.Fatalf("DeriveRawAccountID(bot) = %q, %v", got, ok)
	}
	if got, ok := DeriveRawAccountID("b0f5860fdecb-im-wechat"); !ok || got != "b0f5860fdecb@im.wechat" {
		t.Fatalf("DeriveRawAccountID(wechat) = %q, %v", got, ok)
	}
	if _, ok := DeriveRawAccountID("plain"); ok {
		t.Fatalf("DeriveRawAccountID(plain) should not resolve")
	}
}

func TestStoreSaveLoadAndIndex(t *testing.T) {
	store := NewStore(t.TempDir())

	if err := store.Save("acc-im-bot", Account{Token: "tok", BaseURL: "https://a.test", UserID: "u1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	account, err := store.Load("acc-im-bot")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if account.Token != "tok" || account.BaseURL != "https://a.test" || account.UserID != "u1" {
		t.Fatalf("account = %+v", account)
	}
	if account.SavedAt == "" {
		t.Fatalf("SavedAt was not stamped")
	}

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"acc-im-bot"}) {
		t.Fatalf("List() = %v", ids)
	}

	info, err := os.Stat(store.AccountPath("acc-im-bot"))
	if err != nil {
		t.Fatalf("stat account file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("account file mode = %o, want 600", perm)
	}

	// A partial update must merge rather than clear untouched fields.
	if err := store.Save("acc-im-bot", Account{BaseURL: "https://b.test"}); err != nil {
		t.Fatalf("Save() merge error = %v", err)
	}
	account, _ = store.Load("acc-im-bot")
	if account.Token != "tok" || account.BaseURL != "https://b.test" || account.UserID != "u1" {
		t.Fatalf("merged account = %+v", account)
	}

	// Saving twice must not duplicate the index entry.
	ids, _ = store.List()
	if len(ids) != 1 {
		t.Fatalf("List() = %v, want one entry", ids)
	}
}

func TestStoreLoadFallsBackToRawAccountFilename(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := os.MkdirAll(store.AccountsDir(), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := Account{Token: "legacy", UserID: "u9"}
	payload, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(store.AccountsDir(), "abc@im.bot.json"), payload, 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	account, err := store.Load("abc-im-bot")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if account.Token != "legacy" || account.AccountID != "abc-im-bot" {
		t.Fatalf("account = %+v", account)
	}
}

func TestStoreResolvePrefersNewestAccount(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Resolve(""); err == nil {
		t.Fatalf("Resolve() on an empty store should fail")
	}

	if err := store.Save("first-im-bot", Account{Token: "t1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save("second-im-bot", Account{Token: "t2"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	account, err := store.Resolve("")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if account.AccountID != "second-im-bot" {
		t.Fatalf("Resolve(\"\") = %q, want the newest account", account.AccountID)
	}

	account, err = store.Resolve("first@im.bot")
	if err != nil {
		t.Fatalf("Resolve(raw id) error = %v", err)
	}
	if account.AccountID != "first-im-bot" {
		t.Fatalf("Resolve(raw id) = %q", account.AccountID)
	}
}

func TestStoreTokensNewestFirst(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, id := range []string{"a-im-bot", "b-im-bot", "c-im-bot"} {
		if err := store.Save(id, Account{Token: "token-" + id}); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	tokens := store.Tokens(2)
	if !reflect.DeepEqual(tokens, []string{"token-c-im-bot", "token-b-im-bot"}) {
		t.Fatalf("Tokens(2) = %v", tokens)
	}
}

func TestStoreRemoveDeletesAllAccountFiles(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save("acc-im-bot", Account{Token: "tok"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(store.SyncBufPath("acc-im-bot"), []byte(`{"get_updates_buf":"x"}`), 0o600); err != nil {
		t.Fatalf("write sync buf: %v", err)
	}
	if err := os.WriteFile(store.ContextTokenPath("acc-im-bot"), []byte(`{"u1":"c1"}`), 0o600); err != nil {
		t.Fatalf("write context tokens: %v", err)
	}

	if err := store.Remove("acc-im-bot"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	for _, path := range []string{
		store.AccountPath("acc-im-bot"),
		store.SyncBufPath("acc-im-bot"),
		store.ContextTokenPath("acc-im-bot"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", path)
		}
	}
	ids, _ := store.List()
	if len(ids) != 0 {
		t.Fatalf("List() = %v, want empty", ids)
	}

	// Removing an unknown account is a no-op, not an error.
	if err := store.Remove("missing-im-bot"); err != nil {
		t.Fatalf("Remove(missing) error = %v", err)
	}
}

func TestStoreRemoveStaleForUserID(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Save("old-im-bot", Account{Token: "t1", UserID: "u1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save("other-im-bot", Account{Token: "t2", UserID: "u2"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save("new-im-bot", Account{Token: "t3", UserID: "u1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	removed, err := store.RemoveStaleForUserID("new-im-bot", "u1")
	if err != nil {
		t.Fatalf("RemoveStaleForUserID() error = %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"old-im-bot"}) {
		t.Fatalf("removed = %v", removed)
	}
	ids, _ := store.List()
	if !reflect.DeepEqual(ids, []string{"other-im-bot", "new-im-bot"}) {
		t.Fatalf("List() = %v", ids)
	}
}

func TestSyncStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acc.sync.json")
	store := NewSyncStore(path)

	if got := store.Load(); got != (SyncState{}) {
		t.Fatalf("Load() on a missing file = %+v", got)
	}

	want := SyncState{GetUpdatesBuf: "cursor-1", LastSeq: 42, LastMessageID: 99}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if got := NewSyncStore(path).Load(); got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("sync file mode = %o, want 600", perm)
	}

	// A corrupt cursor file restarts from scratch instead of failing the gateway.
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := store.Load(); got != (SyncState{}) {
		t.Fatalf("Load() on a corrupt file = %+v", got)
	}
}
