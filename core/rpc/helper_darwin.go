//go:build darwin

package rpc

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	helperLabel           = "dev.inficonnect.helper"
	helperSupportDir      = "/Library/Application Support/InfiConnect"
	helperBinaryPath      = "/Library/Application Support/InfiConnect/inficonnect-helper"
	helperTokenPath       = "/Library/Application Support/InfiConnect/helper.token"
	helperPlistPath       = "/Library/LaunchDaemons/dev.inficonnect.helper.plist"
	helperSocketPath      = "/var/run/inficonnect-helper.sock"
	helperLogPath         = "/var/log/inficonnect-helper.log"
	helperProtocolVersion = 1
)

// HelperStatus describes the state of the macOS privileged Split-TUN helper daemon.
type HelperStatus struct {
	Supported   bool   `json:"supported"`
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
	NeedsUpdate bool   `json:"needsUpdate,omitempty"`
	Version     int    `json:"version,omitempty"`
	Message     string `json:"message,omitempty"`
}

type helperHandshakeReq struct {
	Token   string `json:"token"`
	Action  string `json:"action"` // "ping" or "spawn_core"
	Version int    `json:"version"`
}

type helperHandshakeResp struct {
	OK      bool   `json:"ok"`
	Version int    `json:"version"`
	Error   string `json:"error,omitempty"`
}

type helperDaemonConfig struct {
	SocketPath string
	OwnerUID   int
	OwnerGID   int
	TokenFile  string
	WorkerBin  string // defaults to os.Executable()
	WorkerArgs []string
}

// GetHelperStatus checks whether the privileged Split-TUN LaunchDaemon is installed and reachable.
func GetHelperStatus() HelperStatus {
	st := HelperStatus{Supported: true}
	if _, err := os.Stat(helperPlistPath); err == nil {
		if _, err := os.Stat(helperBinaryPath); err == nil {
			st.Installed = true
		}
	}
	if resp, err := pingHelperSocket(helperSocketPath, helperTokenPath); err == nil && resp.OK {
		st.Installed = true
		st.Running = true
		st.Version = resp.Version
	}
	if st.Installed {
		if bin, err := os.Executable(); err == nil && !isHelperBinaryCurrent(bin) {
			st.NeedsUpdate = true
		}
	}
	return st
}

func isHelperDaemonReady() bool {
	if bin, err := os.Executable(); err == nil && !isHelperBinaryCurrent(bin) {
		return false
	}
	resp, err := pingHelperSocket(helperSocketPath, helperTokenPath)
	return err == nil && resp.OK && resp.Version == helperProtocolVersion
}

func isHelperBinaryCurrent(bin string) bool {
	if bin == "" {
		var err error
		bin, err = os.Executable()
		if err != nil {
			return false
		}
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	curStat, err := os.Stat(bin)
	if err != nil {
		return false
	}
	helperStat, err := os.Stat(helperBinaryPath)
	if err != nil {
		return false
	}
	if curStat.Size() != helperStat.Size() {
		return false
	}
	h1, err := fileSHA256(bin)
	if err != nil {
		return false
	}
	h2, err := fileSHA256(helperBinaryPath)
	if err != nil {
		return false
	}
	return h1 == h2
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readHelperToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errors.New("empty helper auth token")
	}
	return tok, nil
}

