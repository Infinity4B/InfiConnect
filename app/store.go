package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/inficonnect/inficonnect/core"
)

const secretServiceName = "dev.inficonnect.app"

// SecretStore abstracts OS credential storage (Keychain / Secret Service / Credential Manager).
type SecretStore interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

type mygoSecretStore struct {
	mod mygo.SecretsModule
}

func newMyGoSecretStore() SecretStore {
	return mygoSecretStore{mod: mygo.Secrets.Service(secretServiceName)}
}

func (s mygoSecretStore) Set(key string, value []byte) error { return s.mod.Set(key, value) }
func (s mygoSecretStore) Get(key string) ([]byte, error)     { return s.mod.Get(key) }
func (s mygoSecretStore) Delete(key string) error            { return s.mod.Delete(key) }

// AppSettings holds global application preferences.
type AppSettings struct {
	DefaultSocksBind    string `json:"defaultSocksBind"`
	DefaultHTTPBind     string `json:"defaultHttpBind"`
	DefaultRemoteDNS    string `json:"defaultRemoteDns"`
	DefaultSecondaryDNS string `json:"defaultSecondaryDns"`
	DefaultProxyAll     bool   `json:"defaultProxyAll"`
	AutoReconnect       bool   `json:"autoReconnect"`
	CloseToTray         bool   `json:"closeToTray"`
	OpenAtLogin         bool   `json:"openAtLogin"`
	Language            string `json:"language"` // "zh-CN" or "en"
	Theme               string `json:"theme"`    // "system", "light", "dark"
	CheckUpdatesOnStart bool   `json:"checkUpdatesOnStart"`
}

func DefaultSettings() AppSettings {
	return AppSettings{
		DefaultSocksBind:    "127.0.0.1:1080",
		DefaultHTTPBind:     "127.0.0.1:1081",
		DefaultRemoteDNS:    "auto",
		DefaultSecondaryDNS: "auto",
		DefaultProxyAll:     false,
		AutoReconnect:       true,
		CloseToTray:         true,
		OpenAtLogin:         false,
		Language:            "zh-CN",
		Theme:               "system",
		CheckUpdatesOnStart: true,
	}
}

type profileDiskFile struct {
	ActiveProfileID string         `json:"activeProfileId"`
	Profiles        []core.Profile `json:"profiles"`
}

// Store persists profiles (metadata only; secrets live in OS keychain),
// settings, and per-profile aTrust session files under user config dir (0700/0600).
type Store struct {
	mu              sync.RWMutex
	dir             string
	secrets         SecretStore
	activeProfileID string
	profiles        []core.Profile
	settings        AppSettings
}

// OpenStore initializes the configuration directory and loads saved profiles & settings.
func OpenStore(dir string, secrets SecretStore) (*Store, error) {
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locate user config dir: %w", err)
		}
		dir = filepath.Join(base, "inficonnect")
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if secrets == nil {
		secrets = newMyGoSecretStore()
	}

	s := &Store{
		dir:      dir,
		secrets:  secrets,
		settings: DefaultSettings(),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) SessionFilePath(profileID string) string {
	safe := strings.ReplaceAll(profileID, string(os.PathSeparator), "_")
	return filepath.Join(s.dir, "sessions", safe+".json")
}

func (s *Store) load() error {
	// Profiles
	if data, err := os.ReadFile(filepath.Join(s.dir, "profiles.json")); err == nil {
		var f profileDiskFile
		if err := json.Unmarshal(data, &f); err == nil {
			s.activeProfileID = f.ActiveProfileID
			s.profiles = f.Profiles
		}
	}
	// Settings
	if data, err := os.ReadFile(filepath.Join(s.dir, "settings.json")); err == nil {
		cfg := DefaultSettings()
		if err := json.Unmarshal(data, &cfg); err == nil {
			s.settings = cfg
		}
	}
	if s.activeProfileID == "" && len(s.profiles) > 0 {
		s.activeProfileID = s.profiles[0].ID
	}
	return nil
}

func (s *Store) saveProfilesLocked() error {
	sanitized := make([]core.Profile, len(s.profiles))
	for i, p := range s.profiles {
		p.Password = ""
		p.TOTPSecret = ""
		p.CertPassword = ""
		p.SocksPasswd = ""
		sanitized[i] = p
	}
	raw, err := json.MarshalIndent(profileDiskFile{
		ActiveProfileID: s.activeProfileID,
		Profiles:        sanitized,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(s.dir, "profiles.json"), raw, 0o600)
}

func (s *Store) saveSettingsLocked() error {
	raw, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(s.dir, "settings.json"), raw, 0o600)
}

