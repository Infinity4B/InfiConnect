package core_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inficonnect/inficonnect/core"
	"github.com/mythologyli/zju-connect/client/authchallenge"
)

func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 37, G: 99, B: 235, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

type mockSession struct {
	resources core.ResourceSnapshot
	broken    chan error
	kaFail    atomic.Bool
	closed    atomic.Bool
}

func (m *mockSession) DialTarget() *core.DialTarget {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &core.DialTarget{
		Dial:       d.DialContext,
		DialIPPort: d.DialContext,
		Resolve: func(ctx context.Context, name string) (context.Context, net.IP, error) {
			if ip := net.ParseIP(name); ip != nil {
				return ctx, ip, nil
			}
			return ctx, net.ParseIP("127.0.0.1"), nil
		},
	}
}

func (m *mockSession) Resources() core.ResourceSnapshot { return m.resources }
func (m *mockSession) Broken() <-chan error             { return m.broken }
func (m *mockSession) KeepAlive(ctx context.Context) error {
	if m.kaFail.Load() {
		return errors.New("simulated keepalive failure")
	}
	return nil
}
func (m *mockSession) Close() error {
	m.closed.Store(true)
	return nil
}

func TestChallengeBridge_AllFourTypes(t *testing.T) {
	pngBytes := makeTestPNG(t, 320, 180)
	chEvents := make(chan core.Challenge, 8)
	var bridge *core.ChallengeBridge
	bridge = core.NewChallengeBridge(
		5*time.Second,
		func() context.Context { return context.Background() },
		func(ch core.Challenge) { chEvents <- ch },
		nil,
	)

	// 1. Code challenges (SMS / TOTP / Radius / Device)
	for _, kind := range []authchallenge.CodeKind{
		authchallenge.CodeSMS,
		authchallenge.CodeTOTP,
		authchallenge.CodeRadius,
		authchallenge.CodeDevice,
	} {
		go func() {
			ch := <-chEvents
			if ch.Type != core.ChallengeTypeCode || ch.CodeKind != core.CodeKind(kind) {
				t.Errorf("unexpected challenge: %+v", ch)
			}
			_ = bridge.Submit(core.ChallengeAnswer{
				ID:                ch.ID,
				Code:              "654321",
				SkipSecondaryAuth: true,
			})
		}()
		resp, err := bridge.HandleCodeChallenge(authchallenge.CodeChallenge{
			Kind:                 kind,
			Message:              "Enter code",
			CanSkipSecondaryAuth: true,
		})
		if err != nil {
			t.Fatalf("code challenge %s failed: %v", kind, err)
		}
		if resp.Code != "654321" || !resp.SkipSecondaryAuth {
			t.Fatalf("unexpected code response: %+v", resp)
		}
	}

	// 2. Text Captcha
	go func() {
		ch := <-chEvents
		if ch.Type != core.ChallengeTypeTextCaptcha || ch.ImageWidth != 320 || ch.ImageHeight != 180 || ch.ImageMime != "image/png" {
			t.Errorf("unexpected text captcha: %+v", ch)
		}
		_ = bridge.Submit(core.ChallengeAnswer{ID: ch.ID, Code: "AB12"})
	}()
	textResp, err := bridge.HandleTextCaptcha(authchallenge.TextCaptchaChallenge{
		Image:   pngBytes,
		Message: "Type letters",
	})
	if err != nil || textResp.Code != "AB12" {
		t.Fatalf("text captcha failed: resp=%+v err=%v", textResp, err)
	}

	// 3. Click Captcha
	go func() {
		ch := <-chEvents
		if ch.Type != core.ChallengeTypeClickCaptcha || ch.ImageWidth != 320 || ch.ImageHeight != 180 {
			t.Errorf("unexpected click captcha: %+v", ch)
		}
		_ = bridge.Submit(core.ChallengeAnswer{
			ID:     ch.ID,
			Points: []core.Point{{X: 100, Y: 50}, {X: 220, Y: 120}},
			Width:  320,
			Height: 180,
		})
	}()
	clickResp, err := bridge.HandleClickCaptcha(authchallenge.ClickCaptchaChallenge{
		Image:   pngBytes,
		Message: "Click characters in order",
	})
	if err != nil || len(clickResp.Points) != 2 || clickResp.Width != 320 || clickResp.Height != 180 {
		t.Fatalf("click captcha failed: resp=%+v err=%v", clickResp, err)
	}

	// 4. External Login (CAS & OAuth2)
	for _, kind := range []authchallenge.ExternalLoginKind{
		authchallenge.ExternalLoginCAS,
		authchallenge.ExternalLoginOAuth2,
	} {
		go func() {
			ch := <-chEvents
			if ch.Type != core.ChallengeTypeExternalLogin || ch.ExternalKind != core.ExternalLoginKind(kind) {
				t.Errorf("unexpected external login: %+v", ch)
			}
			_ = bridge.Submit(core.ChallengeAnswer{
				ID:          ch.ID,
				CallbackURL: "https://vpn.example.edu/passport/v1/auth/cas?ticket=ST-12345",
			})
		}()
		extResp, err := bridge.HandleExternalLogin(authchallenge.ExternalLoginChallenge{
			Kind:     kind,
			LoginURL: "https://sso.example.edu/login",
		})
		if err != nil || !strings.Contains(extResp.CallbackURL, "ST-12345") {
			t.Fatalf("external login %s failed: resp=%+v err=%v", kind, extResp, err)
		}
	}
}

