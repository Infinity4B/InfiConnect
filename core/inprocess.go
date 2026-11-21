package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mythologyli/zju-connect/client"
	atrustclient "github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/client/authchallenge"
	easyconnectclient "github.com/mythologyli/zju-connect/client/easyconnect"
	"github.com/mythologyli/zju-connect/dial"
	zclog "github.com/mythologyli/zju-connect/log"
	"github.com/mythologyli/zju-connect/resolve"
	"github.com/mythologyli/zju-connect/service"
	"github.com/mythologyli/zju-connect/stack"
	"github.com/mythologyli/zju-connect/stack/gvisor"
	"github.com/mythologyli/zju-connect/stack/tcptunnel"
	"github.com/mythologyli/zju-connect/stack/tun"
	"github.com/mythologyli/zju-connect/underlay"
	"golang.org/x/crypto/pkcs12"
	"inet.af/netaddr"
)

// ActiveSession abstracts an established VPN tunnel + dialer + resources.
// The default implementation builds a real zju-connect session; tests may
// inject a custom SessionFactory to exercise all challenges and lifecycle paths.
type ActiveSession interface {
	DialTarget() *DialTarget
	Resources() ResourceSnapshot
	KeepAlive(ctx context.Context) error
	// Broken returns a channel closed when the underlying tunnel unexpectedly dies.
	Broken() <-chan error
	Close() error
}

// SessionFactory creates an ActiveSession for profile using the provided
// ChallengeHandler for interactive auth challenges.
type SessionFactory func(ctx context.Context, profile Profile, handler authchallenge.Handler, reuseSession bool, logFn func(LogLevel, string, string)) (ActiveSession, error)

// InProcessOptions configures NewInProcessEngine.
type InProcessOptions struct {
	ChallengeTimeout     time.Duration
	KeepAliveInterval    time.Duration
	ReconnectBaseBackoff time.Duration
	ReconnectMaxBackoff  time.Duration
	CaptureStdLog        bool
	SessionFactory       SessionFactory
}

// InProcessEngine implements Engine directly in the current process using
// github.com/mythologyli/zju-connect.
type InProcessEngine struct {
	opts InProcessOptions

	mu          sync.RWMutex
	closed      bool
	state       State
	profile     Profile
	connectedAt *time.Time
	reconnectN  int
	lastError   string
	resources   ResourceSnapshot

	sessionCancel context.CancelFunc
	sessionDone   chan struct{}
	bridge        *ChallengeBridge

	proxy   *ProxyServer
	target  *SwappableTarget
	counter *TrafficCounter

	subsMu sync.Mutex
	subs   map[uint64]chan Event
	nextID uint64

	logWriter *engineLogWriter
}

// NewInProcessEngine constructs an InProcessEngine.
func NewInProcessEngine(opts InProcessOptions) *InProcessEngine {
	if opts.ChallengeTimeout <= 0 {
		opts.ChallengeTimeout = DefaultChallengeTimeout
	}
	if opts.KeepAliveInterval <= 0 {
		opts.KeepAliveInterval = 60 * time.Second
	}
	if opts.ReconnectBaseBackoff <= 0 {
		opts.ReconnectBaseBackoff = 2 * time.Second
	}
	if opts.ReconnectMaxBackoff <= 0 {
		opts.ReconnectMaxBackoff = 30 * time.Second
	}
	if opts.SessionFactory == nil {
		opts.SessionFactory = buildZJUConnectSession
	}

	e := &InProcessEngine{
		opts:    opts,
		state:   StateIdle,
		target:  &SwappableTarget{},
		counter: NewTrafficCounter(),
		subs:    make(map[uint64]chan Event),
	}
	e.logWriter = &engineLogWriter{engine: e}
	if opts.CaptureStdLog {
		stdlog.SetFlags(0)
		stdlog.SetOutput(e.logWriter)
	}
	return e
}

func (e *InProcessEngine) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 128)
	e.subsMu.Lock()
	e.nextID++
	id := e.nextID
	e.subs[id] = ch
	e.subsMu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			e.subsMu.Lock()
			delete(e.subs, id)
			e.subsMu.Unlock()
		})
	}
}

func (e *InProcessEngine) emit(ev Event) {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default:
			// Drop oldest non-critical traffic/log event if consumer is slow.
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

func (e *InProcessEngine) emitLog(level LogLevel, source, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	e.emit(Event{
		Type: EventLog,
		Log: &LogEntry{
			Timestamp: time.Now(),
			Level:     level,
			Source:    source,
			Message:   msg,
		},
	})
}

func (e *InProcessEngine) State() State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

func (e *InProcessEngine) Resources() ResourceSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.resources
}

func (e *InProcessEngine) CheckRoute(target string, defaultPort int, network string) RouteCheckResult {
	return EvaluateRoute(e.Resources(), target, defaultPort, network)
}

func (e *InProcessEngine) Status() StatusSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.statusLocked()
}

