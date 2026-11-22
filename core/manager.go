package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type managedSession struct {
	profileID   string
	profileName string
	engine      Engine
	socksBind   string
	httpBind    string
	unsub       func()
	stop        chan struct{}
	stopOnce    sync.Once
}

func (ms *managedSession) closeStop() {
	ms.stopOnce.Do(func() {
		close(ms.stop)
	})
}

// SessionManager coordinates multiple concurrent VPN sessions (one isolated
// Engine per Profile) with automatic local proxy port conflict resolution and
// unified event aggregation.
type SessionManager struct {
	factory func() Engine

	mu       sync.RWMutex
	closed   bool
	sessions map[string]*managedSession
	order    []string

	subsMu sync.Mutex
	subs   map[uint64]chan Event
	nextID uint64
}

// NewSessionManager constructs a SessionManager that creates a fresh Engine
// via factory for each simultaneous VPN connection.
func NewSessionManager(factory func() Engine) *SessionManager {
	if factory == nil {
		factory = func() Engine { return NewInProcessEngine(InProcessOptions{}) }
	}
	return &SessionManager{
		factory:  factory,
		sessions: make(map[string]*managedSession),
		subs:     make(map[uint64]chan Event),
	}
}

// Subscribe registers a listener for aggregated events from all active sessions.
func (m *SessionManager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	m.subsMu.Lock()
	m.nextID++
	id := m.nextID
	m.subs[id] = ch
	m.subsMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.subsMu.Lock()
			delete(m.subs, id)
			m.subsMu.Unlock()
		})
	}
}

func (m *SessionManager) emit(ev Event) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- ev:
		default:
			if ev.Type == EventTraffic || ev.Type == EventLog {
				continue
			}
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

// Connect starts an independent VPN session for profile. Multiple different
// profiles can be connected simultaneously; connecting an already-active
// profile returns ErrAlreadyConnected. If the profile's configured SOCKS5 or
// HTTP bind address collides with another active session, an available port on
// the same host is automatically assigned for this session.
func (m *SessionManager) Connect(ctx context.Context, profile Profile) error {
	profile.ApplyDefaults()
	if strings.TrimSpace(profile.ID) == "" {
		return fmt.Errorf("%w: profile ID is required", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.ServerAddress) == "" {
		return fmt.Errorf("%w: server address is required", ErrInvalidProfile)
	}
	if profile.Protocol != ProtocolEasyConnect && profile.Protocol != ProtocolATrust {
		return fmt.Errorf("%w: unsupported protocol %q", ErrInvalidProfile, profile.Protocol)
	}

	var stale *managedSession
	var portNotes []string

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrEngineClosed
	}

	if existing, ok := m.sessions[profile.ID]; ok {
		st := existing.engine.Status().State
		if st != StateIdle && st != StateError {
			m.mu.Unlock()
			return ErrAlreadyConnected
		}
		stale = existing
		delete(m.sessions, profile.ID)
		m.removeOrderLocked(profile.ID)
	}

	// Collect ports currently held by other active sessions so simultaneous
	// connections with identical default ports (e.g. 1080/1081) auto-allocate
	// free ports cleanly.
	usedAddrs := m.collectActiveProxyAddrsLocked(profile.ID)

	if profile.SocksBind != "" {
		orig := profile.SocksBind
		resolved, changed, err := allocateAvailableBindAddr(orig, usedAddrs)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("allocate SOCKS5 port: %w", err)
		}
		profile.SocksBind = resolved
		markAddrUsed(usedAddrs, resolved)
		if changed {
			portNotes = append(portNotes, fmt.Sprintf("SOCKS5 port %s is in use by another connection; auto-assigned %s", orig, resolved))
		}
	}

	if profile.HTTPBind != "" {
		orig := profile.HTTPBind
		resolved, changed, err := allocateAvailableBindAddr(orig, usedAddrs)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("allocate HTTP proxy port: %w", err)
		}
		profile.HTTPBind = resolved
		markAddrUsed(usedAddrs, resolved)
		if changed {
			portNotes = append(portNotes, fmt.Sprintf("HTTP proxy port %s is in use by another connection; auto-assigned %s", orig, resolved))
		}
	}

	eng := m.factory()
	evCh, unsub := eng.Subscribe()
	ms := &managedSession{
		profileID:   profile.ID,
		profileName: profile.Name,
		engine:      eng,
		socksBind:   profile.SocksBind,
		httpBind:    profile.HTTPBind,
		unsub:       unsub,
		stop:        make(chan struct{}),
	}
	m.sessions[profile.ID] = ms
	m.order = append(m.order, profile.ID)
	m.mu.Unlock()

	if stale != nil {
		stale.closeStop()
		if stale.unsub != nil {
			stale.unsub()
		}
		_ = stale.engine.Close()
	}

	for _, note := range portNotes {
		m.emit(Event{
			Type:      EventLog,
			ProfileID: profile.ID,
			Log: &LogEntry{
				Timestamp:   time.Now(),
				Level:       LogInfo,
				Source:      "proxy",
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Message:     note,
			},
		})
	}

	go m.forwardSessionEvents(ms, evCh)

	if err := eng.Connect(ctx, profile); err != nil {
		m.mu.Lock()
		if m.sessions[profile.ID] == ms {
			delete(m.sessions, profile.ID)
			m.removeOrderLocked(profile.ID)
		}
		m.mu.Unlock()
		ms.closeStop()
		if ms.unsub != nil {
			ms.unsub()
		}
		_ = eng.Close()
		return err
	}

	return nil
}

