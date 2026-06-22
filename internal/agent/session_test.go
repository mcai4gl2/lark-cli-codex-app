package agent

import (
	"path/filepath"
	"testing"
)

func TestSessionStoreLookupEmpty(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	id, err := store.Lookup(SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if id != "" {
		t.Fatalf("expected empty session id, got %q", id)
	}
}

func TestSessionStorePutAndLookup(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	key := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"}

	if err := store.Put(key, "sess-abc"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	id, err := store.Lookup(key)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if id != "sess-abc" {
		t.Fatalf("session id = %q, want sess-abc", id)
	}
}

func TestSessionStorePutUpdates(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	key := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"}

	store.Put(key, "sess-1")
	store.Put(key, "sess-2")

	id, _ := store.Lookup(key)
	if id != "sess-2" {
		t.Fatalf("session id = %q, want sess-2", id)
	}
}

func TestSessionStoreRemove(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	key := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"}

	store.Put(key, "sess-abc")

	removed, err := store.Remove(key)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true")
	}

	id, _ := store.Lookup(key)
	if id != "" {
		t.Fatalf("after remove, session id = %q", id)
	}
}

func TestSessionStoreRemoveNonExistent(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	removed, err := store.Remove(SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed {
		t.Fatal("expected removed=false for non-existent key")
	}
}

func TestSessionStoreMultipleKeys(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "sessions.json"))
	k1 := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"}
	k2 := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "2.0"}
	k3 := SessionKey{Provider: "slack", ChannelID: "C2", ThreadTS: "1.0"}

	store.Put(k1, "s1")
	store.Put(k2, "s2")
	store.Put(k3, "s3")

	for _, tc := range []struct {
		key  SessionKey
		want string
	}{
		{k1, "s1"},
		{k2, "s2"},
		{k3, "s3"},
	} {
		id, _ := store.Lookup(tc.key)
		if id != tc.want {
			t.Fatalf("Lookup(%+v) = %q, want %q", tc.key, id, tc.want)
		}
	}
}

func TestSessionStoreNilDisabled(t *testing.T) {
	var store *SessionStore
	if store.Enabled() {
		t.Fatal("nil store should not be enabled")
	}
	id, err := store.Lookup(SessionKey{})
	if err != nil || id != "" {
		t.Fatalf("nil store Lookup: id=%q err=%v", id, err)
	}
	if err := store.Put(SessionKey{}, "x"); err != nil {
		t.Fatalf("nil store Put: %v", err)
	}
	removed, err := store.Remove(SessionKey{})
	if err != nil || removed {
		t.Fatalf("nil store Remove: removed=%v err=%v", removed, err)
	}
}

func TestSessionStorePersistsToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	key := SessionKey{Provider: "slack", ChannelID: "C1", ThreadTS: "1.0"}

	store1 := NewSessionStore(path)
	store1.Put(key, "persistent-id")

	store2 := NewSessionStore(path)
	id, err := store2.Lookup(key)
	if err != nil {
		t.Fatalf("Lookup from fresh store: %v", err)
	}
	if id != "persistent-id" {
		t.Fatalf("session id = %q, want persistent-id", id)
	}
}
