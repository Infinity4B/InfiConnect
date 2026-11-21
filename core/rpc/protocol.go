package rpc

import (
	"encoding/json"

	"github.com/inficonnect/inficonnect/core"
)

const (
	MethodConnect         = "connect"
	MethodDisconnect      = "disconnect"
	MethodSubmitChallenge = "submit_challenge"
	MethodCancelChallenge = "cancel_challenge"
	MethodStatus          = "status"
	MethodResources       = "resources"
	MethodCheckRoute      = "check_route"
	MethodShutdown        = "shutdown"
	NotifyEvent           = "event"
)

// Message is a single NDJSON JSON-RPC 2.0 frame exchanged over stdio between
// the parent GUI process and the `--core` worker subprocess.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *uint64         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError carries an error response over JSON-RPC.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return e.Message
}

type ConnectParams struct {
	Profile core.Profile `json:"profile"`
}

type CancelChallengeParams struct {
	ID string `json:"id"`
}

type CheckRouteParams struct {
	Target      string `json:"target"`
	DefaultPort int    `json:"defaultPort"`
	Network     string `json:"network"`
}
