package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inficonnect/inficonnect/core"
)

// SubprocessOptions configures NewSubprocessEngine.
type SubprocessOptions struct {
	// BinaryPath is the executable to spawn with WorkerArgs (defaults to os.Executable()).
	BinaryPath string
	// WorkerArgs defaults to []string{"--core"}.
	WorkerArgs []string
	// ReconnectBaseBackoff controls crash-recovery respawn backoff.
	ReconnectBaseBackoff time.Duration
	ReconnectMaxBackoff  time.Duration
}

type workerProc struct {
	elevated bool
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser

	writeMu sync.Mutex
	pendMu  sync.Mutex
	pending map[uint64]chan Message
	nextID  atomic.Uint64

	exited chan error
}

// SubprocessEngine implements core.Engine by spawning a fresh `--core` worker
// subprocess per VPN session and communicating via NDJSON JSON-RPC over stdio.
//
// Benefits over pure in-process execution:
//   - Guaranteed zero socket/goroutine/global-state leaks on Disconnect (OS reclaims all).
//   - Complete crash isolation if upstream zju-connect calls log.Fatalf or os.Exit(2).
//   - Clean foundation for v2 elevated TUN worker subprocess.
type SubprocessEngine struct {
	opts SubprocessOptions

	mu        sync.RWMutex
	closed    bool
	status    core.StatusSnapshot
	resources core.ResourceSnapshot
	profile   core.Profile

	worker        *workerProc
	stopping      bool
	everConnected bool
	superCancel   context.CancelFunc
	superDone     chan struct{}

	subsMu sync.Mutex
	subs   map[uint64]chan core.Event
	nextID uint64
}

// NewSubprocessEngine creates a Scheme-B subprocess-backed core.Engine.
func NewSubprocessEngine(opts SubprocessOptions) *SubprocessEngine {
	if len(opts.WorkerArgs) == 0 {
		opts.WorkerArgs = []string{"--core"}
	}
	if opts.ReconnectBaseBackoff <= 0 {
		opts.ReconnectBaseBackoff = 2 * time.Second
	}
	if opts.ReconnectMaxBackoff <= 0 {
		opts.ReconnectMaxBackoff = 30 * time.Second
	}
	return &SubprocessEngine{
		opts:   opts,
		status: core.StatusSnapshot{State: core.StateIdle},
		subs:   make(map[uint64]chan core.Event),
	}
}