func (e *InProcessEngine) statusLocked() StatusSnapshot {
	var socksAddr, httpAddr string
	if e.proxy != nil {
		socksAddr = e.proxy.SocksAddr()
		httpAddr = e.proxy.HTTPAddr()
	}
	var activeCh *Challenge
	if e.bridge != nil {
		activeCh = e.bridge.Active()
	}
	var connAt *time.Time
	if e.connectedAt != nil {
		t := *e.connectedAt
		connAt = &t
	}
	server := ""
	if e.profile.ServerAddress != "" {
		server = net.JoinHostPort(e.profile.ServerAddress, strconv.Itoa(e.profile.ServerPort))
	}
	return StatusSnapshot{
		State:            e.state,
		ProfileID:        e.profile.ID,
		ProfileName:      e.profile.Name,
		Protocol:         e.profile.Protocol,
		Server:           server,
		VirtualIP:        e.resources.VirtualIP,
		SocksAddr:        socksAddr,
		HTTPAddr:         httpAddr,
		ConnectedAt:      connAt,
		ReconnectAttempt: e.reconnectN,
		LastError:        e.lastError,
		ActiveChallenge:  activeCh,
		Traffic:          e.counter.Snapshot(),
	}
}

func (e *InProcessEngine) setStateLocked(st State, lastErr string) StatusSnapshot {
	e.state = st
	if lastErr != "" {
		e.lastError = lastErr
	} else if st == StateConnected || st == StateConnecting || st == StateIdle {
		e.lastError = ""
	}
	snap := e.statusLocked()
	return snap
}

func (e *InProcessEngine) transition(st State, lastErr string) {
	e.mu.Lock()
	snap := e.setStateLocked(st, lastErr)
	e.mu.Unlock()
	e.emit(Event{Type: EventState, Status: &snap})
	if lastErr != "" {
		e.emit(Event{Type: EventError, Error: lastErr, Status: &snap})
	}
}