// Disconnect terminates the VPN session for profileID. If profileID is empty,
// all active sessions are disconnected.
func (m *SessionManager) Disconnect(ctx context.Context, profileID string) error {
	if profileID == "" {
		return m.DisconnectAll(ctx)
	}

	m.mu.RLock()
	ms, ok := m.sessions[profileID]
	m.mu.RUnlock()
	if !ok {
		return nil
	}

	err := ms.engine.Disconnect(ctx)

	removed := false
	m.mu.Lock()
	if m.sessions[profileID] == ms {
		delete(m.sessions, profileID)
		m.removeOrderLocked(profileID)
		removed = true
	}
	m.mu.Unlock()

	if removed {
		ms.closeStop()
		if ms.unsub != nil {
			ms.unsub()
		}
		_ = ms.engine.Close()
		idleSnap := StatusSnapshot{
			State:       StateIdle,
			ProfileID:   profileID,
			ProfileName: ms.profileName,
		}
		m.emit(Event{
			Type:      EventState,
			ProfileID: profileID,
			Status:    &idleSnap,
		})
	}
	return err
}

// DisconnectAll concurrently disconnects every active session.
func (m *SessionManager) DisconnectAll(ctx context.Context) error {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	m.mu.RUnlock()

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	for _, id := range ids {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			if err := m.Disconnect(ctx, pid); err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return firstErr
}

// SubmitChallenge routes a user challenge answer to whichever session owns answer.ID.
func (m *SessionManager) SubmitChallenge(ctx context.Context, answer ChallengeAnswer) error {
	if answer.ID == "" {
		return ErrChallengeNotFound
	}
	candidates := m.snapshotSessions()
	// First check exact match on active challenge ID.
	for _, ms := range candidates {
		st := ms.engine.Status()
		if st.ActiveChallenge != nil && st.ActiveChallenge.ID == answer.ID {
			return ms.engine.SubmitChallenge(ctx, answer)
		}
	}
	// Fallback: try all non-idle sessions in case status snapshot lagged slightly.
	for _, ms := range candidates {
		err := ms.engine.SubmitChallenge(ctx, answer)
		if !errors.Is(err, ErrChallengeNotFound) {
			return err
		}
	}
	return ErrChallengeNotFound
}

// CancelChallenge routes a challenge cancellation to whichever session owns challengeID.
func (m *SessionManager) CancelChallenge(ctx context.Context, challengeID string) error {
	if challengeID == "" {
		return ErrChallengeNotFound
	}
	candidates := m.snapshotSessions()
	for _, ms := range candidates {
		st := ms.engine.Status()
		if st.ActiveChallenge != nil && st.ActiveChallenge.ID == challengeID {
			return ms.engine.CancelChallenge(ctx, challengeID)
		}
	}
	for _, ms := range candidates {
		err := ms.engine.CancelChallenge(ctx, challengeID)
		if !errors.Is(err, ErrChallengeNotFound) {
			return err
		}
	}
	return ErrChallengeNotFound
}

// Statuses returns the current StatusSnapshot for every managed session in connect order.
func (m *SessionManager) Statuses() []StatusSnapshot {
	sessions := m.snapshotSessions()
	out := make([]StatusSnapshot, 0, len(sessions))
	for _, ms := range sessions {
		st := ms.engine.Status()
		if st.ProfileID == "" {
			st.ProfileID = ms.profileID
		}
		if st.ProfileName == "" {
			st.ProfileName = ms.profileName
		}
		if st.ActiveChallenge != nil {
			if st.ActiveChallenge.ProfileID == "" {
				st.ActiveChallenge.ProfileID = ms.profileID
			}
			if st.ActiveChallenge.ProfileName == "" {
				st.ActiveChallenge.ProfileName = ms.profileName
			}
		}
		st.Traffic.ProfileID = ms.profileID
		out = append(out, st)
	}
	return out
}

// Status returns the StatusSnapshot for profileID. When profileID is empty,
// it returns the primary session (preferring connected, then busy, then error,
// or StateIdle if none).
func (m *SessionManager) Status(profileID string) StatusSnapshot {
	if profileID != "" {
		m.mu.RLock()
		ms, ok := m.sessions[profileID]
		m.mu.RUnlock()
		if !ok {
			return StatusSnapshot{State: StateIdle, ProfileID: profileID}
		}
		st := ms.engine.Status()
		if st.ProfileID == "" {
			st.ProfileID = ms.profileID
		}
		if st.ProfileName == "" {
			st.ProfileName = ms.profileName
		}
		st.Traffic.ProfileID = ms.profileID
		return st
	}

	all := m.Statuses()
	var firstBusy, firstErr *StatusSnapshot
	for i := range all {
		st := &all[i]
		if st.State == StateConnected {
			return *st
		}
		if firstBusy == nil && (st.State == StateConnecting || st.State == StateAuthenticating || st.State == StateReconnecting || st.State == StateDisconnecting) {
			firstBusy = st
		}
		if firstErr == nil && st.State == StateError {
			firstErr = st
		}
	}
	if firstBusy != nil {
		return *firstBusy
	}
	if firstErr != nil {
		return *firstErr
	}
	return StatusSnapshot{State: StateIdle}
}

// Resources returns the ResourceSnapshot for profileID (or the first connected
// session when profileID is empty).
func (m *SessionManager) Resources(profileID string) ResourceSnapshot {
	if profileID != "" {
		m.mu.RLock()
		ms, ok := m.sessions[profileID]
		m.mu.RUnlock()
		if !ok {
			return ResourceSnapshot{
				ProfileID:       profileID,
				IPResources:     []IPResourceItem{},
				DomainResources: []DomainResourceItem{},
				DNSOverrides:    []DNSOverrideItem{},
			}
		}
		res := ms.engine.Resources()
		if res.ProfileID == "" {
			res.ProfileID = ms.profileID
		}
		if res.ProfileName == "" {
			res.ProfileName = ms.profileName
		}
		return res
	}

	for _, ms := range m.snapshotSessions() {
		if ms.engine.Status().State == StateConnected {
			res := ms.engine.Resources()
			if res.ProfileID == "" {
				res.ProfileID = ms.profileID
			}
			if res.ProfileName == "" {
				res.ProfileName = ms.profileName
			}
			return res
		}
	}
	return ResourceSnapshot{
		IPResources:     []IPResourceItem{},
		DomainResources: []DomainResourceItem{},
		DNSOverrides:    []DNSOverrideItem{},
	}
}

// AllResources returns ResourceSnapshots for all currently connected sessions.
func (m *SessionManager) AllResources() []ResourceSnapshot {
	sessions := m.snapshotSessions()
	out := make([]ResourceSnapshot, 0, len(sessions))
	for _, ms := range sessions {
		if ms.engine.Status().State != StateConnected {
			continue
		}
		res := ms.engine.Resources()
		if res.ProfileID == "" {
			res.ProfileID = ms.profileID
		}
		if res.ProfileName == "" {
			res.ProfileName = ms.profileName
		}
		out = append(out, res)
	}
	return out
}

// CheckRoute evaluates whether target routes via VPN.
// If profileID is specified, only that session's resources are checked.
// If profileID is empty, all connected sessions are checked and the first
// matching VPN session is reported in ProfileID/ProfileName.
func (m *SessionManager) CheckRoute(profileID, target string, defaultPort int, network string) RouteCheckResult {
	if profileID != "" {
		res := m.Resources(profileID)
		out := EvaluateRoute(res, target, defaultPort, network)
		if out.ViaVPN {
			out.ProfileID = res.ProfileID
			out.ProfileName = res.ProfileName
		}
		return out
	}

	allRes := m.AllResources()
	for _, res := range allRes {
		out := EvaluateRoute(res, target, defaultPort, network)
		if out.ViaVPN {
			out.ProfileID = res.ProfileID
			out.ProfileName = res.ProfileName
			return out
		}
	}
	return EvaluateRoute(ResourceSnapshot{}, target, defaultPort, network)
}

// Close shuts down the SessionManager and disconnects all active sessions.
func (m *SessionManager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.DisconnectAll(ctx)
}

func (m *SessionManager) snapshotSessions() []*managedSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*managedSession, 0, len(m.order))
	for _, id := range m.order {
		if ms, ok := m.sessions[id]; ok {
			out = append(out, ms)
		}
	}
	return out
}

