package app

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	vaultFileName = "secrets.vault"
	vaultKeyName  = ".vault.key"
	vaultMagic    = "ICV1"
	vaultKeySize  = 32
)

// FileSecretStore stores secrets in an AES-256-GCM encrypted file (0600)
// inside the application's private config directory (0700).
//
// Using a local encrypted vault avoids repeated macOS Login Keychain password
// prompts triggered by ad-hoc signed / rebuilt open-source binaries, while
// ensuring plaintext secrets are never written to profiles.json.
type FileSecretStore struct {
	mu        sync.Mutex
	keyPath   string
	vaultPath string
	key       []byte
	data      map[string][]byte
}

// OpenFileSecretStore loads (or prepares) the encrypted secret vault in dir.
func OpenFileSecretStore(dir string) (*FileSecretStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create vault dir: %w", err)
	}
	s := &FileSecretStore{
		keyPath:   filepath.Join(dir, vaultKeyName),
		vaultPath: filepath.Join(dir, vaultFileName),
		data:      make(map[string][]byte),
	}
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *FileSecretStore) loadLocked() error {
	raw, err := os.ReadFile(s.vaultPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read secret vault: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}

	key, err := os.ReadFile(s.keyPath)
	if err != nil {
		return fmt.Errorf("read secret vault key: %w", err)
	}
	if len(key) != vaultKeySize {
		return fmt.Errorf("invalid secret vault key length: %d", len(key))
	}
	s.key = key

	block, err := aes.NewCipher(s.key)
	if err != nil {
		return fmt.Errorf("init vault cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("init vault gcm: %w", err)
	}

	magicLen := len(vaultMagic)
	nonceSize := gcm.NonceSize()
	if len(raw) < magicLen+nonceSize || !bytes.Equal(raw[:magicLen], []byte(vaultMagic)) {
		return errors.New("corrupted secret vault header")
	}
	nonce := raw[magicLen : magicLen+nonceSize]
	ciphertext := raw[magicLen+nonceSize:]

	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(vaultMagic))
	if err != nil {
		return fmt.Errorf("decrypt secret vault: %w", err)
	}

	var decoded map[string][]byte
	if err := json.Unmarshal(plain, &decoded); err != nil {
		return fmt.Errorf("decode secret vault: %w", err)
	}
	if decoded != nil {
		s.data = decoded
	}
	return nil
}

func (s *FileSecretStore) ensureKeyLocked() error {
	if len(s.key) == vaultKeySize {
		return nil
	}
	if existing, err := os.ReadFile(s.keyPath); err == nil && len(existing) == vaultKeySize {
		s.key = existing
		return nil
	}
	key := make([]byte, vaultKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return fmt.Errorf("generate vault key: %w", err)
	}
	if err := atomicWriteFile(s.keyPath, key, 0o600); err != nil {
		return fmt.Errorf("write vault key: %w", err)
	}
	s.key = key
	return nil
}

func (s *FileSecretStore) saveLocked() error {
	if err := s.ensureKeyLocked(); err != nil {
		return err
	}
	plain, err := json.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("encode secret vault: %w", err)
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return fmt.Errorf("init vault cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("init vault gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate vault nonce: %w", err)
	}

	out := make([]byte, 0, len(vaultMagic)+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, vaultMagic...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, []byte(vaultMagic))

	return atomicWriteFile(s.vaultPath, out, 0o600)
}

func (s *FileSecretStore) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = append([]byte(nil), value...)
	return s.saveLocked()
}

func (s *FileSecretStore) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.data[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), v...), nil
}

func (s *FileSecretStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data[key]; !ok {
		return nil
	}
	delete(s.data, key)
	return s.saveLocked()
}

// LocalValues returns all secrets currently held in the local encrypted vault
// (used for log redaction without ever touching the OS keychain).
func (s *FileSecretStore) LocalValues() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.data))
	for _, v := range s.data {
		if len(v) > 0 {
			out = append(out, string(v))
		}
	}
	return out
}

// MigratingSecretStore reads/writes strictly against primary (FileSecretStore)
// and only falls back to legacy (OS keychain) once per missing key to migrate
// pre-existing credentials into primary so the OS keychain is never prompted
// on profile creation, edits, or subsequent connections.
type MigratingSecretStore struct {
	primary SecretStore
	legacy  SecretStore

	mu            sync.Mutex
	legacyChecked map[string]bool
}

// NewMigratingSecretStore creates a secret store backed by primary with
// one-time read migration from legacy.
func NewMigratingSecretStore(primary SecretStore, legacy SecretStore) *MigratingSecretStore {
	return &MigratingSecretStore{
		primary:       primary,
		legacy:        legacy,
		legacyChecked: make(map[string]bool),
	}
}

func (m *MigratingSecretStore) Set(key string, value []byte) error {
	if err := m.primary.Set(key, value); err != nil {
		return err
	}
	m.mu.Lock()
	m.legacyChecked[key] = true
	m.mu.Unlock()
	return nil
}

func (m *MigratingSecretStore) Delete(key string) error {
	if err := m.primary.Delete(key); err != nil {
		return err
	}
	m.mu.Lock()
	m.legacyChecked[key] = true
	m.mu.Unlock()
	return nil
}

func (m *MigratingSecretStore) Get(key string) ([]byte, error) {
	v, err := m.primary.Get(key)
	if err == nil {
		return v, nil
	}
	if m.legacy == nil {
		return nil, err
	}

	m.mu.Lock()
	alreadyChecked := m.legacyChecked[key]
	m.mu.Unlock()
	if alreadyChecked {
		return nil, err
	}

	legacyVal, legacyErr := m.legacy.Get(key)

	m.mu.Lock()
	m.legacyChecked[key] = true
	m.mu.Unlock()

	if legacyErr != nil {
		return nil, legacyErr
	}
	// Migrate into primary so future reads never touch the legacy OS keychain.
	_ = m.primary.Set(key, legacyVal)
	return legacyVal, nil
}

// LocalValues delegates to primary when it supports listing local secrets
// without invoking the OS keychain.
func (m *MigratingSecretStore) LocalValues() []string {
	type localLister interface {
		LocalValues() []string
	}
	if l, ok := m.primary.(localLister); ok {
		return l.LocalValues()
	}
	return nil
}