// ProfilesListResult is returned to the frontend.
type ProfilesListResult struct {
	ActiveProfileID string         `json:"activeProfileId"`
	Profiles        []core.Profile `json:"profiles"`
}

func (s *Store) ListProfiles() ProfilesListResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Profile, len(s.profiles))
	for i, p := range s.profiles {
		p.Password = ""
		p.TOTPSecret = ""
		p.CertPassword = ""
		p.SocksPasswd = ""
		out[i] = p
	}
	return ProfilesListResult{
		ActiveProfileID: s.activeProfileID,
		Profiles:        out,
	}
}

func (s *Store) ActiveProfileID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeProfileID
}

func (s *Store) SetActiveProfile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, p := range s.profiles {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found && id != "" {
		return fmt.Errorf("profile %q not found", id)
	}
	s.activeProfileID = id
	return s.saveProfilesLocked()
}

// SaveProfileInput carries a profile along with explicit flags for clearing stored secrets.
type SaveProfileInput struct {
	Profile        core.Profile `json:"profile"`
	ClearPassword  bool         `json:"clearPassword,omitempty"`
	ClearTOTP      bool         `json:"clearTotp,omitempty"`
	ClearCertPass  bool         `json:"clearCertPass,omitempty"`
	ClearSocksPass bool         `json:"clearSocksPass,omitempty"`
}

// SaveProfile creates or updates a profile, storing secrets in the OS keychain.
func (s *Store) SaveProfile(in SaveProfileInput) (core.Profile, error) {
	p := in.Profile
	p.Name = strings.TrimSpace(p.Name)
	p.ServerAddress = strings.TrimSpace(p.ServerAddress)
	if p.Name == "" {
		return core.Profile{}, errors.New("profile name is required")
	}
	if p.ServerAddress == "" {
		return core.Profile{}, errors.New("server address is required")
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	existingIdx := -1
	if p.ID != "" {
		for i, existing := range s.profiles {
			if existing.ID == p.ID {
				existingIdx = i
				break
			}
		}
	} else {
		p.ID = newProfileID()
	}

	p.ApplyDefaults()
	p.ClientDataPath = s.SessionFilePath(p.ID)

	if existingIdx >= 0 {
		prev := s.profiles[existingIdx]
		p.CreatedAt = prev.CreatedAt
		p.HasPassword = prev.HasPassword
		p.HasTOTP = prev.HasTOTP
		p.HasCertPass = prev.HasCertPass
		p.HasSocksPass = prev.HasSocksPass
	} else {
		p.CreatedAt = now
	}
	p.UpdatedAt = now

	// Persist secrets in OS credential store (only overwrite when non-empty or explicitly cleared).
	if err := s.updateSecret(p.ID, "password", p.Password, in.ClearPassword, &p.HasPassword); err != nil {
		return core.Profile{}, err
	}
	if err := s.updateSecret(p.ID, "totp", p.TOTPSecret, in.ClearTOTP, &p.HasTOTP); err != nil {
		return core.Profile{}, err
	}
	if err := s.updateSecret(p.ID, "cert_pass", p.CertPassword, in.ClearCertPass, &p.HasCertPass); err != nil {
		return core.Profile{}, err
	}
	if err := s.updateSecret(p.ID, "socks_pass", p.SocksPasswd, in.ClearSocksPass, &p.HasSocksPass); err != nil {
		return core.Profile{}, err
	}

	// Never retain plaintext secrets on the in-memory profile list.
	p.Password = ""
	p.TOTPSecret = ""
	p.CertPassword = ""
	p.SocksPasswd = ""

	if existingIdx >= 0 {
		s.profiles[existingIdx] = p
	} else {
		s.profiles = append(s.profiles, p)
		if s.activeProfileID == "" {
			s.activeProfileID = p.ID
		}
	}

	if err := s.saveProfilesLocked(); err != nil {
		return core.Profile{}, err
	}
	return p, nil
}

func (s *Store) updateSecret(profileID, field, newVal string, clear bool, flag *bool) error {
	key := secretKey(profileID, field)
	if clear {
		if err := s.secrets.Delete(key); err != nil && !errors.Is(err, errors.ErrUnsupported) {
			return fmt.Errorf("delete %s secret: %w", field, err)
		}
		*flag = false
		return nil
	}
	if newVal != "" {
		if err := s.secrets.Set(key, []byte(newVal)); err != nil {
			return fmt.Errorf("store %s secret in system keychain: %w", field, err)
		}
		*flag = true
	}
	return nil
}

// DeleteProfile deletes a profile, its keychain secrets, and its cached aTrust session file.
func (s *Store) DeleteProfile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, p := range s.profiles {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("profile %q not found", id)
	}

	s.profiles = slices.Delete(s.profiles, idx, idx+1)
	if s.activeProfileID == id {
		if len(s.profiles) > 0 {
			s.activeProfileID = s.profiles[0].ID
		} else {
			s.activeProfileID = ""
		}
	}

	for _, field := range []string{"password", "totp", "cert_pass", "socks_pass"} {
		_ = s.secrets.Delete(secretKey(id, field))
	}
	_ = os.Remove(s.SessionFilePath(id))
	return s.saveProfilesLocked()
}