func (m *SessionManager) removeOrderLocked(profileID string) {
	for i, id := range m.order {
		if id == profileID {
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
}

func (m *SessionManager) forwardSessionEvents(ms *managedSession, evCh <-chan Event) {
	for {
		select {
		case <-ms.stop:
			return
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			ev.ProfileID = ms.profileID
			if ev.Status != nil {
				cp := *ev.Status
				if cp.ProfileID == "" {
					cp.ProfileID = ms.profileID
				}
				if cp.ProfileName == "" {
					cp.ProfileName = ms.profileName
				}
				if cp.ActiveChallenge != nil {
					chCp := *cp.ActiveChallenge
					if chCp.ProfileID == "" {
						chCp.ProfileID = ms.profileID
					}
					if chCp.ProfileName == "" {
						chCp.ProfileName = ms.profileName
					}
					cp.ActiveChallenge = &chCp
				}
				cp.Traffic.ProfileID = ms.profileID
				ev.Status = &cp
			}
			if ev.Challenge != nil {
				cp := *ev.Challenge
				if cp.ProfileID == "" {
					cp.ProfileID = ms.profileID
				}
				if cp.ProfileName == "" {
					cp.ProfileName = ms.profileName
				}
				ev.Challenge = &cp
			}
			if ev.Log != nil {
				cp := *ev.Log
				if cp.ProfileID == "" {
					cp.ProfileID = ms.profileID
				}
				if cp.ProfileName == "" {
					cp.ProfileName = ms.profileName
				}
				ev.Log = &cp
			}
			if ev.Traffic != nil {
				cp := *ev.Traffic
				cp.ProfileID = ms.profileID
				ev.Traffic = &cp
			}
			if ev.Resources != nil {
				cp := *ev.Resources
				if cp.ProfileID == "" {
					cp.ProfileID = ms.profileID
				}
				if cp.ProfileName == "" {
					cp.ProfileName = ms.profileName
				}
				ev.Resources = &cp
			}

			// If a session naturally returns to StateIdle (e.g. auth challenge canceled),
			// detach it from active sessions before emitting so Statuses() is already clean.
			var shouldClose bool
			if ev.Type == EventState && ev.Status != nil && ev.Status.State == StateIdle {
				m.mu.Lock()
				if m.sessions[ms.profileID] == ms {
					delete(m.sessions, ms.profileID)
					m.removeOrderLocked(ms.profileID)
					shouldClose = true
				}
				m.mu.Unlock()
			}

			m.emit(ev)

			if shouldClose {
				go func(target *managedSession) {
					target.closeStop()
					if target.unsub != nil {
						target.unsub()
					}
					_ = target.engine.Close()
				}(ms)
				return
			}
		}
	}
}

func (m *SessionManager) collectActiveProxyAddrsLocked(excludeProfileID string) map[string]struct{} {
	used := make(map[string]struct{})
	for id, ms := range m.sessions {
		if id == excludeProfileID {
			continue
		}
		st := ms.engine.Status()
		if st.State == StateIdle || st.State == StateError {
			continue
		}
		markAddrUsed(used, ms.socksBind)
		markAddrUsed(used, ms.httpBind)
		markAddrUsed(used, st.SocksAddr)
		markAddrUsed(used, st.HTTPAddr)
	}
	return used
}

func canonicalHostPort(addr string) (host string, port int, ok bool) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0, false
	}
	h, pStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, false
	}
	p, err := strconv.Atoi(pStr)
	if err != nil || p < 0 || p > 65535 {
		return "", 0, false
	}
	if h == "" || h == "0.0.0.0" || h == "::" {
		h = "127.0.0.1"
	}
	return h, p, true
}

