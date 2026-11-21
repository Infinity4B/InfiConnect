package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/inficonnect/inficonnect/core"
)

type socketAuthHandshake struct {
	Token string `json:"token"`
}

// RunCoreWorker runs the `--core` subprocess JSON-RPC loop.
// By default it communicates over stdin/stdout (redirecting os.Stdout to os.Stderr).
// When launched with `--rpc-connect <unix-socket> --rpc-token-file <path>` (used by
// elevated TUN mode), it dials the parent GUI's private Unix socket, authenticates
// with the one-time token, and serves JSON-RPC over that connection.
func RunCoreWorker() error {
	sockPath, tokenFile := parseWorkerSocketArgs(os.Args[1:])
	if sockPath != "" {
		return runCoreWorkerOverSocket(sockPath, tokenFile)
	}

	rpcOut := os.Stdout
	os.Stdout = os.Stderr
	return ServeStream(os.Stdin, rpcOut, core.InProcessOptions{
		CaptureStdLog: true,
	})
}

func parseWorkerSocketArgs(args []string) (sockPath, tokenFile string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rpc-connect":
			if i+1 < len(args) {
				sockPath = args[i+1]
				i++
			}
		case "--rpc-token-file":
			if i+1 < len(args) {
				tokenFile = args[i+1]
				i++
			}
		}
	}
	return sockPath, tokenFile
}

func runCoreWorkerOverSocket(sockPath, tokenFile string) error {
	tokBytes, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("read rpc token file: %w", err)
	}
	token := strings.TrimSpace(string(tokBytes))
	if token == "" {
		return fmt.Errorf("empty rpc auth token")
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to parent rpc socket: %w", err)
	}
	defer conn.Close()

	raw, err := json.Marshal(socketAuthHandshake{Token: token})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("send rpc auth handshake: %w", err)
	}

	return ServeStream(conn, conn, core.InProcessOptions{
		CaptureStdLog: true,
	})
}

// ServeStream runs the JSON-RPC server on arbitrary streams (used by both
// RunCoreWorker and unit tests).
func ServeStream(r io.Reader, w io.Writer, opts core.InProcessOptions) error {
	engine := core.NewInProcessEngine(opts)
	defer engine.Close()

	var writeMu sync.Mutex
	writeMsg := func(msg Message) error {
		msg.JSONRPC = "2.0"
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		data = append(data, '\n')
		writeMu.Lock()
		_, err = w.Write(data)
		writeMu.Unlock()
		return err
	}

	evCh, unsub := engine.Subscribe()
	defer unsub()

	evStop := make(chan struct{})
	evDone := make(chan struct{})
	go func() {
		defer close(evDone)
		for {
			select {
			case <-evStop:
				return
			case ev := <-evCh:
				raw, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				_ = writeMsg(Message{
					Method: NotifyEvent,
					Params: raw,
				})
			}
		}
	}()
	defer func() {
		close(evStop)
		<-evDone
	}()

	scanner := bufio.NewScanner(r)
	// Allow large messages (e.g., base64 captcha images or large resource snapshots).
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var wg sync.WaitGroup
	defer wg.Wait()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Message
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.Method == MethodShutdown {
			_ = engine.Disconnect(context.Background())
			if req.ID != nil {
				_ = writeMsg(Message{ID: req.ID, Result: json.RawMessage(`{"ok":true}`)})
			}
			return nil
		}

		wg.Add(1)
		go func(m Message) {
			defer wg.Done()
			res, err := dispatchRPC(engine, m)
			if m.ID == nil {
				return
			}
			reply := Message{ID: m.ID}
			if err != nil {
				reply.Error = &RPCError{Code: -32000, Message: err.Error()}
			} else {
				raw, mErr := json.Marshal(res)
				if mErr != nil {
					reply.Error = &RPCError{Code: -32603, Message: mErr.Error()}
				} else {
					reply.Result = raw
				}
			}
			_ = writeMsg(reply)
		}(req)
	}

	return scanner.Err()
}

func dispatchRPC(engine core.Engine, req Message) (any, error) {
	ctx := context.Background()
	switch req.Method {
	case MethodConnect:
		var p ConnectParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("decode connect params: %w", err)
		}
		if err := engine.Connect(ctx, p.Profile); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case MethodDisconnect:
		if err := engine.Disconnect(ctx); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case MethodSubmitChallenge:
		var ans core.ChallengeAnswer
		if err := json.Unmarshal(req.Params, &ans); err != nil {
			return nil, fmt.Errorf("decode submit_challenge params: %w", err)
		}
		if err := engine.SubmitChallenge(ctx, ans); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case MethodCancelChallenge:
		var p CancelChallengeParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("decode cancel_challenge params: %w", err)
		}
		if err := engine.CancelChallenge(ctx, p.ID); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil

	case MethodStatus:
		return engine.Status(), nil

	case MethodResources:
		return engine.Resources(), nil

	case MethodCheckRoute:
		var p CheckRouteParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, fmt.Errorf("decode check_route params: %w", err)
		}
		return engine.CheckRoute(p.Target, p.DefaultPort, p.Network), nil

	default:
		return nil, fmt.Errorf("unknown RPC method: %s", req.Method)
	}
}