func TestChallengeBridge_CancelAndTimeout(t *testing.T) {
	// Cancel path
	chEvents := make(chan core.Challenge, 2)
	var bridge *core.ChallengeBridge
	bridge = core.NewChallengeBridge(
		5*time.Second,
		func() context.Context { return context.Background() },
		func(ch core.Challenge) { chEvents <- ch },
		nil,
	)

	go func() {
		ch := <-chEvents
		_ = bridge.Cancel(ch.ID)
	}()
	_, err := bridge.HandleCodeChallenge(authchallenge.CodeChallenge{Kind: authchallenge.CodeSMS})
	if !errors.Is(err, core.ErrChallengeCanceled) {
		t.Fatalf("expected ErrChallengeCanceled, got %v", err)
	}

	// Timeout path
	shortBridge := core.NewChallengeBridge(
		40*time.Millisecond,
		func() context.Context { return context.Background() },
		nil,
		nil,
	)
	_, err = shortBridge.HandleCodeChallenge(authchallenge.CodeChallenge{Kind: authchallenge.CodeTOTP})
	if !errors.Is(err, core.ErrChallengeTimeout) {
		t.Fatalf("expected ErrChallengeTimeout, got %v", err)
	}
}

func TestInProcessEngine_LifecycleReconnectAndProxy(t *testing.T) {
	// Start local HTTP target server to test proxy traffic & byte counting.
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello-inficonnect"))
	}))
	defer targetSrv.Close()

	var connects atomic.Int32
	var curSess atomic.Pointer[mockSession]

	eng := core.NewInProcessEngine(core.InProcessOptions{
		ChallengeTimeout:     2 * time.Second,
		KeepAliveInterval:    80 * time.Millisecond,
		ReconnectBaseBackoff: 50 * time.Millisecond,
		ReconnectMaxBackoff:  100 * time.Millisecond,
		SessionFactory: func(ctx context.Context, profile core.Profile, handler authchallenge.Handler, reuseSession bool, logFn func(core.LogLevel, string, string)) (core.ActiveSession, error) {
			n := connects.Add(1)
			if n == 1 {
				// Trigger an SMS challenge on initial connect.
				resp, err := handler.HandleCodeChallenge(authchallenge.CodeChallenge{
					Kind:    authchallenge.CodeSMS,
					Message: "Enter SMS code",
				})
				if err != nil {
					return nil, err
				}
				if resp.Code != "123456" {
					return nil, errors.New("wrong sms code")
				}
			}
			s := &mockSession{
				broken: make(chan error, 1),
				resources: core.ResourceSnapshot{
					VirtualIP: "10.99.0.2",
					IPResources: []core.IPResourceItem{
						{IPMin: "10.0.0.0", IPMax: "10.255.255.255", CIDR: "10.0.0.0/8", PortMin: 1, PortMax: 65535, Protocol: "all"},
					},
					DomainResources: []core.DomainResourceItem{
						{Domain: "internal.example.edu", PortMin: 1, PortMax: 65535, Protocol: "tcp"},
					},
				},
			}
			curSess.Store(s)
			return s, nil
		},
	})
	defer eng.Close()

	evCh, unsub := eng.Subscribe()
	defer unsub()

	prof := core.Profile{
		ID:            "p1",
		Name:          "Test VPN",
		Protocol:      core.ProtocolEasyConnect,
		ServerAddress: "vpn.example.edu",
		ServerPort:    443,
		SocksBind:     "127.0.0.1:0",
		HTTPBind:      "127.0.0.1:0",
		AutoReconnect: true,
	}

	if err := eng.Connect(context.Background(), prof); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Wait for challenge event and answer it.
	waitFor := func(desc string, pred func(core.Event) bool) core.Event {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-evCh:
				if pred(ev) {
					return ev
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s (state=%s)", desc, eng.State())
			}
		}
	}

	chEv := waitFor("challenge", func(e core.Event) bool { return e.Type == core.EventChallenge })
	if err := eng.SubmitChallenge(context.Background(), core.ChallengeAnswer{
		ID:   chEv.Challenge.ID,
		Code: "123456",
	}); err != nil {
		t.Fatalf("SubmitChallenge failed: %v", err)
	}

	waitFor("connected", func(e core.Event) bool {
		return e.Type == core.EventState && e.Status != nil && e.Status.State == core.StateConnected
	})

	// Second concurrent Connect must be rejected.
	if err := eng.Connect(context.Background(), prof); !errors.Is(err, core.ErrAlreadyConnected) {
		t.Fatalf("expected ErrAlreadyConnected, got %v", err)
	}

	// Verify HTTP proxy forwarding & traffic counters!
	status := eng.Status()
	proxyURL, err := url.Parse("http://" + status.HTTPAddr)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   3 * time.Second,
	}
	resp, err := httpClient.Get(targetSrv.URL)
	if err != nil {
		t.Fatalf("HTTP proxy request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "hello-inficonnect" {
		t.Fatalf("unexpected proxy response: %q", string(body))
	}
	if eng.Status().Traffic.BytesRecv == 0 {
		t.Fatalf("expected non-zero BytesRecv after proxy traffic")
	}

	// Verify route checker!
	if r := eng.CheckRoute("https://git.internal.example.edu/repo", 443, "tcp"); !r.ViaVPN {
		t.Fatalf("expected internal domain via VPN, got %+v", r)
	}
	if r := eng.CheckRoute("10.1.2.3:80", 80, "tcp"); !r.ViaVPN {
		t.Fatalf("expected 10.x via VPN, got %+v", r)
	}
	if r := eng.CheckRoute("1.1.1.1:443", 443, "tcp"); r.ViaVPN {
		t.Fatalf("expected 1.1.1.1 direct, got %+v", r)
	}

	// Trigger tunnel drop -> auto-reconnect!
	curSess.Load().broken <- errors.New("network reset")
	waitFor("reconnecting", func(e core.Event) bool {
		return e.Type == core.EventState && e.Status != nil && e.Status.State == core.StateReconnecting
	})
	waitFor("reconnected", func(e core.Event) bool {
		return e.Type == core.EventState && e.Status != nil && e.Status.State == core.StateConnected
	})
	if connects.Load() < 2 {
		t.Fatalf("expected >= 2 connects after reconnect, got %d", connects.Load())
	}

	// Disconnect and verify port release across 3 cycles!
	if err := eng.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	if eng.State() != core.StateIdle {
		t.Fatalf("expected StateIdle after disconnect, got %s", eng.State())
	}
}