func (e *InProcessEngine) Connect(_ context.Context, profile Profile) error {
	profile.ApplyDefaults()
	if strings.TrimSpace(profile.ServerAddress) == "" {
		return fmt.Errorf("%w: server address is required", ErrInvalidProfile)
	}
	if profile.Protocol != ProtocolEasyConnect && profile.Protocol != ProtocolATrust {
		return fmt.Errorf("%w: unsupported protocol %q", ErrInvalidProfile, profile.Protocol)
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return ErrEngineClosed
	}
	if e.state != StateIdle && e.state != StateError {
		e.mu.Unlock()
		return ErrAlreadyConnected
	}

	if profile.DebugLog {
		zclog.EnableDebug()
	} else {
		zclog.DisableDebug()
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	e.profile = profile
	e.connectedAt = nil
	e.reconnectN = 0
	e.resources = ResourceSnapshot{}
	e.counter = NewTrafficCounter()
	e.target = &SwappableTarget{}
	e.sessionCancel = cancel
	e.sessionDone = done

	bridge := NewChallengeBridge(
		e.opts.ChallengeTimeout,
		func() context.Context { return sessionCtx },
		func(ch Challenge) {
			e.mu.Lock()
			if e.state == StateConnecting || e.state == StateReconnecting {
				e.state = StateAuthenticating
			}
			snap := e.statusLocked()
			e.mu.Unlock()
			e.emit(Event{Type: EventState, Status: &snap})
			e.emit(Event{Type: EventChallenge, Challenge: &ch, Status: &snap})
		},
		func(challengeID string) {
			e.mu.Lock()
			if e.state == StateAuthenticating {
				e.state = StateConnecting
			}
			snap := e.statusLocked()
			e.mu.Unlock()
			e.emit(Event{Type: EventChallengeResolved, ChallengeID: challengeID, Status: &snap})
			e.emit(Event{Type: EventState, Status: &snap})
		},
	)
	e.bridge = bridge

	// Start local proxy listeners up front so proxy addresses are known immediately
	// and persist cleanly across reconnect cycles.
	proxySrv, err := StartProxyServer(ProxyConfig{
		SocksBind: profile.SocksBind,
		SocksUser: profile.SocksUser,
		SocksPass: profile.SocksPasswd,
		HTTPBind:  profile.HTTPBind,
	}, e.target, e.counter, e.logWriter)
	if err != nil {
		cancel()
		close(done)
		e.sessionCancel = nil
		e.sessionDone = nil
		e.bridge = nil
		snap := e.setStateLocked(StateError, err.Error())
		e.mu.Unlock()
		e.emit(Event{Type: EventState, Status: &snap})
		e.emit(Event{Type: EventError, Error: err.Error(), Status: &snap})
		return err
	}
	e.proxy = proxySrv

	snap := e.setStateLocked(StateConnecting, "")
	e.mu.Unlock()

	e.emit(Event{Type: EventState, Status: &snap})
	e.emitLog(LogInfo, "core", fmt.Sprintf("Connecting to %s:%d (%s)...", profile.ServerAddress, profile.ServerPort, profile.Protocol))
	if proxySrv.SocksAddr() != "" {
		e.emitLog(LogInfo, "proxy", fmt.Sprintf("SOCKS5 listening on %s", proxySrv.SocksAddr()))
	}
	if proxySrv.HTTPAddr() != "" {
		e.emitLog(LogInfo, "proxy", fmt.Sprintf("HTTP proxy listening on %s", proxySrv.HTTPAddr()))
	}

	go e.runSessionLoop(sessionCtx, done, profile, bridge)
	return nil
}

func (e *InProcessEngine) runSessionLoop(ctx context.Context, done chan struct{}, profile Profile, bridge *ChallengeBridge) {
	defer close(done)
	defer func() {
		bridge.CancelAll()
		e.target.Set(nil)
		e.mu.Lock()
		pxy := e.proxy
		e.proxy = nil
		e.bridge = nil
		e.sessionCancel = nil
		e.sessionDone = nil
		e.connectedAt = nil
		e.mu.Unlock()
		if pxy != nil {
			_ = pxy.Close()
		}
	}()

	attempt := 0
	reuseSession := true

	for {
		if ctx.Err() != nil {
			e.transition(StateIdle, "")
			return
		}

		sess, err := e.opts.SessionFactory(ctx, profile, bridge, reuseSession, e.emitLog)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrChallengeCanceled) {
				e.emitLog(LogInfo, "core", "Connection canceled by user")
				e.transition(StateIdle, "")
				return
			}

			// For aTrust, if resuming cached session data failed, fall back to a fresh login.
			if profile.Protocol == ProtocolATrust && reuseSession && profile.ClientDataPath != "" {
				e.emitLog(LogWarn, "core", fmt.Sprintf("Cached aTrust session failed (%v); retrying full login...", err))
				reuseSession = false
				continue
			}

			e.emitLog(LogError, "core", fmt.Sprintf("Connection failed: %v", err))

			// If initial connection fails before ever connecting, enter error state and stop.
			if attempt == 0 && !profile.AutoReconnect {
				e.transition(StateError, err.Error())
				return
			}
			if attempt == 0 {
				// First connect failure should surface directly to the user rather than infinite-looping on bad credentials.
				e.transition(StateError, err.Error())
				return
			}

			// Reconnect failed — wait with exponential backoff.
			if !e.waitReconnectBackoff(ctx, attempt, err.Error()) {
				e.transition(StateIdle, "")
				return
			}
			attempt++
			continue
		}

		// Session established!
		now := time.Now()
		resSnap := sess.Resources()
		e.mu.Lock()
		e.connectedAt = &now
		e.reconnectN = 0
		e.resources = resSnap
		snap := e.setStateLocked(StateConnected, "")
		e.mu.Unlock()

		// Track consecutive dial errors to detect dead tunnels early.
		var consecutiveDialErr atomic.Int32
		dialErrSignal := make(chan error, 1)
		dt := sess.DialTarget()
		if dt != nil {
			origOnErr := dt.OnDialErr
			dt.OnDialErr = func(dErr error) {
				if origOnErr != nil {
					origOnErr(dErr)
				}
				if dErr == nil || errors.Is(dErr, context.Canceled) || errors.Is(dErr, dial.ErrACLDenied) {
					return
				}
				if consecutiveDialErr.Add(1) >= 5 {
					select {
					case dialErrSignal <- fmt.Errorf("consecutive dial failures: %w", dErr):
					default:
					}
				}
			}
			origDial := dt.Dial
			if origDial != nil {
				dt.Dial = func(dctx context.Context, network, addr string) (net.Conn, error) {
					c, derr := origDial(dctx, network, addr)
					if derr == nil {
						consecutiveDialErr.Store(0)
					}
					return c, derr
				}
			}
			origDialIP := dt.DialIPPort
			if origDialIP != nil {
				dt.DialIPPort = func(dctx context.Context, network, addr string) (net.Conn, error) {
					c, derr := origDialIP(dctx, network, addr)
					if derr == nil {
						consecutiveDialErr.Store(0)
					}
					return c, derr
				}
			}
		}
		e.target.Set(dt)

		e.emit(Event{Type: EventResources, Resources: &resSnap, Status: &snap})
		e.emit(Event{Type: EventState, Status: &snap})
		e.emitLog(LogInfo, "core", fmt.Sprintf("Connected (virtual IP: %s, %d IP rules, %d domain rules)", resSnap.VirtualIP, len(resSnap.IPResources), len(resSnap.DomainResources)))

		// Run keepalive + traffic monitor until disconnect or failure.
		dropErr := e.monitorSession(ctx, sess, profile, dialErrSignal)

		// Tear down current session cleanly while keeping proxy listeners open if reconnecting.
		e.target.Set(nil)
		_ = sess.Close()

		if ctx.Err() != nil {
			e.emitLog(LogInfo, "core", "Disconnected")
			e.transition(StateIdle, "")
			return
		}

		if !profile.AutoReconnect {
			e.emitLog(LogError, "core", fmt.Sprintf("Tunnel dropped: %v", dropErr))
			e.transition(StateError, dropErr.Error())
			return
		}

		attempt = 1
		reuseSession = true
		e.emitLog(LogWarn, "core", fmt.Sprintf("Connection lost (%v); scheduling reconnect...", dropErr))
		if !e.waitReconnectBackoff(ctx, attempt, dropErr.Error()) {
			e.transition(StateIdle, "")
			return
		}
		attempt++
	}
}

