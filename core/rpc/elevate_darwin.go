//go:build darwin

package rpc

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

var helperInstallMu sync.Mutex

func isElevatedProcess() bool {
	return os.Geteuid() == 0
}

// spawnElevatedWorker obtains an administrator-privileged `--core` worker on macOS.
//
// It first connects directly to the persistent LaunchDaemon helper over
// /var/run/inficonnect-helper.sock (zero password prompts). Only if the helper
// is not yet installed or running does it perform a one-time macOS administrator
// authorization to install the LaunchDaemon, after which all future connections
// run without prompting for a password.
func (e *SubprocessEngine) spawnElevatedWorker(bin string) (*workerProc, error) {
	if isHelperBinaryCurrent(bin) {
		if w, err := e.connectViaHelperSocket(helperSocketPath, helperTokenPath); err == nil {
			return w, nil
		}
	}

	// Serialize helper installation across concurrent Split-TUN connections so
	// simultaneous connects never trigger duplicate macOS administrator prompts.
	helperInstallMu.Lock()
	defer helperInstallMu.Unlock()

	if isHelperBinaryCurrent(bin) {
		if w, err := e.connectViaHelperSocket(helperSocketPath, helperTokenPath); err == nil {
			return w, nil
		}
	}

	if err := InstallPrivilegedHelper(bin); err != nil {
		return nil, err
	}

	w, err := e.connectViaHelperSocket(helperSocketPath, helperTokenPath)
	if err != nil {
		return nil, fmt.Errorf("connect to privileged TUN helper after install: %w", err)
	}
	return w, nil
}

func (e *SubprocessEngine) connectViaHelperSocket(sockPath, tokenPath string) (*workerProc, error) {
	conn, reader, _, err := dialHelperAction(sockPath, tokenPath, "spawn_core")
	if err != nil {
		return nil, err
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