func (e *SubprocessEngine) Subscribe() (<-chan core.Event, func()) {
	ch := make(chan core.Event, 128)
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

func (e *SubprocessEngine) emit(ev core.Event) {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default:
			if ev.Type == core.EventTraffic || ev.Type == core.EventLog {
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

func (e *SubprocessEngine) emitLog(level core.LogLevel, source, msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	e.emit(core.Event{
		Type: core.EventLog,
		Log: &core.LogEntry{
			Timestamp: time.Now(),
			Level:     level,
			Source:    source,
			Message:   msg,
		},
	})
}

func (e *SubprocessEngine) State() core.State {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status.State
}

func (e *SubprocessEngine) Status() core.StatusSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

func (e *SubprocessEngine) Resources() core.ResourceSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.resources
}

func (e *SubprocessEngine) CheckRoute(target string, defaultPort int, network string) core.RouteCheckResult {
	return core.EvaluateRoute(e.Resources(), target, defaultPort, network)
}

func (e *SubprocessEngine) Connect(ctx context.Context, profile core.Profile) error {
	profile.ApplyDefaults()
	if strings.TrimSpace(profile.ServerAddress) == "" {
		return fmt.Errorf("%w: server address is required", core.ErrInvalidProfile)
	}
	if profile.Protocol != core.ProtocolEasyConnect && profile.Protocol != core.ProtocolATrust {
		return fmt.Errorf("%w: unsupported protocol %q", core.ErrInvalidProfile, profile.Protocol)
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return core.ErrEngineClosed
	}
	if e.status.State != core.StateIdle && e.status.State != core.StateError {
		e.mu.Unlock()
		return core.ErrAlreadyConnected
	}

	// Reap any leftover worker from a prior failed/error connection so we never leak `--core` processes.
	oldWorker := e.worker
	oldSuperCancel := e.superCancel
	oldSuperDone := e.superDone
	e.worker = nil
	e.superCancel = nil
	e.superDone = nil
	e.stopping = true
	e.mu.Unlock()

	if oldSuperCancel != nil {
		oldSuperCancel()
	}
	if oldWorker != nil {
		_ = oldWorker.killGracefully()
	}
	if oldSuperDone != nil {
		<-oldSuperDone
	}

	needElevation := profile.TUNMode && !isElevatedProcess()
	w, err := e.spawnWorker(needElevation)
	if err != nil {
		e.mu.Lock()
		e.stopping = false
		e.status.State = core.StateError
		e.status.LastError = err.Error()
		errSnap := e.status
		e.mu.Unlock()
		e.emit(core.Event{Type: core.EventState, Status: &errSnap})
		e.emit(core.Event{Type: core.EventError, Error: err.Error(), Status: &errSnap})
		return err
	}

	superCtx, superCancel := context.WithCancel(context.Background())
	superDone := make(chan struct{})

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		superCancel()
		close(superDone)
		_ = w.killGracefully()
		return core.ErrEngineClosed
	}

	e.profile = profile
	e.worker = w
	e.stopping = false
	e.everConnected = false
	e.resources = core.ResourceSnapshot{}
	e.superCancel = superCancel
	e.superDone = superDone
	e.status = core.StatusSnapshot{
		State:       core.StateConnecting,
		ProfileID:   profile.ID,
		ProfileName: profile.Name,
		Protocol:    profile.Protocol,
		Server:      fmt.Sprintf("%s:%d", profile.ServerAddress, profile.ServerPort),
	}
	snap := e.status
	e.mu.Unlock()

	e.emit(core.Event{Type: core.EventState, Status: &snap})

	if err := w.call(ctx, MethodConnect, ConnectParams{Profile: profile}, nil); err != nil {
		_ = w.killGracefully()
		superCancel()
		close(superDone)
		e.mu.Lock()
		e.worker = nil
		e.superCancel = nil
		e.superDone = nil
		e.status.State = core.StateError
		e.status.LastError = err.Error()
		errSnap := e.status
		e.mu.Unlock()
		e.emit(core.Event{Type: core.EventState, Status: &errSnap})
		e.emit(core.Event{Type: core.EventError, Error: err.Error(), Status: &errSnap})
		return err
	}

	go e.supervise(superCtx, superDone, w, profile)
	return nil
}

func (e *SubprocessEngine) supervise(ctx context.Context, done chan struct{}, w *workerProc, profile core.Profile) {
	defer close(done)
	cur := w
	crashAttempt := 0

	for {
		select {
		case <-ctx.Done():
			return
		case exitErr := <-cur.exited:
			e.mu.Lock()
			stopping := e.stopping
			wasConnected := e.everConnected
			if e.worker == cur {
				e.worker = nil
			}
			e.mu.Unlock()

			if stopping || ctx.Err() != nil {
				return
			}

			reason := "core worker process exited unexpectedly"
			if exitErr != nil {
				reason = fmt.Sprintf("core worker exited: %v", exitErr)
			}
			e.emitLog(core.LogWarn, "supervisor", reason)

			if !profile.AutoReconnect || !wasConnected || cur.elevated {
				e.mu.Lock()
				e.status.State = core.StateError
				e.status.LastError = reason
				e.status.ActiveChallenge = nil
				snap := e.status
				e.mu.Unlock()
				e.emit(core.Event{Type: core.EventState, Status: &snap})
				e.emit(core.Event{Type: core.EventError, Error: reason, Status: &snap})
				return
			}

			crashAttempt++
			backoff := e.opts.ReconnectBaseBackoff
			for i := 1; i < crashAttempt; i++ {
				backoff *= 2
				if backoff >= e.opts.ReconnectMaxBackoff {
					backoff = e.opts.ReconnectMaxBackoff
					break
				}
			}

			e.mu.Lock()
			e.status.State = core.StateReconnecting
			e.status.ReconnectAttempt = crashAttempt
			e.status.LastError = reason
			e.status.ActiveChallenge = nil
			snap := e.status
			e.mu.Unlock()
			e.emit(core.Event{Type: core.EventState, Status: &snap})
			e.emitLog(core.LogInfo, "supervisor", fmt.Sprintf("Respawning core worker (attempt #%d) in %s...", crashAttempt, backoff))

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			nextW, err := e.spawnWorker(false)
			if err != nil {
				e.emitLog(core.LogError, "supervisor", fmt.Sprintf("Failed to respawn core worker: %v", err))
				continue
			}

			e.mu.Lock()
			if e.stopping || ctx.Err() != nil {
				e.mu.Unlock()
				_ = nextW.killGracefully()
				return
			}
			e.worker = nextW
			e.mu.Unlock()

			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = nextW.call(callCtx, MethodConnect, ConnectParams{Profile: profile}, nil)
			cancel()
			if err != nil {
				e.emitLog(core.LogError, "supervisor", fmt.Sprintf("Respawned worker connect failed: %v", err))
				_ = nextW.killGracefully()
				continue
			}
			cur = nextW
		}
	}
}

func (e *SubprocessEngine) Disconnect(ctx context.Context) error {
	e.mu.Lock()
	if e.status.State == core.StateIdle {
		e.mu.Unlock()
		return nil
	}
	if e.status.State == core.StateError && e.worker == nil {
		e.status = core.StatusSnapshot{State: core.StateIdle}
		snap := e.status
		e.mu.Unlock()
		e.emit(core.Event{Type: core.EventState, Status: &snap})
		return nil
	}

	e.stopping = true
	w := e.worker
	e.worker = nil
	superCancel := e.superCancel
	superDone := e.superDone
	e.superCancel = nil
	e.superDone = nil
	e.status.State = core.StateDisconnecting
	e.status.ActiveChallenge = nil
	discSnap := e.status
	e.mu.Unlock()

	e.emit(core.Event{Type: core.EventState, Status: &discSnap})

	if superCancel != nil {
		superCancel()
	}
	if w != nil {
		_ = w.killGracefully()
	}
	if superDone != nil {
		select {
		case <-superDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	e.mu.Lock()
	e.status = core.StatusSnapshot{State: core.StateIdle}
	e.resources = core.ResourceSnapshot{}
	idleSnap := e.status
	e.mu.Unlock()
	e.emit(core.Event{Type: core.EventState, Status: &idleSnap})
	return nil
}

func (e *SubprocessEngine) SubmitChallenge(ctx context.Context, answer core.ChallengeAnswer) error {
	e.mu.RLock()
	w := e.worker
	e.mu.RUnlock()
	if w == nil {
		return core.ErrChallengeNotFound
	}
	return w.call(ctx, MethodSubmitChallenge, answer, nil)
}

func (e *SubprocessEngine) CancelChallenge(ctx context.Context, challengeID string) error {
	e.mu.RLock()
	w := e.worker
	e.mu.RUnlock()
	if w == nil {
		return core.ErrChallengeNotFound
	}
	return w.call(ctx, MethodCancelChallenge, CancelChallengeParams{ID: challengeID}, nil)
}

func (e *SubprocessEngine) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.Disconnect(ctx)
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

func (e *SubprocessEngine) spawnWorker(elevated bool) (*workerProc, error) {
	bin := e.opts.BinaryPath
	if bin == "" {
		var err error
		bin, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable path: %w", err)
		}
	}

	if elevated {
		return e.spawnElevatedWorker(bin)
	}

	cmd := exec.Command(bin, e.opts.WorkerArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start core worker: %w", err)
	}

	w := &workerProc{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[uint64]chan Message),
		exited:  make(chan error, 1),
	}

	go e.readWorkerStdout(w)
	go e.readWorkerStderr(w)
	go func() {
		err := cmd.Wait()
		w.failPending(errors.New("core worker exited"))
		w.exited <- err
		close(w.exited)
	}()

	return w, nil
}

func (e *SubprocessEngine) readWorkerStdout(w *workerProc) {
	scanner := bufio.NewScanner(w.stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if msg.ID != nil {
			w.pendMu.Lock()
			ch, ok := w.pending[*msg.ID]
			if ok {
				delete(w.pending, *msg.ID)
			}
			w.pendMu.Unlock()
			if ok {
				ch <- msg
			}
			continue
		}

		if msg.Method == NotifyEvent {
			var ev core.Event
			if err := json.Unmarshal(msg.Params, &ev); err != nil {
				continue
			}
			e.handleWorkerEvent(w, ev)
		}
	}
}

func (e *SubprocessEngine) readWorkerStderr(w *workerProc) {
	scanner := bufio.NewScanner(w.stderr)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			e.emitLog(core.LogWarn, "core-stderr", line)
		}
	}
}

