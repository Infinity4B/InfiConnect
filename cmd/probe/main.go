// Command probe is the M0 headless verification harness for InfiConnect's core layer.
// It validates:
//  1. Auth challenge handler injection (SMS/TOTP/Text/Click/ExternalLogin)
//  2. Local SOCKS5/HTTP proxy forwarding through core.Engine
//  3. Repeated Connect -> Disconnect cycles (port release & goroutine cleanup)
//
// Usage:
//
//	go run ./cmd/probe --mock --cycles 3
//	go run ./cmd/probe --protocol easyconnect --server vpn.example.edu --username u --password p --target http://internal.host --cycles 3
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/inficonnect/inficonnect/core"
)

func main() {
	var (
		mockMode  = flag.Bool("mock", false, "run with local mock VPN session and local target server")
		protocol  = flag.String("protocol", "easyconnect", "easyconnect or atrust")
		server    = flag.String("server", "", "VPN server hostname")
		port      = flag.Int("port", 443, "VPN server port")
		username  = flag.String("username", "", "VPN username")
		password  = flag.String("password", "", "VPN password")
		totp      = flag.String("totp", "", "TOTP secret")
		socksBind = flag.String("socks", "127.0.0.1:10880", "SOCKS5 listen address")
		httpBind  = flag.String("http", "127.0.0.1:10881", "HTTP proxy listen address")
		targetURL = flag.String("target", "", "URL to fetch via proxy after connecting")
		cycles    = flag.Int("cycles", 3, "number of Connect -> Disconnect cycles")
	)
	flag.Parse()

	opts := core.InProcessOptions{CaptureStdLog: true}
	var mockTarget *httptest.Server
	if *mockMode {
		mockTarget = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("probe-ok"))
		}))
		defer mockTarget.Close()
		if *server == "" {
			*server = "mock.vpn.local"
		}
		if *targetURL == "" {
			*targetURL = mockTarget.URL
		}
		opts.SessionFactory = core.NewLoopbackMockSessionFactory()
	}

	if *server == "" {
		fmt.Fprintln(os.Stderr, "error: --server is required (or pass --mock)")
		os.Exit(2)
	}

	eng := core.NewInProcessEngine(opts)
	defer eng.Close()

	evCh, unsub := eng.Subscribe()
	defer unsub()

	stdin := bufio.NewReader(os.Stdin)
	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()
	fmt.Printf("[M0 Probe] Baseline goroutines: %d\n", baseGoroutines)

	prof := core.Profile{
		ID:            "probe",
		Name:          "M0 Probe",
		Protocol:      core.Protocol(*protocol),
		ServerAddress: *server,
		ServerPort:    *port,
		Username:      *username,
		Password:      *password,
		TOTPSecret:    *totp,
		SocksBind:     *socksBind,
		HTTPBind:      *httpBind,
		AutoReconnect: false,
	}

	for cycle := 1; cycle <= *cycles; cycle++ {
		fmt.Printf("\n=== Cycle %d/%d ===\n", cycle, *cycles)
		if err := eng.Connect(context.Background(), prof); err != nil {
			fmt.Fprintf(os.Stderr, "Cycle %d Connect error: %v\n", cycle, err)
			os.Exit(1)
		}

		connected := false
		for !connected {
			ev := <-evCh
			switch ev.Type {
			case core.EventLog:
				if ev.Log != nil {
					fmt.Printf("  [%s] %s\n", ev.Log.Source, ev.Log.Message)
				}
			case core.EventChallenge:
				handleInteractiveChallenge(eng, ev.Challenge, stdin)
			case core.EventState:
				if ev.Status != nil {
					fmt.Printf("  -> State: %s\n", ev.Status.State)
					if ev.Status.State == core.StateConnected {
						connected = true
					} else if ev.Status.State == core.StateError {
						fmt.Fprintf(os.Stderr, "Cycle %d entered error: %s\n", cycle, ev.Status.LastError)
						os.Exit(1)
					}
				}
			}
		}

		if *targetURL != "" {
			st := eng.Status()
			u, _ := url.Parse("http://" + st.HTTPAddr)
			hc := &http.Client{
				Transport: &http.Transport{Proxy: http.ProxyURL(u)},
				Timeout:   10 * time.Second,
			}
			resp, err := hc.Get(*targetURL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Cycle %d proxy GET %s failed: %v\n", cycle, *targetURL, err)
				os.Exit(1)
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
			_ = resp.Body.Close()
			fmt.Printf("  -> Proxy GET %s: HTTP %d (%q)\n", *targetURL, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		if err := eng.Disconnect(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Cycle %d Disconnect error: %v\n", cycle, err)
			os.Exit(1)
		}

		// Verify immediate port release.
		for _, addr := range []string{*socksBind, *httpBind} {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Cycle %d FAIL: port %s still occupied after disconnect: %v\n", cycle, addr, err)
				os.Exit(1)
			}
			_ = ln.Close()
		}

		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		fmt.Printf("  -> Ports released OK; goroutines now: %d (delta: %+d)\n", runtime.NumGoroutine(), runtime.NumGoroutine()-baseGoroutines)
	}

	fmt.Printf("\n[M0 Probe] SUCCESS: %d/%d cycles completed cleanly.\n", *cycles, *cycles)
}

func handleInteractiveChallenge(eng core.Engine, ch *core.Challenge, stdin *bufio.Reader) {
	if ch == nil {
		return
	}
	fmt.Printf("  [Challenge %s] type=%s message=%q\n", ch.ID, ch.Type, ch.Message)
	switch ch.Type {
	case core.ChallengeTypeCode, core.ChallengeTypeTextCaptcha:
		fmt.Print("  Enter code: ")
		line, _ := stdin.ReadString('\n')
		_ = eng.SubmitChallenge(context.Background(), core.ChallengeAnswer{ID: ch.ID, Code: strings.TrimSpace(line)})
	case core.ChallengeTypeExternalLogin:
		fmt.Printf("  Open URL: %s\n  Paste callback URL: ", ch.LoginURL)
		line, _ := stdin.ReadString('\n')
		_ = eng.SubmitChallenge(context.Background(), core.ChallengeAnswer{ID: ch.ID, CallbackURL: strings.TrimSpace(line)})
	case core.ChallengeTypeClickCaptcha:
		fmt.Printf("  Click captcha (%dx%d), enter x,y pairs separated by spaces: ", ch.ImageWidth, ch.ImageHeight)
		line, _ := stdin.ReadString('\n')
		var pts []core.Point
		for _, tok := range strings.Fields(line) {
			parts := strings.Split(tok, ",")
			if len(parts) == 2 {
				x, _ := strconv.Atoi(parts[0])
				y, _ := strconv.Atoi(parts[1])
				pts = append(pts, core.Point{X: x, Y: y})
			}
		}
		_ = eng.SubmitChallenge(context.Background(), core.ChallengeAnswer{
			ID:     ch.ID,
			Points: pts,
			Width:  ch.ImageWidth,
			Height: ch.ImageHeight,
		})
	}
}
