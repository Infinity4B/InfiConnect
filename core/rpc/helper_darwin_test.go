//go:build darwin

package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHelperDaemon_PingAuthAndSpawnWorker(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ic-helper-test-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "helper.sock")
	tokenPath := filepath.Join(dir, "helper.token")
	if err := os.WriteFile(tokenPath, []byte("test-secret-token-123456\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	daemonDone := make(chan error, 1)
	go func() {
		daemonDone <- runHelperDaemonWithConfig(ctx, helperDaemonConfig{
			SocketPath: sockPath,
			OwnerUID:   os.Getuid(),
			OwnerGID:   os.Getgid(),
			TokenFile:  tokenPath,
			WorkerBin:  "/bin/cat",
			WorkerArgs: nil,
		})
	}()

	// Wait for socket to come up.
	deadline := time.Now().Add(3 * time.Second)
	var resp helperHandshakeResp
	for time.Now().Before(deadline) {
		resp, err = pingHelperSocket(sockPath, tokenPath)
		if err == nil && resp.OK {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil || !resp.OK || resp.Version != helperProtocolVersion {
		t.Fatalf("pingHelperSocket failed: resp=%+v err=%v", resp, err)
	}

	// Wrong token must be rejected.
	badTokenPath := filepath.Join(dir, "bad.token")
	if err := os.WriteFile(badTokenPath, []byte("wrong-token\n"), 0o600); err != nil {
		t.Fatalf("write bad token: %v", err)
	}
	if _, err := pingHelperSocket(sockPath, badTokenPath); err == nil {
		t.Fatal("expected wrong token to be rejected")
	}

	// Spawn a bridged worker (`/bin/cat`) and verify bidirectional round-trip + clean teardown.
	conn, reader, spawnResp, err := dialHelperAction(sockPath, tokenPath, "spawn_core")
	if err != nil || !spawnResp.OK {
		t.Fatalf("dialHelperAction(spawn_core): resp=%+v err=%v", spawnResp, err)
	}
	if _, err := conn.Write([]byte("hello-tun-helper\n")); err != nil {
		t.Fatalf("write to bridged worker: %v", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read from bridged worker: %v", err)
	}
	if line != "hello-tun-helper\n" {
		t.Fatalf("unexpected echo: %q", line)
	}
	_ = conn.Close()

	// Shutdown daemon cleanly.
	cancel()
	select {
	case err := <-daemonDone:
		if err != nil {
			t.Fatalf("daemon exited with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for helper daemon to exit")
	}
}