func (e *InProcessEngine) monitorSession(ctx context.Context, sess ActiveSession, profile Profile, dialErrSignal <-chan error) error {
	trafficTicker := time.NewTicker(1 * time.Second)
	defer trafficTicker.Stop()

	var kaTicker *time.Ticker
	var kaChan <-chan time.Time
	if !profile.DisableKeepAlive {
		kaTicker = time.NewTicker(e.opts.KeepAliveInterval)
		defer kaTicker.Stop()
		kaChan = kaTicker.C
	}

	broken := sess.Broken()
	kaFailures := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-broken:
			if err == nil {
				err = errors.New("tunnel closed unexpectedly")
			}
			return err
		case err := <-dialErrSignal:
			return err
		case <-trafficTicker.C:
			stats := e.counter.Snapshot()
			e.emit(Event{Type: EventTraffic, Traffic: &stats})
		case <-kaChan:
			kaCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := sess.KeepAlive(kaCtx)
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				kaFailures++
				e.emitLog(LogWarn, "keepalive", fmt.Sprintf("Keepalive failed (%d/3): %v", kaFailures, err))
				if kaFailures >= 3 {
					return fmt.Errorf("keepalive failed 3 consecutive times: %w", err)
				}
			} else {
				kaFailures = 0
			}
		}
	}
}

func (e *InProcessEngine) waitReconnectBackoff(ctx context.Context, attempt int, reason string) bool {
	backoff := e.opts.ReconnectBaseBackoff
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff >= e.opts.ReconnectMaxBackoff {
			backoff = e.opts.ReconnectMaxBackoff
			break
		}
	}

	e.mu.Lock()
	e.reconnectN = attempt
	snap := e.setStateLocked(StateReconnecting, reason)
	e.mu.Unlock()
	e.emit(Event{Type: EventState, Status: &snap})
	e.emitLog(LogInfo, "core", fmt.Sprintf("Reconnecting (attempt #%d) in %s...", attempt, backoff))

	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *InProcessEngine) Disconnect(ctx context.Context) error {
	e.mu.Lock()
	if e.state == StateIdle {
		e.mu.Unlock()
		return nil
	}
	if e.state == StateError {
		e.lastError = ""
		snap := e.setStateLocked(StateIdle, "")
		e.mu.Unlock()
		e.emit(Event{Type: EventState, Status: &snap})
		return nil
	}
	cancel := e.sessionCancel
	done := e.sessionDone
	bridge := e.bridge
	snap := e.setStateLocked(StateDisconnecting, "")
	e.mu.Unlock()

	e.emit(Event{Type: EventState, Status: &snap})
	if bridge != nil {
		bridge.CancelAll()
	}
	if cancel != nil {
		cancel()
	}

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (e *InProcessEngine) SubmitChallenge(_ context.Context, answer ChallengeAnswer) error {
	e.mu.RLock()
	bridge := e.bridge
	e.mu.RUnlock()
	if bridge == nil {
		return ErrChallengeNotFound
	}
	return bridge.Submit(answer)
}

func (e *InProcessEngine) CancelChallenge(_ context.Context, challengeID string) error {
	e.mu.RLock()
	bridge := e.bridge
	e.mu.RUnlock()
	if bridge == nil {
		return ErrChallengeNotFound
	}
	return bridge.Cancel(challengeID)
}

func (e *InProcessEngine) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.Disconnect(ctx)
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

// engineLogWriter captures stdlib log lines (including zju-connect/log) and
// forwards them as structured EventLog messages.
type engineLogWriter struct {
	engine *InProcessEngine
	mu     sync.Mutex
	buf    bytes.Buffer
}

func (w *engineLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf.Write(p)
	var lines []string
	for {
		idx := bytes.IndexByte(w.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf.Next(idx + 1))
		lines = append(lines, strings.TrimSpace(line))
	}
	w.mu.Unlock()

	for _, line := range lines {
		if line == "" {
			continue
		}
		lvl := LogInfo
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "fatal"):
			lvl = LogError
		case strings.Contains(lower, "warn") || strings.Contains(lower, "retry") || strings.Contains(lower, "skip"):
			lvl = LogWarn
		case strings.Contains(lower, "debug") || strings.HasPrefix(line, "Send: wrote") || strings.HasPrefix(line, "Recv: read"):
			lvl = LogDebug
		}
		w.engine.emitLog(lvl, "zju-connect", line)
	}
	return len(p), nil
}

