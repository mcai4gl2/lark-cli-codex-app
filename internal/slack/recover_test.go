package slack

import (
	"path/filepath"
	"testing"
)

func TestNormalizeRecoverMode(t *testing.T) {
	tests := map[string]RecoverMode{
		"":           RecoverModeThread,
		"thread":     RecoverModeThread,
		"mention-dm": RecoverModeMentionDM,
		"off":        RecoverModeOff,
		"unknown":    RecoverModeThread,
	}
	for input, want := range tests {
		if got := NormalizeRecoverMode(input); got != want {
			t.Fatalf("NormalizeRecoverMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRecoveryStorePersistsThreadState(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}

	if err := store.MarkParticipating(key); err != nil {
		t.Fatalf("MarkParticipating() error = %v", err)
	}
	if err := store.MarkProcessed(key, "111.333"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	reloaded := NewRecoveryStore(store.Path())
	threads, err := reloaded.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("thread count = %d", len(threads))
	}
	if threads[0].Key != key || threads[0].LastProcessedTS != "111.333" {
		t.Fatalf("thread record = %+v", threads[0])
	}
}

func TestRecoveryStoreSkipsAlreadyProcessedMessages(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}
	if err := store.MarkProcessed(key, "111.333"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	if store.ShouldProcess(key, "111.222") {
		t.Fatal("ShouldProcess older message = true")
	}
	if store.ShouldProcess(key, "111.333") {
		t.Fatal("ShouldProcess same message = true")
	}
	if !store.ShouldProcess(key, "111.444") {
		t.Fatal("ShouldProcess newer message = false")
	}
}

func TestRecoveryStoreClaimUpdatesStateOnce(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}

	claimed, err := store.Claim(key, "111.333")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !claimed {
		t.Fatal("Claim() first message = false")
	}

	claimed, err = store.Claim(key, "111.333")
	if err != nil {
		t.Fatalf("Claim() same message error = %v", err)
	}
	if claimed {
		t.Fatal("Claim() same message = true")
	}

	claimed, err = store.Claim(key, "111.222")
	if err != nil {
		t.Fatalf("Claim() older message error = %v", err)
	}
	if claimed {
		t.Fatal("Claim() older message = true")
	}

	threads, err := store.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("thread count = %d", len(threads))
	}
	if threads[0].LastProcessedTS != "111.333" || threads[0].LastSeenAt == "" {
		t.Fatalf("thread record = %+v", threads[0])
	}
}

func TestRecoveryStoreComparesSlackTimestampsNumerically(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	key := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}
	if err := store.MarkProcessed(key, "2.9"); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	if store.ShouldProcess(key, "2.10") {
		t.Fatal("ShouldProcess non-canonical older numeric timestamp = true")
	}
}

func TestRecoveryStoreRemoveThread(t *testing.T) {
	store := NewRecoveryStore(filepath.Join(t.TempDir(), "recover-state.json"))
	keep := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "111.222"}
	remove := RecoveryThreadKey{TeamID: "T123", ChannelID: "C123", ThreadTS: "333.444"}

	if err := store.MarkProcessed(keep, "111.333"); err != nil {
		t.Fatalf("MarkProcessed(keep) error = %v", err)
	}
	if err := store.MarkProcessed(remove, "333.555"); err != nil {
		t.Fatalf("MarkProcessed(remove) error = %v", err)
	}

	removed, err := store.RemoveThread(remove)
	if err != nil {
		t.Fatalf("RemoveThread() error = %v", err)
	}
	if !removed {
		t.Fatal("RemoveThread() removed = false")
	}

	removed, err = store.RemoveThread(remove)
	if err != nil {
		t.Fatalf("RemoveThread() second error = %v", err)
	}
	if removed {
		t.Fatal("RemoveThread() second removed = true")
	}

	threads, err := store.Threads()
	if err != nil {
		t.Fatalf("Threads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].Key != keep {
		t.Fatalf("threads = %+v", threads)
	}
}
