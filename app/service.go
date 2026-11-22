package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/inficonnect/inficonnect/app/sysproxy"
	"github.com/inficonnect/inficonnect/core"
	"github.com/inficonnect/inficonnect/core/rpc"
)

const maxLogBuffer = 2000

// AppInfo exposes application metadata to the frontend.
type AppInfo struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	ConfigDir     string `json:"configDir"`
	UpdateEnabled bool   `json:"updateEnabled"`
}

// UpdateCheckResult summarizes an update check against GitHub Releases.
type UpdateCheckResult struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Date      string `json:"date,omitempty"`
	Enabled   bool   `json:"enabled"`
}

// AppService is the sole mygo-bound service exposed to the TypeScript frontend.
// It depends strictly on core.SessionManager / core.Engine and never imports upstream zju-connect.
type AppService struct {
	manager  *core.SessionManager
	store    *Store
	sysProxy *sysproxy.Manager
	extLogin *ExternalLoginManager

	mu            sync.RWMutex
	mainWin       *mygo.Window
	tray          *TrayController
	logs          []core.LogEntry
	pendingUpdate *mygo.Update

	unsub func()
	stop  chan struct{}
}

// NewAppService wires AppService to the core session manager, profile store, and sysproxy manager.
// Arguments may be nil before App.WhenReady (e.g. during `mygo generate`) and
// populated via initRuntime once ready.
func NewAppService(manager *core.SessionManager, store *Store, sp *sysproxy.Manager) *AppService {
	s := &AppService{
		extLogin: NewExternalLoginManager(),
		logs:     make([]core.LogEntry, 0, 256),
		stop:     make(chan struct{}),
	}
	if manager != nil && store != nil && sp != nil {
		s.initRuntime(manager, store, sp)
	}
	return s
}

func (s *AppService) initRuntime(manager *core.SessionManager, store *Store, sp *sysproxy.Manager) {
	s.mu.Lock()
	s.manager = manager
	s.store = store
	s.sysProxy = sp
	s.mu.Unlock()
	if manager != nil {
		evCh, unsub := manager.Subscribe()
		s.unsub = unsub
		go s.forwardEvents(evCh)
	}
}

func (s *AppService) attachUI(win *mygo.Window, tray *TrayController) {
	s.mu.Lock()
	s.mainWin = win
	s.tray = tray
	s.mu.Unlock()
}

func (s *AppService) close() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	if s.unsub != nil {
		s.unsub()
	}
	s.extLogin.Close()
}

func (s *AppService) refreshTray() {
	s.mu.RLock()
	tray := s.tray
	s.mu.RUnlock()
	if tray != nil {
		tray.Refresh()
	}
}

func (s *AppService) appendLog(entry core.LogEntry) {
	s.mu.Lock()
	if len(s.logs) >= maxLogBuffer {
		copy(s.logs, s.logs[len(s.logs)-maxLogBuffer+1:])
		s.logs = s.logs[:maxLogBuffer-1]
	}
	s.logs = append(s.logs, entry)
	s.mu.Unlock()
}

