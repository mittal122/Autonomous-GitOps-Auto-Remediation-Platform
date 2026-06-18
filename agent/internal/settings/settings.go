// Package settings persists user-configurable integration settings (Loki, Alertmanager, …)
// so they survive process restarts without requiring environment variables or config files.
//
// Values are encrypted at rest with AES-256-GCM using a key that is generated automatically
// on first run and never exposed through any API — the operator never sees or sets it,
// keeping the "no manual configuration" property while still protecting secrets (e.g. a Loki
// auth header) stored in the database.
package settings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/autosre/agent/internal/store"
)

const keySize = 32 // AES-256

// EnsureMasterKey returns the encryption key at path, generating and persisting a new
// random 32-byte key (mode 0600) if the file does not already exist.
func EnsureMasterKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != keySize {
			return nil, fmt.Errorf("master key at %q has length %d, want %d (corrupt or foreign file)", path, len(data), keySize)
		}
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read master key %q: %w", path, err)
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create master key dir: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write master key %q: %w", path, err)
	}
	return key, nil
}

// Store persists integration settings as AES-256-GCM-encrypted blobs on top of store.Store.
type Store struct {
	db  store.Store
	gcm cipher.AEAD
}

// New wraps db with encryption using key (must be exactly 32 bytes, see EnsureMasterKey).
func New(db store.Store, key []byte) (*Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm mode: %w", err)
	}
	return &Store{db: db, gcm: gcm}, nil
}

// LokiSettings mirrors ingestor.LokiConfig plus an optional auth header for Loki instances
// that require authentication. Durations are stored as strings (e.g. "30s") for portability.
type LokiSettings struct {
	Addr         string `json:"addr"`
	Query        string `json:"query"`
	PollInterval string `json:"poll_interval"`
	Timeout      string `json:"timeout"`
	AuthHeader   string `json:"auth_header,omitempty"` // e.g. "Bearer xyz"; encrypted at rest like everything else
}

const keyLokiSettings = "loki.settings"

// LoadLokiSettings returns the persisted Loki settings. ok is false when nothing has been saved yet.
func (s *Store) LoadLokiSettings(ctx context.Context) (LokiSettings, bool, error) {
	var out LokiSettings
	ok, err := s.getJSON(ctx, keyLokiSettings, &out)
	if err != nil || !ok {
		return LokiSettings{}, false, err
	}
	return out, true, nil
}

// SaveLokiSettings encrypts and persists settings, overwriting any previous value.
func (s *Store) SaveLokiSettings(ctx context.Context, settings LokiSettings) error {
	return s.putJSON(ctx, keyLokiSettings, settings)
}

// DeleteLokiSettings removes any persisted Loki settings (disables the integration).
func (s *Store) DeleteLokiSettings(ctx context.Context) error {
	return s.db.DeleteSetting(ctx, keyLokiSettings)
}

// ---------------------------------------------------------------------------
// Generic encrypt/decrypt helpers
// ---------------------------------------------------------------------------

func (s *Store) putJSON(ctx context.Context, key string, v any) error {
	plain, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal %q: %w", key, err)
	}
	sealed, err := s.encrypt(plain)
	if err != nil {
		return fmt.Errorf("encrypt %q: %w", key, err)
	}
	return s.db.PutSetting(ctx, key, sealed)
}

// getJSON loads and decrypts key into v. ok is false when the key is absent.
func (s *Store) getJSON(ctx context.Context, key string, v any) (bool, error) {
	sealed, ok, err := s.db.GetSetting(ctx, key)
	if err != nil {
		return false, fmt.Errorf("load %q: %w", key, err)
	}
	if !ok {
		return false, nil
	}
	plain, err := s.decrypt(sealed)
	if err != nil {
		return false, fmt.Errorf("decrypt %q: %w", key, err)
	}
	if err := json.Unmarshal(plain, v); err != nil {
		return false, fmt.Errorf("unmarshal %q: %w", key, err)
	}
	return true, nil
}

func (s *Store) encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return s.gcm.Seal(nonce, nonce, plain, nil), nil
}

func (s *Store) decrypt(sealed []byte) ([]byte, error) {
	nonceSize := s.gcm.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := sealed[:nonceSize], sealed[nonceSize:]
	return s.gcm.Open(nil, nonce, ciphertext, nil)
}