func TestThreeConnectDisconnectCycles_PortAndGoroutineCleanup(t *testing.T) {
	// Pick two free ports and reuse the EXACT same ports 3 times in a row.
	ln1, _ := net.Listen("tcp", "127.0.0.1:0")
	socksAddr := ln1.Addr().String()
	_ = ln1.Close()

	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	httpAddr := ln2.Addr().String()
	_ = ln2.Close()

	eng := core.NewInProcessEngine(core.InProcessOptions{
		SessionFactory: func(ctx context.Context, profile core.Profile, handler authchallenge.Handler, reuseSession bool, logFn func(core.LogLevel, string, string)) (core.ActiveSession, error) {
			return &mockSession{broken: make(chan error, 1)}, nil
		},
	})
	defer eng.Close()

	evCh, unsub := eng.Subscribe()
	defer unsub()

	runtime.GC()
	baseGoroutines := runtime.NumGoroutine()

	prof := core.Profile{
		ID:            "cycle",
		Name:          "Cycle Test",
		Protocol:      core.ProtocolEasyConnect,
		ServerAddress: "vpn.example.edu",
		ServerPort:    443,
		SocksBind:     socksAddr,
		HTTPBind:      httpAddr,
	}

	for i := 0; i < 3; i++ {
		if err := eng.Connect(context.Background(), prof); err != nil {
			t.Fatalf("cycle %d Connect failed (port not released?): %v", i+1, err)
		}
		deadline := time.After(3 * time.Second)
	waitConn:
		for {
			select {
			case ev := <-evCh:
				if ev.Type == core.EventState && ev.Status != nil && ev.Status.State == core.StateConnected {
					break waitConn
				}
			case <-deadline:
				t.Fatalf("cycle %d timed out waiting for connected", i+1)
			}
		}
		if err := eng.Disconnect(context.Background()); err != nil {
			t.Fatalf("cycle %d Disconnect failed: %v", i+1, err)
		}
	}

	// Verify both ports are immediately bindable again.
	for _, addr := range []string{socksAddr, httpAddr} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("port %s still occupied after disconnect: %v", addr, err)
		}
		_ = ln.Close()
	}

	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	afterGoroutines := runtime.NumGoroutine()
	if afterGoroutines > baseGoroutines+4 {
		t.Fatalf("goroutine leak detected: before=%d after=%d", baseGoroutines, afterGoroutines)
	}
}