func markAddrUsed(used map[string]struct{}, addr string) {
	h, p, ok := canonicalHostPort(addr)
	if !ok || p == 0 {
		return
	}
	used[net.JoinHostPort(h, strconv.Itoa(p))] = struct{}{}
}

func isAddrInUse(used map[string]struct{}, addr string) bool {
	h, p, ok := canonicalHostPort(addr)
	if !ok || p == 0 {
		return false
	}
	_, exists := used[net.JoinHostPort(h, strconv.Itoa(p))]
	return exists
}

// allocateAvailableBindAddr returns addr unchanged unless it collides with a
// port already reserved by another active session. On collision it scans upward
// on the same bind host for the next free port.
func allocateAvailableBindAddr(addr string, used map[string]struct{}) (string, bool, error) {
	origHost, pStr, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return addr, false, nil
	}
	port, err := strconv.Atoi(pStr)
	if err != nil || port == 0 {
		return addr, false, nil
	}
	if !isAddrInUse(used, addr) {
		return addr, false, nil
	}

	probeHost := origHost
	if probeHost == "" {
		probeHost = "127.0.0.1"
	}
	const maxScan = 1000
	for offset := 1; offset <= maxScan; offset++ {
		candPort := port + offset
		if candPort > 65535 {
			break
		}
		cand := net.JoinHostPort(origHost, strconv.Itoa(candPort))
		if isAddrInUse(used, cand) {
			continue
		}
		probeAddr := net.JoinHostPort(probeHost, strconv.Itoa(candPort))
		ln, lErr := net.Listen("tcp", probeAddr)
		if lErr == nil {
			_ = ln.Close()
			return cand, true, nil
		}
	}
	return "", false, fmt.Errorf("no free local port found near %s", addr)
}
