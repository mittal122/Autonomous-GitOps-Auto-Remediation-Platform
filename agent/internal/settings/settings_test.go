package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autosre/agent/internal/settings"
	"github.com/autosre/agent/internal/store"
)

func openTestStore(t *testing.T) store.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_journal_mode=WAL"
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnsureMasterKey_GeneratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")

	key1, err := settings.EnsureMasterKey(path)
	if err != nil {
		t.Fatalf("EnsureMasterKey (create) failed: %v", err)
	}
	if len(key1) != 32 {
		t.Fatalf("expected 32-byte key, got %d bytes", len(key1))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("key file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected mode 0600, got %o", perm)
	}

	key2, err := settings.EnsureMasterKey(path)
	if err != nil {
		t.Fatalf("EnsureMasterKey (reload) failed: %v", err)
	}
	if string(key1) != string(key2) {
		t.Error("expected EnsureMasterKey to return the same key on a second call")
	}
}

func TestEnsureMasterKey_RejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("setup write failed: %v", err)
	}
	if _, err := settings.EnsureMasterKey(path); err == nil {
		t.Error("expected error for a key file of the wrong length")
	}
}

func TestLokiSettings_RoundTrip(t *testing.T) {
	db := openTestStore(t)
	key, err := settings.EnsureMasterKey(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("EnsureMasterKey failed: %v", err)
	}
	s, err := settings.New(db, key)
	if err != nil {
		t.Fatalf("settings.New failed: %v", err)
	}

	ctx := context.Background()

	if _, ok, err := s.LoadLokiSettings(ctx); err != nil || ok {
		t.Fatalf("expected no settings before save, got ok=%v err=%v", ok, err)
	}

	want := settings.LokiSettings{
		Addr:         "http://loki.example.com:3100",
		Query:        `{namespace="staging"}`,
		PollInterval: "30s",
		Timeout:      "10s",
		AuthHeader:   "Bearer secret-token",
	}
	if err := s.SaveLokiSettings(ctx, want); err != nil {
		t.Fatalf("SaveLokiSettings failed: %v", err)
	}

	got, ok, err := s.LoadLokiSettings(ctx)
	if err != nil {
		t.Fatalf("LoadLokiSettings failed: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after save")
	}
	if got != want {
		t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, want)
	}

	// The underlying stored bytes must not contain the plaintext secret —
	// proves the value is actually encrypted, not just JSON-marshaled.
	raw, ok, err := db.GetSetting(ctx, "loki.settings")
	if err != nil || !ok {
		t.Fatalf("GetSetting failed: ok=%v err=%v", ok, err)
	}
	if strings.Contains(string(raw), want.AuthHeader) {
		t.Error("stored bytes contain the plaintext auth header — encryption is not effective")
	}

	if err := s.DeleteLokiSettings(ctx); err != nil {
		t.Fatalf("DeleteLokiSettings failed: %v", err)
	}
	if _, ok, err := s.LoadLokiSettings(ctx); err != nil || ok {
		t.Fatalf("expected no settings after delete, got ok=%v err=%v", ok, err)
	}
}

func TestLokiSettings_WrongKeyFailsToDecrypt(t *testing.T) {
	db := openTestStore(t)
	key1, _ := settings.EnsureMasterKey(filepath.Join(t.TempDir(), "key1"))
	key2, _ := settings.EnsureMasterKey(filepath.Join(t.TempDir(), "key2"))

	s1, err := settings.New(db, key1)
	if err != nil {
		t.Fatalf("settings.New(key1) failed: %v", err)
	}
	if err := s1.SaveLokiSettings(context.Background(), settings.LokiSettings{Addr: "http://loki:3100"}); err != nil {
		t.Fatalf("SaveLokiSettings failed: %v", err)
	}

	s2, err := settings.New(db, key2)
	if err != nil {
		t.Fatalf("settings.New(key2) failed: %v", err)
	}
	if _, _, err := s2.LoadLokiSettings(context.Background()); err == nil {
		t.Error("expected decryption to fail when using the wrong key")
	}
}