// ClearProfileSession removes persisted aTrust session data for a profile.
func (s *Store) ClearProfileSession(id string) error {
	err := os.Remove(s.SessionFilePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// HydrateProfile loads the profile and retrieves its secrets from the OS keychain
// strictly in memory right before passing it to core.Engine.Connect.
func (s *Store) HydrateProfile(id string) (core.Profile, error) {
	s.mu.RLock()
	var found *core.Profile
	for i := range s.profiles {
		if s.profiles[i].ID == id {
			cp := s.profiles[i]
			found = &cp
			break
		}
	}
	s.mu.RUnlock()

	if found == nil {
		return core.Profile{}, fmt.Errorf("profile %q not found", id)
	}

	p := *found
	p.ClientDataPath = s.SessionFilePath(p.ID)
	if p.HasPassword {
		if v, err := s.secrets.Get(secretKey(p.ID, "password")); err == nil {
			p.Password = string(v)
		}
	}
	if p.HasTOTP {
		if v, err := s.secrets.Get(secretKey(p.ID, "totp")); err == nil {
			p.TOTPSecret = string(v)
		}
	}
	if p.HasCertPass {
		if v, err := s.secrets.Get(secretKey(p.ID, "cert_pass")); err == nil {
			p.CertPassword = string(v)
		}
	}
	if p.HasSocksPass {
		if v, err := s.secrets.Get(secretKey(p.ID, "socks_pass")); err == nil {
			p.SocksPasswd = string(v)
		}
	}
	p.ApplyDefaults()
	return p, nil
}

// SensitiveLiterals collects usernames, phones, and saved secrets across profiles
// so log export redaction scrubs them automatically.
func (s *Store) SensitiveLiterals() []string {
	s.mu.RLock()
	profiles := append([]core.Profile(nil), s.profiles...)
	s.mu.RUnlock()

	var out []string
	for _, p := range profiles {
		if p.Username != "" {
			out = append(out, p.Username)
		}
		if p.Phone != "" {
			out = append(out, p.Phone)
		}
		if p.TwfID != "" {
			out = append(out, p.TwfID)
		}
		if p.SID != "" {
			out = append(out, p.SID)
		}
		if p.SignKey != "" {
			out = append(out, p.SignKey)
		}
		if p.DeviceID != "" {
			out = append(out, p.DeviceID)
		}
	}
	return out
}

func (s *Store) GetSettings() AppSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Store) SaveSettings(cfg AppSettings) (AppSettings, error) {
	if cfg.DefaultSocksBind == "" {
		cfg.DefaultSocksBind = "127.0.0.1:1080"
	}
	if cfg.DefaultHTTPBind == "" {
		cfg.DefaultHTTPBind = "127.0.0.1:1081"
	}
	if cfg.DefaultRemoteDNS == "" {
		cfg.DefaultRemoteDNS = "auto"
	}
	if cfg.DefaultSecondaryDNS == "" {
		cfg.DefaultSecondaryDNS = "auto"
	}
	if cfg.Language == "" {
		cfg.Language = "zh-CN"
	}
	if cfg.Theme == "" {
		cfg.Theme = "system"
	}
	s.mu.Lock()
	s.settings = cfg
	err := s.saveSettingsLocked()
	s.mu.Unlock()
	return cfg, err
}

func secretKey(profileID, field string) string {
	return fmt.Sprintf("profile:%s:%s", profileID, field)
}

func newProfileID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("prof-%d", time.Now().UnixNano())
	}
	return "prof-" + hex.EncodeToString(b[:])
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