func (s *AppService) forwardEvents(evCh <-chan core.Event) {
	for {
		select {
		case <-s.stop:
			return
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			switch ev.Type {
			case core.EventState:
				if ev.Status != nil {
					// If the session currently providing OS system proxy went idle/errored,
					// automatically restore OS system proxy settings.
					if ev.Status.State == core.StateIdle || ev.Status.State == core.StateError {
						spState := s.sysProxy.State()
						if spState.Enabled && (spState.ProfileID == "" || spState.ProfileID == ev.Status.ProfileID) {
							_ = s.sysProxy.Disable()
							EventSystemProxyChanged.Broadcast(s.sysProxy.State())
						}
					}
					EventStateChanged.Broadcast(*ev.Status)
					EventSessionsChanged.Broadcast(s.manager.Statuses())
					s.refreshTray()
				}
			case core.EventChallenge:
				if ev.Challenge != nil {
					ch := *ev.Challenge
					EventChallengeRequested.Broadcast(ch)
					// For CAS / OAuth2 external login, automatically open the intercepted mygo child window.
					if ch.Type == core.ChallengeTypeExternalLogin && ch.LoginURL != "" {
						go func(c core.Challenge) {
							_ = s.OpenExternalLoginWindow(c.ID, c.LoginURL, c.ExternalKind)
						}(ch)
					}
					s.refreshTray()
				}
			case core.EventChallengeResolved:
				s.extLogin.CloseChallenge(ev.ChallengeID)
				EventChallengeResolved.Broadcast(ev.ChallengeID)
				EventSessionsChanged.Broadcast(s.manager.Statuses())
				s.refreshTray()
			case core.EventLog:
				if ev.Log != nil {
					s.appendLog(*ev.Log)
					EventLogReceived.Broadcast(*ev.Log)
				}
			case core.EventTraffic:
				if ev.Traffic != nil {
					EventTrafficUpdated.Broadcast(*ev.Traffic)
				}
			case core.EventResources:
				if ev.Resources != nil {
					EventResourcesUpdated.Broadcast(*ev.Resources)
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Bound methods callable from TypeScript via generated `src/mygo.ts`
// -----------------------------------------------------------------------------

// GetAppInfo returns application metadata and runtime environment details.
func (s *AppService) GetAppInfo() AppInfo {
	ver := mygo.App.Version()
	if ver == "" {
		ver = "0.1.0-dev"
	}
	return AppInfo{
		Name:          mygo.App.Name(),
		Version:       ver,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		ConfigDir:     s.store.Dir(),
		UpdateEnabled: mygo.Updater.Enabled(),
	}
}

// GetStatus returns the primary VPN connection status snapshot.
func (s *AppService) GetStatus() core.StatusSnapshot {
	return s.manager.Status("")
}

// GetSessions returns status snapshots for all active, connecting, or errored VPN sessions.
func (s *AppService) GetSessions() []core.StatusSnapshot {
	return s.manager.Statuses()
}

// GetResources returns server-pushed resources for profileID (or the primary connected session when empty).
func (s *AppService) GetResources(profileID string) core.ResourceSnapshot {
	return s.manager.Resources(profileID)
}

// GetAllResources returns server-pushed resources for every currently connected session.
func (s *AppService) GetAllResources() []core.ResourceSnapshot {
	return s.manager.AllResources()
}

// CheckRoute evaluates whether target (URL, domain, or IP:port) routes through VPN.
// Pass an empty profileID to check across all connected sessions.
func (s *AppService) CheckRoute(profileID string, target string, defaultPort int, network string) core.RouteCheckResult {
	return s.manager.CheckRoute(profileID, target, defaultPort, network)
}

// ListProfiles returns all saved connection profiles (with secrets stripped) and the active profile ID.
func (s *AppService) ListProfiles() ProfilesListResult {
	return s.store.ListProfiles()
}

// SaveProfile creates or updates a connection profile, storing secrets securely in the OS keychain.
func (s *AppService) SaveProfile(input SaveProfileInput) (core.Profile, error) {
	saved, err := s.store.SaveProfile(input)
	if err != nil {
		return core.Profile{}, err
	}
	EventProfilesChanged.Broadcast(s.store.ListProfiles())
	s.refreshTray()
	return saved, nil
}

// DeleteProfile deletes a connection profile (disconnecting it first if active) and removes its secrets.
func (s *AppService) DeleteProfile(id string) error {
	if s.manager != nil {
		st := s.manager.Status(id)
		if st.State != core.StateIdle {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.Disconnect(ctx, id)
			cancel()
		}
	}
	if err := s.store.DeleteProfile(id); err != nil {
		return err
	}
	EventProfilesChanged.Broadcast(s.store.ListProfiles())
	s.refreshTray()
	return nil
}

// SetActiveProfile sets the active profile used when clicking Connect.
func (s *AppService) SetActiveProfile(id string) error {
	if err := s.store.SetActiveProfile(id); err != nil {
		return err
	}
	EventProfilesChanged.Broadcast(s.store.ListProfiles())
	s.refreshTray()
	return nil
}

// ClearProfileSession deletes cached aTrust session data for a profile to force fresh authentication.
func (s *AppService) ClearProfileSession(id string) error {
	return s.store.ClearProfileSession(id)
}

// ImportTOML parses raw ZJU-Connect TOML configuration text and saves it as a new profile.
func (s *AppService) ImportTOML(tomlText string, profileName string) (core.Profile, error) {
	p, err := core.ImportZJUConnectTOML([]byte(tomlText), profileName)
	if err != nil {
		return core.Profile{}, err
	}
	saved, err := s.store.SaveProfile(SaveProfileInput{Profile: p})
	if err != nil {
		return core.Profile{}, err
	}
	EventProfilesChanged.Broadcast(s.store.ListProfiles())
	s.refreshTray()
	return saved, nil
}

// ImportTOMLFile opens a native file picker to import a ZJU-Connect TOML config file.
func (s *AppService) ImportTOMLFile(ctx context.Context) (*core.Profile, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx),
		Title:  "Import ZJU-Connect TOML Configuration",
		Filters: []mygo.FileFilter{
			{Name: "TOML Configuration", Extensions: []string{"toml"}},
			{Name: "All Files", Extensions: []string{"*"}},
		},
	})
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		return nil, fmt.Errorf("read TOML file: %w", err)
	}
	base := strings.TrimSuffix(filepath.Base(paths[0]), filepath.Ext(paths[0]))
	p, err := s.ImportTOML(string(data), base)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Connect starts a VPN session using profileID (or the active profile when empty).
// Multiple different profiles can be connected simultaneously.
func (s *AppService) Connect(ctx context.Context, profileID string) error {
	if profileID == "" {
		profileID = s.store.ActiveProfileID()
	}
	if profileID == "" {
		return errors.New("no connection profile selected; please create or import a profile first")
	}
	_ = s.store.SetActiveProfile(profileID)
	EventProfilesChanged.Broadcast(s.store.ListProfiles())
	profile, err := s.store.HydrateProfile(profileID)
	if err != nil {
		return err
	}
	err = s.manager.Connect(ctx, profile)
	if err == nil {
		EventSessionsChanged.Broadcast(s.manager.Statuses())
		s.refreshTray()
	}
	return err
}

// Disconnect terminates the VPN session for profileID (or all active sessions when profileID is empty).
func (s *AppService) Disconnect(ctx context.Context, profileID string) error {
	if profileID == "" {
		return s.DisconnectAll(ctx)
	}
	st := s.manager.Status(profileID)
	if st.ActiveChallenge != nil {
		s.extLogin.CloseChallenge(st.ActiveChallenge.ID)
	}
	spState := s.sysProxy.State()
	if spState.Enabled && (spState.ProfileID == "" || spState.ProfileID == profileID) {
		_ = s.sysProxy.Disable()
		EventSystemProxyChanged.Broadcast(s.sysProxy.State())
	}
	err := s.manager.Disconnect(ctx, profileID)
	EventSessionsChanged.Broadcast(s.manager.Statuses())
	s.refreshTray()
	return err
}

// DisconnectAll terminates every active VPN session and restores system proxy settings.
func (s *AppService) DisconnectAll(ctx context.Context) error {
	s.extLogin.Close()
	if s.sysProxy.State().Enabled {
		_ = s.sysProxy.Disable()
		EventSystemProxyChanged.Broadcast(s.sysProxy.State())
	}
	err := s.manager.DisconnectAll(ctx)
	EventSessionsChanged.Broadcast(s.manager.Statuses())
	s.refreshTray()
	return err
}

// SubmitChallenge submits the user's answer for a pending interactive authentication challenge.
func (s *AppService) SubmitChallenge(ctx context.Context, answer core.ChallengeAnswer) error {
	return s.manager.SubmitChallenge(ctx, answer)
}

// CancelChallenge cancels a pending interactive authentication challenge.
func (s *AppService) CancelChallenge(ctx context.Context, challengeID string) error {
	s.extLogin.CloseChallenge(challengeID)
	return s.manager.CancelChallenge(ctx, challengeID)
}

// OpenExternalLoginWindow opens a native WebView window for CAS/OAuth2 login with automatic callback interception.
func (s *AppService) OpenExternalLoginWindow(challengeID string, loginURL string, kind core.ExternalLoginKind) error {
	s.mu.RLock()
	parent := s.mainWin
	s.mu.RUnlock()

	return s.extLogin.Open(parent, challengeID, kind, loginURL, func(callbackURL string) {
		_ = s.manager.SubmitChallenge(context.Background(), core.ChallengeAnswer{
			ID:          challengeID,
			CallbackURL: callbackURL,
		})
	})
}

// OpenExternalURL opens a URL in the system default browser (fallback for external login or links).
func (s *AppService) OpenExternalURL(rawURL string) error {
	return mygo.Shell.OpenExternal(rawURL)
}

// GetSystemProxyState returns the current OS system proxy configuration state.
func (s *AppService) GetSystemProxyState() sysproxy.State {
	return s.sysProxy.State()
}

// SetSystemProxy enables or disables the OS system proxy pointing to profileID's local proxy addresses
// (or the first connected session when profileID is empty).
func (s *AppService) SetSystemProxy(profileID string, enabled bool) (sysproxy.State, error) {
	if !enabled {
		err := s.sysProxy.Disable()
		st := s.sysProxy.State()
		EventSystemProxyChanged.Broadcast(st)
		s.refreshTray()
		return st, err
	}

	var status core.StatusSnapshot
	if profileID != "" {
		status = s.manager.Status(profileID)
	} else {
		// Prefer the active profile if connected, otherwise fall back to any connected session.
		if activeID := s.store.ActiveProfileID(); activeID != "" {
			if st := s.manager.Status(activeID); st.State == core.StateConnected {
				status = st
			}
		}
		if status.State != core.StateConnected {
			status = s.manager.Status("")
		}
	}
	if status.State != core.StateConnected {
		return s.sysProxy.State(), errors.New("connect to VPN before enabling system proxy")
	}

	err := s.sysProxy.EnableForProfile(status.ProfileID, status.ProfileName, status.HTTPAddr, status.SocksAddr)
	st := s.sysProxy.State()
	EventSystemProxyChanged.Broadcast(st)
	s.refreshTray()
	return st, err
}

// GetLogs returns buffered log entries filtered by optional minimum level.
func (s *AppService) GetLogs(minLevel string, limit int) []core.LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	minRank := logLevelRank(core.LogLevel(strings.ToLower(minLevel)))
	var filtered []core.LogEntry
	for _, e := range s.logs {
		if logLevelRank(e.Level) >= minRank {
			filtered = append(filtered, e)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	if filtered == nil {
		return []core.LogEntry{}
	}
	return filtered
}

// ClearLogs empties the in-memory log buffer.
func (s *AppService) ClearLogs() {
	s.mu.Lock()
	s.logs = s.logs[:0]
	s.mu.Unlock()
}

// ExportSanitizedLogs saves redacted logs (scrubbing usernames, tokens, SIDs, cookies, secrets) to a file chosen by the user.
func (s *AppService) ExportSanitizedLogs(ctx context.Context) (string, error) {
	defaultName := fmt.Sprintf("inficonnect-logs-%s.txt", time.Now().Format("20060102-150405"))
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       "Export Sanitized Logs",
		DefaultPath: defaultName,
		Filters: []mygo.FileFilter{
			{Name: "Log Text", Extensions: []string{"txt", "log"}},
		},
	})
	if err != nil || path == "" {
		return "", err
	}

	s.mu.RLock()
	entries := append([]core.LogEntry(nil), s.logs...)
	s.mu.RUnlock()

	header := fmt.Sprintf("# InfiConnect Sanitized Diagnostic Logs\n# Version: %s | OS: %s/%s | Exported: %s\n# Sensitive values (usernames, passwords, tokens, cookies, session IDs) have been automatically redacted.\n\n",
		mygo.App.Version(), runtime.GOOS, runtime.GOARCH, time.Now().Format(time.RFC3339))
	body := core.SanitizeLogs(entries, s.store.SensitiveLiterals()...)

	if err := os.WriteFile(path, []byte(header+body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// GetSettings returns the current global application settings.
func (s *AppService) GetSettings() AppSettings {
	cfg := s.store.GetSettings()
	cfg.OpenAtLogin = mygo.App.OpenAtLogin()
	return cfg
}

// SaveSettings persists application settings and applies theme / launch-at-login immediately.
func (s *AppService) SaveSettings(cfg AppSettings) (AppSettings, error) {
	prev := s.store.GetSettings()
	if cfg.OpenAtLogin != prev.OpenAtLogin {
		if err := mygo.App.SetOpenAtLogin(cfg.OpenAtLogin); err != nil {
			// Preserve saved preference error only if OS call fails on non-unsupported platforms.
			if !errors.Is(err, errors.ErrUnsupported) {
				return prev, fmt.Errorf("update launch at login: %w", err)
			}
		}
	}
	applyTheme(cfg.Theme)
	return s.store.SaveSettings(cfg)
}

// CheckForUpdates checks for signed application updates via mygo.Updater.
func (s *AppService) CheckForUpdates(ctx context.Context) (UpdateCheckResult, error) {
	if !mygo.Updater.Enabled() {
		return UpdateCheckResult{Enabled: false}, nil
	}
	up, err := mygo.Updater.Check(ctx)
	if err != nil {
		return UpdateCheckResult{Enabled: true}, err
	}
	s.mu.Lock()
	s.pendingUpdate = up
	s.mu.Unlock()
	if up == nil {
		return UpdateCheckResult{Enabled: true, Available: false}, nil
	}
	return UpdateCheckResult{
		Enabled:   true,
		Available: true,
		Version:   up.Version,
		Notes:     up.Notes,
		Date:      up.Date.Format(time.RFC3339),
	}, nil
}

// InstallUpdate downloads, verifies, installs the pending update, and relaunches the app.
func (s *AppService) InstallUpdate(ctx context.Context) error {
	s.mu.RLock()
	up := s.pendingUpdate
	s.mu.RUnlock()
	if up == nil {
		return errors.New("no pending update available")
	}
	if err := up.Install(ctx, nil); err != nil {
		return err
	}
	mygo.App.Relaunch()
	return nil
}

// CopyText copies text to the system clipboard.
func (s *AppService) CopyText(text string) error {
	mygo.Clipboard.WriteText(text)
	return nil
}

// GetTUNHelperStatus reports whether the macOS privileged Split-TUN helper daemon is installed and running.
func (s *AppService) GetTUNHelperStatus() rpc.HelperStatus {
	return rpc.GetHelperStatus()
}

// InstallTUNHelper installs or updates the macOS privileged Split-TUN helper daemon (one-time admin prompt).
func (s *AppService) InstallTUNHelper() (rpc.HelperStatus, error) {
	if err := rpc.InstallPrivilegedHelper(""); err != nil {
		return rpc.GetHelperStatus(), err
	}
	return rpc.GetHelperStatus(), nil
}

// UninstallTUNHelper removes the macOS privileged Split-TUN helper daemon.
func (s *AppService) UninstallTUNHelper() (rpc.HelperStatus, error) {
	if err := rpc.UninstallPrivilegedHelper(); err != nil {
		return rpc.GetHelperStatus(), err
	}
	return rpc.GetHelperStatus(), nil
}

func applyTheme(theme string) {
	switch theme {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

func logLevelRank(lvl core.LogLevel) int {
	switch lvl {
	case core.LogDebug:
		return 0
	case core.LogInfo:
		return 1
	case core.LogWarn:
		return 2
	case core.LogError:
		return 3
	default:
		return 1
	}
}
