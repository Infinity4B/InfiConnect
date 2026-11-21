//go:build darwin

package rpc

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func isElevatedProcess() bool {
	return os.Geteuid() == 0
}

// spawnElevatedWorker starts an administrator-privileged `--core` worker on macOS
// using the native macOS authorization prompt (osascript) and bridges JSON-RPC
// securely over a private one-time Unix domain socket authenticated with a random token.
func (e *SubprocessEngine) spawnElevatedWorker(bin string) (*workerProc, error) {
	tmpDir, err := os.MkdirTemp("", "inficonnect-tun-")
	if err != nil {
		return nil, fmt.Errorf("create temp rpc dir: %w", err)
	}
	_ = os.Chmod(tmpDir, 0o700)

	cleanupTemp := func() {
		_ = os.RemoveAll(tmpDir)
	}

	var tokBuf [32]byte
	if _, err := rand.Read(tokBuf[:]); err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("generate rpc token: %w", err)
	}
	token := hex.EncodeToString(tokBuf[:])
	tokPath := filepath.Join(tmpDir, "token")
	if err := os.WriteFile(tokPath, []byte(token), 0o600); err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("write rpc token file: %w", err)
	}

	sockPath := filepath.Join(tmpDir, "rpc.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("listen rpc unix socket: %w", err)
	}
	_ = os.Chmod(sockPath, 0o600)

	shellArgs := []string{
		fmt.Sprintf("INFICONNECT_OWNER_UID=%d", os.Getuid()),
		fmt.Sprintf("INFICONNECT_OWNER_GID=%d", os.Getgid()),
		shellQuote(bin),
	}
	for _, a := range e.opts.WorkerArgs {
		shellArgs = append(shellArgs, shellQuote(a))
	}
	shellArgs = append(shellArgs,
		"--rpc-connect", shellQuote(sockPath),
		"--rpc-token-file", shellQuote(tokPath),
		"</dev/null", ">/dev/null", "2>&1", "&",
	)
	shellCmd := strings.Join(shellArgs, " ")

	appleScript := fmt.Sprintf(
		`do shell script "%s" with administrator privileges with prompt "InfiConnect 需要管理员权限以创建分流 TUN 网卡并添加内网路由（不会影响默认路由或 Loon/FlClash）。"`,
		escapeAppleScript(shellCmd),
	)

	cmd := exec.Command("osascript", "-e", appleScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = ln.Close()
		cleanupTemp()
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "cancel") || strings.Contains(msg, "-128") {
			return nil, errors.New("administrator authorization was canceled")
		}
		if msg != "" {
			return nil, fmt.Errorf("elevated TUN worker authorization failed: %s", msg)
		}
		return nil, fmt.Errorf("elevated TUN worker authorization failed: %w", err)
	}

	if ul, ok := ln.(*net.UnixListener); ok {
		_ = ul.SetDeadline(time.Now().Add(15 * time.Second))
	}
	conn, err := ln.Accept()
	_ = ln.Close()
	if err != nil {
		cleanupTemp()
		return nil, fmt.Errorf("timed out waiting for elevated TUN worker to connect: %w", err)
	}

	// Authenticate the connecting worker before exposing any RPC commands.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(conn, 64*1024)
	authLine, err := reader.ReadBytes('\n')
	_ = conn.SetReadDeadline(time.Time{})
	cleanupTemp()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read elevated worker auth handshake: %w", err)
	}

	var hs socketAuthHandshake
	if err := json.Unmarshal(authLine, &hs); err != nil ||
		subtle.ConstantTimeCompare([]byte(hs.Token), []byte(token)) != 1 {
		_ = conn.Close()
		return nil, errors.New("elevated worker authentication token mismatch")
	}

	w := &workerProc{
		elevated: true,
		stdin:    conn,
		stdout:   &bufferedReadCloser{r: reader, c: conn},
		pending:  make(map[uint64]chan Message),
		exited:   make(chan error, 1),
	}

	go func() {
		e.readWorkerStdout(w)
		_ = conn.Close()
		w.failPending(errors.New("elevated core worker disconnected"))
		w.exited <- nil
		close(w.exited)
	}()

	return w, nil
}

type bufferedReadCloser struct {
	r *bufio.Reader
	c net.Conn
}

func (b *bufferedReadCloser) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *bufferedReadCloser) Close() error               { return b.c.Close() }

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