func dialHelperAction(sockPath, tokenPath, action string) (net.Conn, *bufio.Reader, helperHandshakeResp, error) {
	token, err := readHelperToken(tokenPath)
	if err != nil {
		return nil, nil, helperHandshakeResp{}, err
	}
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, nil, helperHandshakeResp{}, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	reqRaw, err := json.Marshal(helperHandshakeReq{
		Token:   token,
		Action:  action,
		Version: helperProtocolVersion,
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, helperHandshakeResp{}, err
	}
	if _, err := conn.Write(append(reqRaw, '\n')); err != nil {
		_ = conn.Close()
		return nil, nil, helperHandshakeResp{}, err
	}

	reader := bufio.NewReaderSize(conn, 64*1024)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, nil, helperHandshakeResp{}, err
	}
	var resp helperHandshakeResp
	if err := json.Unmarshal(line, &resp); err != nil {
		_ = conn.Close()
		return nil, nil, helperHandshakeResp{}, err
	}
	if !resp.OK {
		_ = conn.Close()
		msg := resp.Error
		if msg == "" {
			msg = "helper rejected request"
		}
		return nil, nil, resp, errors.New(msg)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, reader, resp, nil
}

func pingHelperSocket(sockPath, tokenPath string) (helperHandshakeResp, error) {
	conn, _, resp, err := dialHelperAction(sockPath, tokenPath, "ping")
	if err != nil {
		return resp, err
	}
	_ = conn.Close()
	return resp, nil
}

// InstallPrivilegedHelper installs (or updates) the root LaunchDaemon helper once using
// a single macOS administrator authorization prompt so subsequent TUN connections never
// ask for an administrator password again.
func InstallPrivilegedHelper(bin string) error {
	if bin == "" {
		var err error
		bin, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}

	tmpDir, err := os.MkdirTemp("", "inficonnect-helper-install-")
	if err != nil {
		return fmt.Errorf("create temp install dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	_ = os.Chmod(tmpDir, 0o700)

	var tokBuf [32]byte
	if _, err := rand.Read(tokBuf[:]); err != nil {
		return fmt.Errorf("generate helper token: %w", err)
	}
	token := hex.EncodeToString(tokBuf[:])
	tmpToken := filepath.Join(tmpDir, "helper.token")
	if err := os.WriteFile(tmpToken, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write temp helper token: %w", err)
	}

	uid := os.Getuid()
	gid := os.Getgid()
	plist := buildHelperPlist(uid, gid)
	tmpPlist := filepath.Join(tmpDir, helperLabel+".plist")
	if err := os.WriteFile(tmpPlist, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write temp launchd plist: %w", err)
	}

	script := fmt.Sprintf(
		"set -e; "+
			"mkdir -p %s; "+
			"chown root:wheel %s; chmod 755 %s; "+
			"launchctl bootout system/%s >/dev/null 2>&1 || true; "+
			"rm -f %s; "+
			"cp %s %s; "+
			"xattr -c %s >/dev/null 2>&1 || true; "+
			"chown root:wheel %s; chmod 755 %s; "+
			"rm -f %s; "+
			"cp %s %s; "+
			"chown %d:%d %s; chmod 600 %s; "+
			"cp -f %s %s; "+
			"chown root:wheel %s; chmod 644 %s; "+
			"rm -f %s; "+
			"launchctl enable system/%s >/dev/null 2>&1 || true; "+
			"ok=0; for i in 1 2 3 4 5; do if launchctl bootstrap system %s >/dev/null 2>&1; then ok=1; break; fi; sleep 0.3; done; "+
			"if [ \"$ok\" != \"1\" ]; then launchctl bootstrap system %s; fi; "+
			"launchctl kickstart -k system/%s >/dev/null 2>&1 || true",
		shellQuote(helperSupportDir),
		shellQuote(helperSupportDir), shellQuote(helperSupportDir),
		helperLabel,
		shellQuote(helperBinaryPath),
		shellQuote(bin), shellQuote(helperBinaryPath),
		shellQuote(helperBinaryPath),
		shellQuote(helperBinaryPath), shellQuote(helperBinaryPath),
		shellQuote(helperTokenPath),
		shellQuote(tmpToken), shellQuote(helperTokenPath),
		uid, gid, shellQuote(helperTokenPath), shellQuote(helperTokenPath),
		shellQuote(tmpPlist), shellQuote(helperPlistPath),
		shellQuote(helperPlistPath), shellQuote(helperPlistPath),
		shellQuote(helperSocketPath),
		helperLabel,
		shellQuote(helperPlistPath),
		shellQuote(helperPlistPath),
		helperLabel,
	)

	prompt := "InfiConnect 需要一次性管理员授权以安装分流 TUN 后台服务。安装完成后，以后每次连接 VPN 均无需再次输入密码，并可随时在「设置」中卸载。"
	if err := runAdminAppleScript(script, prompt); err != nil {
		return err
	}

	// Wait briefly for launchd to start the daemon and create the socket.
	deadline := time.Now().Add(6 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if resp, err := pingHelperSocket(helperSocketPath, helperTokenPath); err == nil && resp.OK {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("privileged helper installed but did not become ready: %w", lastErr)
}

// UninstallPrivilegedHelper removes the macOS LaunchDaemon and all root-owned helper files.
func UninstallPrivilegedHelper() error {
	script := fmt.Sprintf(
		"set -e; "+
			"launchctl bootout system/%s >/dev/null 2>&1 || true; "+
			"rm -f %s %s; "+
			"rm -rf %s",
		helperLabel,
		shellQuote(helperPlistPath),
		shellQuote(helperSocketPath),
		shellQuote(helperSupportDir),
	)
	prompt := "InfiConnect 需要管理员权限以卸载分流 TUN 后台服务。"
	return runAdminAppleScript(script, prompt)
}

func runAdminAppleScript(shellCmd, prompt string) error {
	appleScript := fmt.Sprintf(
		`do shell script "%s" with administrator privileges with prompt "%s"`,
		escapeAppleScript(shellCmd),
		escapeAppleScript(prompt),
	)
	cmd := exec.Command("osascript", "-e", appleScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(strings.ToLower(msg), "cancel") || strings.Contains(msg, "-128") {
			return errors.New("administrator authorization was canceled")
		}
		if msg != "" {
			return fmt.Errorf("administrator authorization failed: %s", msg)
		}
		return fmt.Errorf("administrator authorization failed: %w", err)
	}
	return nil
}

func buildHelperPlist(uid, gid int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>--helper-daemon</string>
		<string>--socket</string>
		<string>%s</string>
		<string>--owner-uid</string>
		<string>%d</string>
		<string>--owner-gid</string>
		<string>%d</string>
		<string>--token-file</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, helperLabel, helperBinaryPath, helperSocketPath, uid, gid, helperTokenPath, helperLogPath)
}

// RunHelperDaemon runs the persistent macOS LaunchDaemon mode (`--helper-daemon`).
func RunHelperDaemon() error {
	cfg := parseHelperDaemonArgs(os.Args[1:])
	if cfg.SocketPath == "" {
		cfg.SocketPath = helperSocketPath
	}
	if cfg.TokenFile == "" {
		cfg.TokenFile = helperTokenPath
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runHelperDaemonWithConfig(ctx, cfg)
}

func parseHelperDaemonArgs(args []string) helperDaemonConfig {
	cfg := helperDaemonConfig{
		OwnerUID: os.Getuid(),
		OwnerGID: os.Getgid(),
	}
	for i := 0; i < len(args); i++ {
		if i+1 >= len(args) {
			break
		}
		switch args[i] {
		case "--socket":
			cfg.SocketPath = args[i+1]
			i++
		case "--token-file":
			cfg.TokenFile = args[i+1]
			i++
		case "--owner-uid":
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				cfg.OwnerUID = v
			}
			i++
		case "--owner-gid":
			if v, err := strconv.Atoi(args[i+1]); err == nil {
				cfg.OwnerGID = v
			}
			i++
		}
	}
	return cfg
}

func runHelperDaemonWithConfig(ctx context.Context, cfg helperDaemonConfig) error {
	expectedToken, err := readHelperToken(cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("read helper token file: %w", err)
	}
	workerBin := cfg.WorkerBin
	workerArgs := cfg.WorkerArgs
	if workerBin == "" {
		workerBin, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolve helper executable: %w", err)
		}
		if len(workerArgs) == 0 {
			workerArgs = []string{"--core"}
		}
	}

	_ = os.Remove(cfg.SocketPath)
	ln, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen helper socket %s: %w", cfg.SocketPath, err)
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(cfg.SocketPath)
	}()

	if os.Geteuid() == 0 {
		_ = os.Chown(cfg.SocketPath, cfg.OwnerUID, cfg.OwnerGID)
	}
	_ = os.Chmod(cfg.SocketPath, 0o600)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept helper connection: %w", err)
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			handleHelperConn(ctx, c, cfg, expectedToken, workerBin, workerArgs)
		}(conn)
	}
}

