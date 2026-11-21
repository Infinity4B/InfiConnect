package sysproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// ErrUnsupported indicates the current desktop environment does not support
// automatic system proxy configuration (e.g. non-GNOME Linux).
var ErrUnsupported = errors.New("automatic system proxy is not supported on this desktop environment")

// State represents the current system proxy state exposed to the UI.
type State struct {
	Enabled   bool   `json:"enabled"`
	Supported bool   `json:"supported"`
	HTTPAddr  string `json:"httpAddr,omitempty"`
	SocksAddr string `json:"socksAddr,omitempty"`
	Message   string `json:"message,omitempty"`
}

type journalRecord struct {
	HTTPAddr  string          `json:"httpAddr"`
	SocksAddr string          `json:"socksAddr"`
	Backup    json.RawMessage `json:"backup"`
}

// Manager configures and restores OS system proxy settings with crash-recovery.
type Manager struct {
	mu          sync.Mutex
	journalPath string
	enabled     bool
	httpAddr    string
	socksAddr   string
	lastMsg     string
}

// NewManager creates a system proxy manager storing its crash-recovery journal in dataDir.
func NewManager(dataDir string) *Manager {
	return &Manager{
		journalPath: filepath.Join(dataDir, "sysproxy_active.json"),
	}
}

// State returns the current system proxy status.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	supported, msg := platformSupportInfo()
	if m.lastMsg != "" {
		msg = m.lastMsg
	}
	return State{
		Enabled:   m.enabled,
		Supported: supported,
		HTTPAddr:  m.httpAddr,
		SocksAddr: m.socksAddr,
		Message:   msg,
	}
}

// RecoverIfDirty checks whether a previous session crashed while system proxy
// was active and restores the saved OS settings immediately.
func (m *Manager) RecoverIfDirty() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.journalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	var rec journalRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		_ = os.Remove(m.journalPath)
		return false, fmt.Errorf("corrupt sysproxy journal removed: %w", err)
	}

	if err := platformRestore(rec.Backup); err != nil {
		return true, fmt.Errorf("restore system proxy from crash journal: %w", err)
	}
	_ = os.Remove(m.journalPath)
	m.enabled = false
	m.httpAddr = ""
	m.socksAddr = ""
	return true, nil
}

// Enable snapshots the current OS proxy state to disk, then enables HTTP/HTTPS/SOCKS5 proxy.
func (m *Manager) Enable(httpAddr, socksAddr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	httpHost, httpPort, err := splitAddr(httpAddr)
	if err != nil && httpAddr != "" {
		return fmt.Errorf("invalid HTTP proxy address %q: %w", httpAddr, err)
	}
	socksHost, socksPort, err := splitAddr(socksAddr)
	if err != nil && socksAddr != "" {
		return fmt.Errorf("invalid SOCKS5 proxy address %q: %w", socksAddr, err)
	}
	if httpHost == "" && socksHost == "" {
		return errors.New("at least one proxy address is required")
	}

	// Only snapshot original state if we don't already have an active backup journal.
	if _, err := os.Stat(m.journalPath); errors.Is(err, os.ErrNotExist) {
		backup, err := platformSnapshot()
		if err != nil {
			m.lastMsg = err.Error()
			return err
		}
		rec := journalRecord{
			HTTPAddr:  httpAddr,
			SocksAddr: socksAddr,
			Backup:    backup,
		}
		raw, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(m.journalPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(m.journalPath, raw, 0o600); err != nil {
			return err
		}
	}

	if err := platformEnable(httpHost, httpPort, socksHost, socksPort); err != nil {
		m.lastMsg = err.Error()
		// Clean up journal if enabling failed before taking effect.
		if !m.enabled {
			_ = os.Remove(m.journalPath)
		}
		return err
	}

	m.enabled = true
	m.httpAddr = httpAddr
	m.socksAddr = socksAddr
	m.lastMsg = ""
	return nil
}

// Disable restores the OS proxy state captured before Enable and deletes the crash journal.
func (m *Manager) Disable() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.journalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			m.enabled = false
			m.httpAddr = ""
			m.socksAddr = ""
			return nil
		}
		return err
	}

	var rec journalRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		_ = os.Remove(m.journalPath)
		m.enabled = false
		return err
	}

	if err := platformRestore(rec.Backup); err != nil {
		return err
	}
	_ = os.Remove(m.journalPath)
	m.enabled = false
	m.httpAddr = ""
	m.socksAddr = ""
	m.lastMsg = ""
	return nil
}

func splitAddr(addr string) (string, int, error) {
	if addr == "" {
		return "", 0, nil
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}
