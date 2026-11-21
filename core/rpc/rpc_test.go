package rpc_test

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/inficonnect/inficonnect/core"
	"github.com/inficonnect/inficonnect/core/rpc"
	"github.com/mythologyli/zju-connect/client/authchallenge"
)

type stubSession struct {
	broken chan error
}

func (s *stubSession) DialTarget() *core.DialTarget { return &core.DialTarget{} }
func (s *stubSession) Resources() core.ResourceSnapshot {
	return core.ResourceSnapshot{VirtualIP: "10.0.0.2"}
}
func (s *stubSession) KeepAlive(context.Context) error { return nil }
func (s *stubSession) Broken() <-chan error            { return s.broken }
func (s *stubSession) Close() error                    { return nil }

func TestServeStream_RoundTripAndChallenge(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()

	serverDone := make(chan error, 1)
	go func() {
		defer s2cW.Close()
		serverDone <- rpc.ServeStream(c2sR, s2cW, core.InProcessOptions{
			SessionFactory: func(ctx context.Context, profile core.Profile, handler authchallenge.Handler, reuseSession bool, logFn func(core.LogLevel, string, string)) (core.ActiveSession, error) {
				resp, err := handler.HandleCodeChallenge(authchallenge.CodeChallenge{Kind: authchallenge.CodeTOTP})
				if err != nil {
					return nil, err
				}
				if resp.Code != "999888" {
					t.Errorf("unexpected totp code: %s", resp.Code)
				}
				return &stubSession{broken: make(chan error, 1)}, nil
			},
		})
	}()

	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(s2cR)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	writeReq := func(raw string) {
		t.Helper()
		if _, err := io.WriteString(c2sW, raw+"\n"); err != nil {
			t.Fatalf("write req: %v", err)
		}
	}

	writeReq(`{"jsonrpc":"2.0","id":1,"method":"connect","params":{"profile":{"id":"p1","name":"RPC Test","protocol":"easyconnect","serverAddress":"vpn.example.edu","serverPort":443,"socksBind":"127.0.0.1:0","httpBind":"127.0.0.1:0"}}}`)

	gotChallenge := false
	gotConnected := false
	deadline := time.After(5 * time.Second)
	for !(gotChallenge && gotConnected) {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("server closed stdout unexpectedly")
			}
			if !gotChallenge && strings.Contains(line, `"type":"challenge"`) {
				gotChallenge = true
				id := extractChallengeID(line)
				writeReq(`{"jsonrpc":"2.0","id":2,"method":"submit_challenge","params":{"id":"` + id + `","code":"999888"}}`)
			}
			if strings.Contains(line, `"state":"connected"`) {
				gotConnected = true
			}
		case <-deadline:
			t.Fatalf("timed out: challenge=%v connected=%v", gotChallenge, gotConnected)
		}
	}

	// Keep draining lines while shutting down so io.Pipe never blocks.
	go func() {
		for range lines {
		}
	}()

	writeReq(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`)
	_ = c2sW.Close()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server exited with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for ServeStream shutdown")
	}
}

func extractChallengeID(s string) string {
	marker := `"id":"ch-`
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	rest := s[idx+6:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}