// -----------------------------------------------------------------------------
// Real ZJU-Connect session assembly (following upstream main.go exact order)
// -----------------------------------------------------------------------------

type zjuConnectSession struct {
	vpnClient      client.Client
	underlayDialer *underlay.Dialer
	resolver       *resolve.Resolver
	dialer         *dial.Dialer
	resources      ResourceSnapshot
	keepAliveURL   string
	useRemoteDNS   bool
	broken         chan error
	closeOnce      sync.Once
}

func (s *zjuConnectSession) DialTarget() *DialTarget {
	return &DialTarget{
		Dial:       s.dialer.Dial,
		DialIPPort: s.dialer.DialIPPort,
		Resolve:    s.resolver.Resolve,
	}
}

func (s *zjuConnectSession) Resources() ResourceSnapshot {
	return s.resources
}

func (s *zjuConnectSession) Broken() <-chan error {
	return s.broken
}

func (s *zjuConnectSession) KeepAlive(ctx context.Context) error {
	if s.keepAliveURL != "" {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.DialContext = s.dialer.Dial
		tr.ResponseHeaderTimeout = 10 * time.Second
		hc := &http.Client{Transport: tr}
		defer hc.CloseIdleConnections()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.keepAliveURL, nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32<<10))
		_ = resp.Body.Close()
		return nil
	}

	if !s.useRemoteDNS {
		return nil
	}
	if udpRes, err := s.resolver.RemoteUDPResolver(); err == nil && udpRes != nil {
		if _, err := udpRes.LookupIP(ctx, "ip4", "www.baidu.com"); err == nil {
			return nil
		}
	}
	if tcpRes, err := s.resolver.RemoteTCPResolver(); err == nil && tcpRes != nil {
		if _, err := tcpRes.LookupIP(ctx, "ip4", "www.baidu.com"); err == nil {
			return nil
		}
		return errors.New("remote DNS keepalive probe failed over both UDP and TCP")
	}
	return nil
}

func (s *zjuConnectSession) Close() error {
	s.closeOnce.Do(func() {
		if s.resolver != nil {
			s.resolver.Close()
		}
		if closer, ok := s.vpnClient.(interface{ Close() }); ok {
			closer.Close()
		}
		if s.underlayDialer != nil {
			_ = s.underlayDialer.Close()
		}
	})
	return nil
}