func TestImportZJUConnectTOMLAndRedaction(t *testing.T) {
	tomlCfg := `
protocol = "atrust"
server_address = "vpn.zju.edu.cn"
server_port = 443
username = "3200101234"
password = "super-secret-password"
totp_secret = "JBSWY3DPEHPK3PXP"
auth_type = "auth/psw"
login_domain = "Radius"
socks_bind = ":1080"
http_bind = ":1081"
proxy_all = true
`
	p, err := core.ImportZJUConnectTOML([]byte(tomlCfg), "ZJU aTrust")
	if err != nil {
		t.Fatalf("ImportZJUConnectTOML failed: %v", err)
	}
	if p.Protocol != core.ProtocolATrust || p.ServerAddress != "vpn.zju.edu.cn" || p.SocksBind != "127.0.0.1:1080" || !p.ProxyAll {
		t.Fatalf("unexpected imported profile: %+v", p)
	}

	raw := `Login user=3200101234 password="super-secret-password" twf_id=abcdef0123456789abcdef0123456789 callback=https://vpn.edu/passport/v1/auth/cas?ticket=ST-999888 phone=13812345678`
	redacted := core.SanitizeLogLine(raw, "3200101234", "super-secret-password")
	for _, leak := range []string{"3200101234", "super-secret-password", "ST-999888", "13812345678", "abcdef0123456789abcdef0123456789"} {
		if strings.Contains(redacted, leak) {
			t.Fatalf("redaction leaked %q in %q", leak, redacted)
		}
	}
}