func (e *SubprocessEngine) handleWorkerEvent(w *workerProc, ev core.Event) {
	e.mu.Lock()
	if e.worker != w || e.stopping {
		e.mu.Unlock()
		return
	}
	if ev.Status != nil {
		e.status = *ev.Status
		if ev.Status.State == core.StateConnected {
			e.everConnected = true
		}
	}
	if ev.Traffic != nil {
		e.status.Traffic = *ev.Traffic
	}
	if ev.Resources != nil {
		e.resources = *ev.Resources
	}
	e.mu.Unlock()

	e.emit(ev)
}

func (w *workerProc) call(ctx context.Context, method string, params any, out any) error {
	id := w.nextID.Add(1)
	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return err
		}
	}

	replyCh := make(chan Message, 1)
	w.pendMu.Lock()
	w.pending[id] = replyCh
	w.pendMu.Unlock()

	msg := Message{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  rawParams,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		w.deletePending(id)
		return err
	}
	data = append(data, '\n')

	w.writeMu.Lock()
	_, err = w.stdin.Write(data)
	w.writeMu.Unlock()
	if err != nil {
		w.deletePending(id)
		return err
	}

	select {
	case rep := <-replyCh:
		if rep.Error != nil {
			return rep.Error
		}
		if out != nil && len(rep.Result) > 0 {
			return json.Unmarshal(rep.Result, out)
		}
		return nil
	case <-ctx.Done():
		w.deletePending(id)
		return ctx.Err()
	}
}

func (w *workerProc) deletePending(id uint64) {
	w.pendMu.Lock()
	delete(w.pending, id)
	w.pendMu.Unlock()
}

func (w *workerProc) failPending(err error) {
	w.pendMu.Lock()
	defer w.pendMu.Unlock()
	for id, ch := range w.pending {
		select {
		case ch <- Message{ID: &id, Error: &RPCError{Code: -32001, Message: err.Error()}}:
		default:
		}
		delete(w.pending, id)
	}
}

func (w *workerProc) killGracefully() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = w.call(ctx, MethodShutdown, nil, nil)
	cancel()
	_ = w.stdin.Close()

	select {
	case <-w.exited:
		return nil
	case <-time.After(1500 * time.Millisecond):
		if w.cmd != nil && w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		if w.stdout != nil {
			_ = w.stdout.Close()
		}
		<-w.exited
		return nil
	}
}