func buildZJUConnectSession(
	ctx context.Context,
	profile Profile,
	handler authchallenge.Handler,
	reuseSession bool,
	logFn func(LogLevel, string, string),
) (ActiveSession, error) {
	// 1. Create underlay dialer.
	underlayDialer, err := underlay.New(underlay.Options{
		InterfaceName:  profile.BindInterface,
		AutoDetect:     profile.AutoDetectInterface,
		LocalDNSServer: profile.LocalDNSServer,
	})
	if err != nil {
		return nil, fmt.Errorf("create underlay dialer: %w", err)
	}

	cleanupOnErr := func(c client.Client) {
		if closer, ok := c.(interface{ Close() }); ok {
			closer.Close()
		}
		_ = underlayDialer.Close()
	}

	// 2. Create protocol client & run Setup.
	var vpnClient client.Client
	tcpTunnelMode := profile.TCPTunnelMode

	switch profile.Protocol {
	case ProtocolEasyConnect:
		tlsCert := tls.Certificate{}
		if profile.CertFile != "" {
			p12Data, err := os.ReadFile(profile.CertFile)
			if err != nil {
				_ = underlayDialer.Close()
				return nil, fmt.Errorf("read certificate file: %w", err)
			}
			key, cert, err := pkcs12.Decode(p12Data, profile.CertPassword)
			if err != nil {
				_ = underlayDialer.Close()
				return nil, fmt.Errorf("decode certificate file: %w", err)
			}
			tlsCert = tls.Certificate{
				Certificate: [][]byte{cert.Raw},
				PrivateKey:  key.(crypto.PrivateKey),
				Leaf:        cert,
			}
		}

		ec := easyconnectclient.NewClient(easyconnectclient.Options{
			Server: net.JoinHostPort(profile.ServerAddress, strconv.Itoa(profile.ServerPort)),
			Auth: easyconnectclient.AuthOptions{
				Username:    profile.Username,
				Password:    profile.Password,
				TOTPSecret:  profile.TOTPSecret,
				Certificate: tlsCert,
			},
			SessionID:     profile.TwfID,
			TestMultiLine: !profile.DisableMultiLine,
			Resources: easyconnectclient.ResourceOptions{
				Fetch:          !profile.DisableServerConfig,
				IncludeDomains: !profile.SkipDomainResource,
			},
			UnderlayDialer:   underlayDialer,
			ChallengeHandler: handler,
		})
		vpnClient = ec

		// Cancel EasyConnect Setup if ctx is canceled.
		stopCancel := context.AfterFunc(ctx, func() { ec.Close() })
		err = ec.Setup()
		stopCancel()
		if err != nil {
			cleanupOnErr(ec)
			return nil, fmt.Errorf("easyconnect setup: %w", err)
		}
		if ec.CanUseTCPTunnel() {
			tcpTunnelMode = true
		}

	case ProtocolATrust:
		var clientData []byte
		if reuseSession && profile.ClientDataPath != "" {
			if data, readErr := os.ReadFile(profile.ClientDataPath); readErr == nil {
				clientData = data
				logFn(LogDebug, "atrust", "Loaded persisted aTrust session data")
			}
		}

		var loginMethod auth.LoginMethod
		if profile.SID == "" || profile.DeviceID == "" {
			loginMethod, err = auth.NewLoginMethod(auth.LoginMethodOptions{
				AuthType:   string(profile.AuthType),
				Username:   profile.Username,
				Password:   profile.Password,
				Phone:      profile.Phone,
				Domain:     profile.LoginDomain,
				CASTicket:  profile.CASTicket,
				OAuth2Code: profile.OAuth2Code,
			})
			if err != nil {
				_ = underlayDialer.Close()
				return nil, fmt.Errorf("configure aTrust login method: %w", err)
			}
		}

		ac := atrustclient.NewClient(atrustclient.ClientOptions{
			Session: atrustclient.SessionOptions{
				Username:     profile.Username,
				SID:          profile.SID,
				DeviceID:     profile.DeviceID,
				MACAddresses: profile.MACAddresses,
				SignKey:      profile.SignKey,
			},
			UnderlayDialer: underlayDialer,
		})
		vpnClient = ac

		var saveFn func([]byte) error
		if profile.ClientDataPath != "" {
			saveFn = func(data []byte) error {
				if mkErr := os.MkdirAll(filepath.Dir(profile.ClientDataPath), 0o700); mkErr != nil {
					return mkErr
				}
				if wErr := os.WriteFile(profile.ClientDataPath, data, 0o600); wErr != nil {
					return wErr
				}
				// If running as an elevated TUN root worker, restore ownership to the GUI user.
				if uidStr, gidStr := os.Getenv("INFICONNECT_OWNER_UID"), os.Getenv("INFICONNECT_OWNER_GID"); uidStr != "" && gidStr != "" {
					if uid, err1 := strconv.Atoi(uidStr); err1 == nil {
						if gid, err2 := strconv.Atoi(gidStr); err2 == nil {
							_ = os.Chown(profile.ClientDataPath, uid, gid)
						}
					}
				}
				return nil
			}
		}

		_, err = ac.SetupContext(ctx, atrustclient.SetupOptions{
			ServerAddress:            profile.ServerAddress,
			ServerPort:               profile.ServerPort,
			LoginMethod:              loginMethod,
			TOTPSecret:               profile.TOTPSecret,
			ClientData:               clientData,
			BestNodesRefreshInterval: time.Duration(profile.UpdateBestNodesInterval) * time.Second,
			SessionRefreshInterval:   time.Duration(profile.SessionRefreshInterval) * time.Second,
			ChallengeHandler:         handler,
			SaveClientData:           saveFn,
		})
		if err != nil {
			cleanupOnErr(ac)
			return nil, fmt.Errorf("atrust setup: %w", err)
		}
		if ac.CanUseTCPTunnel() && profile.TCPTunnelMode {
			tcpTunnelMode = true
		}

	default:
		_ = underlayDialer.Close()
		return nil, fmt.Errorf("unsupported protocol: %s", profile.Protocol)
	}

	// 3. Read server-pushed resources.
	ipResources, _ := vpnClient.IPResources()
	ipSet, _ := vpnClient.IPSet()
	domainResources, _ := vpnClient.DomainResources()
	dnsResource, _ := vpnClient.DNSResource()

	if profile.Protocol == ProtocolEasyConnect {
		if !profile.DisableZJUConfig && strings.Contains(strings.ToLower(profile.ServerAddress), "zju.edu.cn") {
			if domainResources == nil {
				domainResources = make(client.DomainResources)
			}
			domainResources["zju.edu.cn"] = []client.DomainResource{{
				PortMin:  1,
				PortMax:  65535,
				Protocol: "all",
			}}
			ipResources = append([]client.IPResource{{
				IPMin:    net.ParseIP("10.0.0.0"),
				IPMax:    net.ParseIP("10.255.255.255"),
				PortMin:  1,
				PortMax:  65535,
				Protocol: "all",
			}}, ipResources...)
			ipSetBuilder := netaddr.IPSetBuilder{}
			if ipSet != nil {
				ipSetBuilder.AddSet(ipSet)
			}
			ipSetBuilder.AddPrefix(netaddr.MustParseIPPrefix("10.0.0.0/8"))
			ipSet, _ = ipSetBuilder.IPSet()
		}

		for _, customDomain := range profile.CustomProxyDomains {
			d := strings.TrimSpace(customDomain)
			if d == "" {
				continue
			}
			if domainResources == nil {
				domainResources = make(client.DomainResources)
			}
			domainResources[d] = append(domainResources[d], client.DomainResource{
				PortMin:  1,
				PortMax:  65535,
				Protocol: "all",
			})
		}
	}

	// Incorporate user-defined custom TUN routes into ipResources & ipSet so
	// both TUN packet filtering and route installation honor them.
	if profile.TUNMode && len(profile.TUNCustomRoutes) > 0 {
		var ipSetBuilder netaddr.IPSetBuilder
		if ipSet != nil {
			ipSetBuilder.AddSet(ipSet)
		}
		for _, raw := range profile.TUNCustomRoutes {
			pfx, ok := parseIPv4PrefixOrHost(raw)
			if !ok || pfx.Bits() == 0 {
				continue
			}
			ipSetBuilder.AddPrefix(pfx)
			rng := pfx.Range()
			lo := rng.From().IPAddr().IP
			hi := rng.To().IPAddr().IP
			ipResources = append(ipResources, client.IPResource{
				IPMin:    lo,
				IPMax:    hi,
				PortMin:  1,
				PortMax:  65535,
				Protocol: "all",
			})
		}
		ipSet, _ = ipSetBuilder.IPSet()
	}

	// 4. Create network stack (split-TUN, TCP tunnel, or gVisor user-space netstack).
	var vpnStack stack.Stack
	if profile.TUNMode {
		// dnsHijack=false & fakeIP=false keep OS DNS and Loon/Clash Fake-IP (198.18.0.0/15) untouched.
		vpnTUNStack, tunErr := tun.NewStack(vpnClient, false, false, ipResources)
		if tunErr != nil {
			cleanupOnErr(vpnClient)
			return nil, fmt.Errorf("TUN stack setup failed (requires administrator privileges): %w", tunErr)
		}

		// Resolve VPN server IPs so tunnel underlay traffic is excluded from TUN routes.
		var serverIPs []net.IP
		if parsed := net.ParseIP(profile.ServerAddress); parsed != nil {
			serverIPs = append(serverIPs, parsed)
		} else if resolved, rErr := net.LookupIP(profile.ServerAddress); rErr == nil {
			serverIPs = append(serverIPs, resolved...)
		}

		localLANs := CollectLocalLANSubnets()
		safeRoutes := ComputeSafeTUNRoutes(ipSet, profile.TUNCustomRoutes, profile.TUNExcludeRoutes, localLANs, serverIPs)
		addedCount := 0
		for _, pfx := range safeRoutes {
			if rErr := vpnTUNStack.AddRoute(pfx.String()); rErr != nil {
				// If an exact route already exists (e.g. from another proxy exclusion), bisect once and retry.
				if left, right, ok := bisectPrefix(pfx); ok {
					lErr := vpnTUNStack.AddRoute(left.String())
					riErr := vpnTUNStack.AddRoute(right.String())
					if lErr == nil {
						addedCount++
					}
					if riErr == nil {
						addedCount++
					}
					if lErr != nil || riErr != nil {
						logFn(LogWarn, "tun", fmt.Sprintf("Failed to add split route %s: %v", pfx.String(), rErr))
					}
				} else {
					logFn(LogWarn, "tun", fmt.Sprintf("Failed to add route %s: %v", pfx.String(), rErr))
				}
			} else {
				addedCount++
			}
		}
		logFn(LogInfo, "tun", fmt.Sprintf("Split-TUN ready: installed %d internal route(s); default route & DNS left untouched", addedCount))
		vpnStack = vpnTUNStack
	} else if tcpTunnelMode {
		vpnStack, err = tcptunnel.NewStack(vpnClient)
		if err != nil {
			cleanupOnErr(vpnClient)
			return nil, fmt.Errorf("tcptunnel stack setup: %w", err)
		}
	} else {
		vpnStack, err = gvisor.NewStack(vpnClient)
		if err != nil {
			cleanupOnErr(vpnClient)
			return nil, fmt.Errorf("gvisor stack setup: %w", err)
		}
	}

	// 5. Create resolver and local DNS server attached to the stack.
	useRemoteDNS := !profile.DisableRemoteDNS
	remoteDNSServer := profile.RemoteDNSServer
	policyDNSServers, _ := vpnClient.DNSServers()
	if useRemoteDNS && (remoteDNSServer == "" || remoteDNSServer == "auto") {
		remoteDNSServer, err = vpnClient.DNSServer()
		if err != nil {
			useRemoteDNS = false
			remoteDNSServer = "10.10.0.21"
			logFn(LogWarn, "dns", "No DNS server provided by VPN server; disabling remote DNS")
		} else {
			logFn(LogInfo, "dns", fmt.Sprintf("Using remote DNS server %s", remoteDNSServer))
		}
	}
	secondaryDNSServer := profile.SecondaryDNSServer
	if secondaryDNSServer == "" || secondaryDNSServer == "auto" {
		secondaryDNSServer = "114.114.114.114"
		if len(policyDNSServers) > 1 {
			secondaryDNSServer = policyDNSServers[1]
		}
	}

	vpnResolver := resolve.NewResolver(
		vpnStack,
		remoteDNSServer,
		secondaryDNSServer,
		profile.DNSTTL,
		domainResources,
		dnsResource,
		useRemoteDNS,
	)

	for _, cd := range profile.CustomDNS {
		if ipAddr := net.ParseIP(cd.IP); ipAddr != nil {
			vpnResolver.SetPermanentDNS(cd.HostName, ipAddr)
		}
	}

	localResolver := service.NewDnsServer(vpnResolver, []string{remoteDNSServer, secondaryDNSServer})
	vpnStack.SetupResolve(localResolver)
	vpnStack.SetupIPPool(vpnResolver.IPPool)

	// 6. Start network stack in a panic-guarded goroutine.
	broken := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				select {
				case broken <- fmt.Errorf("network stack terminated: %v", r):
				default:
				}
			}
		}()
		vpnStack.Run()
	}()

	// 7. Create unified dialer.
	vpnDialer := dial.NewDialer(vpnStack, vpnResolver, ipResources, profile.ProxyAll, profile.DialDirectProxy)

	// Build immutable ResourceSnapshot for UI & route checker.
	resSnap := buildResourceSnapshot(vpnClient, ipResources, domainResources, dnsResource, policyDNSServers, remoteDNSServer, profile.ProxyAll)

	return &zjuConnectSession{
		vpnClient:      vpnClient,
		underlayDialer: underlayDialer,
		resolver:       vpnResolver,
		dialer:         vpnDialer,
		resources:      resSnap,
		keepAliveURL:   profile.KeepAliveURL,
		useRemoteDNS:   useRemoteDNS,
		broken:         broken,
	}, nil
}