func verifyUnixPeerUID(conn net.Conn, allowedUID int) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("not a unix socket connection")
	}
	rawConn, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("syscall conn: %w", err)
	}
	var peerUID uint32
	var sockErr error
	if err := rawConn.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			sockErr = err
			return
		}
		peerUID = cred.Uid
	}); err != nil {
		return fmt.Errorf("control unix socket: %w", err)
	}
	if sockErr != nil {
		return fmt.Errorf("read peer credentials: %w", sockErr)
	}
	if int(peerUID) != allowedUID && peerUID != 0 {
		return fmt.Errorf("unauthorized peer uid %d (expected %d)", peerUID, allowedUID)
	}
	return nil
}

func writeHelperResp(w io.Writer, resp helperHandshakeResp) {
	if raw, err := json.Marshal(resp); err == nil {
		_, _ = w.Write(append(raw, '\n'))
	}
}

func handleHelperConn(ctx context.Context, conn net.Conn, cfg helperDaemonConfig, expectedToken, workerBin string, workerArgs []string) {
	defer conn.Close()

	if err := verifyUnixPeerUID(conn, cfg.OwnerUID); err != nil {
		writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: err.Error()})
		return
	}

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReaderSize(conn, 64*1024)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	var req helperHandshakeReq
	if err := json.Unmarshal(line, &req); err != nil {
		writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: "invalid handshake"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(expectedToken)) != 1 {
		writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: "authentication failed"})
		return
	}

	switch req.Action {
	case "ping":
		writeHelperResp(conn, helperHandshakeResp{OK: true, Version: helperProtocolVersion})
		return

	case "spawn_core":
		cmd := exec.Command(workerBin, workerArgs...)
		cmd.Env = append(os.Environ(),
			fmt.Sprintf("INFICONNECT_OWNER_UID=%d", cfg.OwnerUID),
			fmt.Sprintf("INFICONNECT_OWNER_GID=%d", cfg.OwnerGID),
		)
		wStdin, err := cmd.StdinPipe()
		if err != nil {
			writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: err.Error()})
			return
		}
		wStdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = wStdin.Close()
			writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: err.Error()})
			return
		}
		cmd.Stderr = io.Discard

		if err := cmd.Start(); err != nil {
			_ = wStdin.Close()
			_ = wStdout.Close()
			writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: err.Error()})
			return
		}

		writeHelperResp(conn, helperHandshakeResp{OK: true, Version: helperProtocolVersion})
		_ = conn.SetDeadline(time.Time{})

		waitDone := make(chan struct{})
		go func() {
			defer close(waitDone)
			_ = cmd.Wait()
		}()

		// If daemon shuts down, close conn to tear down the worker cleanly.
		stopWatch := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-stopWatch:
			}
		}()

		stdinDone := make(chan struct{})
		go func() {
			defer close(stdinDone)
			_, _ = io.Copy(wStdin, reader)
			_ = wStdin.Close()
		}()
		stdoutDone := make(chan struct{})
		go func() {
			defer close(stdoutDone)
			_, _ = io.Copy(conn, wStdout)
			_ = conn.Close()
		}()

		// Wait until either the worker exits or the client disconnects.
		select {
		case <-waitDone:
			_ = conn.Close()
		case <-stdinDone:
			// Client disconnected; give worker a moment to exit cleanly on stdin EOF
			// so it removes utun routes gracefully before escalating to SIGTERM/SIGKILL.
			select {
			case <-waitDone:
			case <-time.After(3 * time.Second):
				if cmd.Process != nil {
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
				select {
				case <-waitDone:
				case <-time.After(1500 * time.Millisecond):
					if cmd.Process != nil {
						_ = cmd.Process.Kill()
					}
					<-waitDone
				}
			}
			_ = conn.Close()
		}
		<-stdoutDone
		<-stdinDone
		close(stopWatch)

	default:
		writeHelperResp(conn, helperHandshakeResp{OK: false, Version: helperProtocolVersion, Error: "unknown action"})
	}
}
