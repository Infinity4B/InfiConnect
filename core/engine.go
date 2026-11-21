package core

import (
	"context"
	"errors"
)

var (
	ErrAlreadyConnected   = errors.New("a VPN session is already active")
	ErrNotConnected       = errors.New("no active VPN session")
	ErrChallengeNotFound  = errors.New("authentication challenge not found or already resolved")
	ErrChallengeCanceled  = errors.New("authentication challenge canceled")
	ErrChallengeTimeout   = errors.New("authentication challenge timed out")
	ErrInvalidProfile     = errors.New("invalid connection profile")
	ErrEngineClosed       = errors.New("engine is closed")
)

// Engine is the sole boundary between the protocol core (zju-connect) and
// upper layers (app / UI). Both the in-process implementation and the
// subprocess JSON-RPC implementation satisfy this interface.
type Engine interface {
	// Connect starts a VPN session for the given profile. Only one session may
	// be active at a time. Connect returns once the connection sequence has been
	// accepted and started (or synchronously fails validation/busy checks);
	// progress, challenges, and completion are delivered via Subscribe events.
	Connect(ctx context.Context, profile Profile) error

	// Disconnect tears down the active session (including canceling any pending
	// connection, reconnect, or authentication challenge) and transitions back
	// to StateIdle.
	Disconnect(ctx context.Context) error

	// SubmitChallenge answers a pending interactive authentication challenge.
	SubmitChallenge(ctx context.Context, answer ChallengeAnswer) error

	// CancelChallenge cancels a pending interactive authentication challenge.
	CancelChallenge(ctx context.Context, challengeID string) error

	// State returns the current lifecycle state.
	State() State

	// Status returns a full snapshot of the current engine status.
	Status() StatusSnapshot

	// Resources returns the latest snapshot of server-pushed VPN resources.
	Resources() ResourceSnapshot

	// CheckRoute evaluates whether target (host, IP, or host:port) routes via VPN.
	CheckRoute(target string, defaultPort int, network string) RouteCheckResult

	// Subscribe registers a listener channel for engine events. The returned
	// cancel function unsubscribes and closes nothing externally (safe to call
	// multiple times).
	Subscribe() (<-chan Event, func())

	// Close shuts down the engine and releases all resources.
	Close() error
}