func buildResourceSnapshot(
	vpnClient client.Client,
	ipResources []client.IPResource,
	domainResources client.DomainResources,
	dnsResource map[string][]net.IP,
	policyDNSServers []string,
	remoteDNS string,
	proxyAll bool,
) ResourceSnapshot {
	snap := ResourceSnapshot{
		IPResources:     make([]IPResourceItem, 0, len(ipResources)),
		DomainResources: make([]DomainResourceItem, 0, len(domainResources)),
		DNSOverrides:    make([]DNSOverrideItem, 0, len(dnsResource)),
		ProxyAll:        proxyAll,
		UpdatedAt:       time.Now(),
	}

	if ip, err := vpnClient.IP(); err == nil && ip != nil {
		snap.VirtualIP = ip.String()
	}

	seenDNS := map[string]struct{}{}
	for _, s := range policyDNSServers {
		if s != "" {
			seenDNS[s] = struct{}{}
			snap.DNSServers = append(snap.DNSServers, s)
		}
	}
	if remoteDNS != "" {
		if _, ok := seenDNS[remoteDNS]; !ok {
			snap.DNSServers = append(snap.DNSServers, remoteDNS)
		}
	}

	for _, r := range ipResources {
		minStr := ""
		maxStr := ""
		cidr := ""
		if r.IPMin != nil {
			minStr = r.IPMin.String()
		}
		if r.IPMax != nil {
			maxStr = r.IPMax.String()
		}
		if r.IPMin != nil && r.IPMax != nil {
			cidr = rangeToCIDR(r.IPMin.To4(), r.IPMax.To4())
		}
		snap.IPResources = append(snap.IPResources, IPResourceItem{
			IPMin:    minStr,
			IPMax:    maxStr,
			CIDR:     cidr,
			PortMin:  r.PortMin,
			PortMax:  r.PortMax,
			Protocol: r.Protocol,
		})
	}

	domains := make([]string, 0, len(domainResources))
	for d := range domainResources {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	for _, d := range domains {
		for _, r := range domainResources[d] {
			snap.DomainResources = append(snap.DomainResources, DomainResourceItem{
				Domain:   d,
				PortMin:  r.PortMin,
				PortMax:  r.PortMax,
				Protocol: r.Protocol,
			})
		}
	}

	hosts := make([]string, 0, len(dnsResource))
	for h := range dnsResource {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		ips := make([]string, 0, len(dnsResource[h]))
		for _, ip := range dnsResource[h] {
			ips = append(ips, ip.String())
		}
		snap.DNSOverrides = append(snap.DNSOverrides, DNSOverrideItem{Host: h, IPs: ips})
	}

	return snap
}

func rangeToCIDR(minIP, maxIP net.IP) string {
	if minIP == nil || maxIP == nil {
		return ""
	}
	lo, ok1 := netaddr.FromStdIP(minIP)
	hi, ok2 := netaddr.FromStdIP(maxIP)
	if !ok1 || !ok2 {
		return ""
	}
	rng := netaddr.IPRangeFrom(lo, hi)
	if pfx, ok := rng.Prefix(); ok {
		return pfx.String()
	}
	return ""
}
