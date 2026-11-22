package core_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/inficonnect/inficonnect/core"
	"github.com/mythologyli/zju-connect/client/authchallenge"
)

func TestSessionManager_MultipleSimultaneousConnectionsAndPortAutoAssign(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("multi-ok"))
	}))
	defer targetSrv.Close()

	// Pick two adjacent free ports to use as shared defaults for BOTH profiles.
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port 1: %v", err)
	}
	sharedSocks := ln1.Addr().String()
	_ = ln1.Close()

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen free port 2: %v", err)
	}
	sharedHTTP := ln2.Addr().String()
	_ = ln2.Close()

	mgr := core.NewSessionManager(func() core.Engine {
		return core.NewInProcessEngine(core.InProcessOptions{
			ChallengeTimeout: 5 * time.Second,
			SessionFactory: func(ctx context.Context, profile core.Profile, handler authchallenge.Handler, reuseSession bool, logFn func(core.LogLevel, string, string)) (core.ActiveSession, error) {
				if profile.ID == "vpn-a" {
					resp, err := handler.HandleCodeChallenge(authchallenge.CodeChallenge{
						Kind:    authchallenge.CodeSMS,
						Message: "SMS for VPN A",
					})
					if err != nil {
						return nil, err
					}
					if resp.Code != "111111" {
						return nil, errors.New("wrong code for vpn-a")
					}
					return &mockSession{
						broken: make(chan error, 1),
						resources: core.ResourceSnapshot{
							VirtualIP: "10.10.0.2",
							IPResources: []core.IPResourceItem{
								{IPMin: "10.10.0.0", IPMax: "10.10.255.255", CIDR: "10.10.0.0/16", PortMin: 1, PortMax: 65535, Protocol: "all"},
							},
							DomainResources: []core.DomainResourceItem{
								{Domain: "alpha.corp", PortMin: 1, PortMax: 65535, Protocol: "tcp"},
							},
						},
					}, nil
				}
				resp, err := handler.HandleCodeChallenge(authchallenge.CodeChallenge{
					Kind:    authchallenge.CodeTOTP,
					Message: "TOTP for VPN B",
				})
				if err != nil {
					return nil, err
				}
				if resp.Code != "222222" {
					return nil, errors.New("wrong code for vpn-b")
				}
				return &mockSession{
					broken: make(chan error, 1),
					resources: core.ResourceSnapshot{
						VirtualIP: "172.20.0.2",
						IPResources: []core.IPResourceItem{
							{IPMin: "172.20.0.0", IPMax: "172.20.255.255", CIDR: "172.20.0.0/16", PortMin: 1, PortMax: 65535, Protocol: "all"},
						},
						DomainResources: []core.DomainResourceItem{
							{Domain: "beta.lab", PortMin: 1, PortMax: 65535, Protocol: "tcp"},
						},
					},
				}, nil
			},
		})
	})
	defer mgr.Close()

	evCh, unsub := mgr.Subscribe()
	defer unsub()

	profA := core.Profile{
		ID:            "vpn-a",
		Name:          "Alpha Corp",
		Protocol:      core.ProtocolEasyConnect,
		ServerAddress: "vpn-a.example.com",
		ServerPort:    443,
		SocksBind:     sharedSocks,
		HTTPBind:      sharedHTTP,
	}
	profB := core.Profile{
		ID:            "vpn-b",
		Name:          "Beta Lab",
		Protocol:      core.ProtocolATrust,
		ServerAddress: "vpn-b.example.com",
		ServerPort:    443,
		SocksBind:     sharedSocks, // Intentionally identical to profA!
		HTTPBind:      sharedHTTP,  // Intentionally identical to profA!
	}

	if err := mgr.Connect(context.Background(), profA); err != nil {
		t.Fatalf("Connect profA: %v", err)
	}
	if err := mgr.Connect(context.Background(), profB); err != nil {
		t.Fatalf("Connect profB simultaneously: %v", err)
	}

	// Connecting an already-active profile again must fail with ErrAlreadyConnected.
	if err := mgr.Connect(context.Background(), profA); !errors.Is(err, core.ErrAlreadyConnected) {
		t.Fatalf("expected ErrAlreadyConnected for duplicate profA, got %v", err)
	}

	// Answer both concurrent challenges routed by challenge ID and wait for both to connect.
	connected := map[string]bool{}
	answered := map[string]bool{}
	deadline := time.After(6 * time.Second)
	for len(connected) < 2 {
		select {
		case ev := <-evCh:
			if ev.Type == core.EventChallenge && ev.Challenge != nil && !answered[ev.Challenge.ID] {
				answered[ev.Challenge.ID] = true
				code := "111111"
				if ev.Challenge.ProfileID == "vpn-b" {
					code = "222222"
					if ev.Challenge.ProfileName != "Beta Lab" {
						t.Fatalf("expected challenge profileName Beta Lab, got %q", ev.Challenge.ProfileName)
					}
				} else if ev.Challenge.ProfileID != "vpn-a" || ev.Challenge.ProfileName != "Alpha Corp" {
					t.Fatalf("unexpected challenge profile metadata: %+v", ev.Challenge)
				}
				if err := mgr.SubmitChallenge(context.Background(), core.ChallengeAnswer{
					ID:   ev.Challenge.ID,
					Code: code,
				}); err != nil {
					t.Fatalf("SubmitChallenge(%s): %v", ev.Challenge.ProfileID, err)
				}
			}
			if ev.Type == core.EventState && ev.Status != nil && ev.Status.State == core.StateConnected {
				connected[ev.Status.ProfileID] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for both sessions to connect; connected=%v statuses=%+v", connected, mgr.Statuses())
		}
	}

	stA := mgr.Status("vpn-a")
	stB := mgr.Status("vpn-b")
	if stA.State != core.StateConnected || stB.State != core.StateConnected {
		t.Fatalf("expected both connected, got A=%s B=%s", stA.State, stB.State)
	}
	if stA.SocksAddr == stB.SocksAddr || stA.HTTPAddr == stB.HTTPAddr {
		t.Fatalf("expected distinct auto-assigned proxy ports, got A=(%s,%s) B=(%s,%s)",
			stA.SocksAddr, stA.HTTPAddr, stB.SocksAddr, stB.HTTPAddr)
	}
	if stB.SocksAddr == stB.HTTPAddr {
		t.Fatalf("profB SOCKS and HTTP ports collided: %s", stB.SocksAddr)
	}

	// Verify BOTH HTTP proxies forward traffic independently!
	for _, st := range []core.StatusSnapshot{stA, stB} {
		proxyURL, err := url.Parse("http://" + st.HTTPAddr)
		if err != nil {
			t.Fatalf("parse proxy %s: %v", st.HTTPAddr, err)
		}
		hc := &http.Client{
			Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
			Timeout:   3 * time.Second,
		}
		resp, err := hc.Get(targetSrv.URL)
		if err != nil {
			t.Fatalf("GET via %s (%s) failed: %v", st.ProfileID, st.HTTPAddr, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if string(body) != "multi-ok" {
			t.Fatalf("unexpected response via %s: %q", st.ProfileID, string(body))
		}
	}

	// Verify cross-VPN route checking identifies the right VPN profile.
	if r := mgr.CheckRoute("", "https://git.alpha.corp", 443, "tcp"); !r.ViaVPN || r.ProfileID != "vpn-a" || r.ProfileName != "Alpha Corp" {
		t.Fatalf("expected alpha.corp -> vpn-a, got %+v", r)
	}
	if r := mgr.CheckRoute("", "172.20.5.6:8080", 8080, "tcp"); !r.ViaVPN || r.ProfileID != "vpn-b" || r.ProfileName != "Beta Lab" {
		t.Fatalf("expected 172.20.5.6 -> vpn-b, got %+v", r)
	}
	if r := mgr.CheckRoute("vpn-a", "172.20.5.6:8080", 8080, "tcp"); r.ViaVPN {
		t.Fatalf("expected 172.20.5.6 NOT via vpn-a, got %+v", r)
	}

	if allRes := mgr.AllResources(); len(allRes) != 2 {
		t.Fatalf("expected 2 resource snapshots, got %d", len(allRes))
	}

	// Disconnect ONLY vpn-a; vpn-b must remain connected and functional!
	if err := mgr.Disconnect(context.Background(), "vpn-a"); err != nil {
		t.Fatalf("Disconnect vpn-a: %v", err)
	}
	if st := mgr.Status("vpn-a").State; st != core.StateIdle {
		t.Fatalf("expected vpn-a idle, got %s", st)
	}
	if st := mgr.Status("vpn-b").State; st != core.StateConnected {
		t.Fatalf("expected vpn-b still connected, got %s", st)
	}
	if len(mgr.Statuses()) != 1 {
		t.Fatalf("expected 1 active status after disconnecting vpn-a, got %d", len(mgr.Statuses()))
	}

	// Verify vpn-a's original ports are immediately released.
	for _, addr := range []string{stA.SocksAddr, stA.HTTPAddr} {
		ln, lErr := net.Listen("tcp", addr)
		if lErr != nil {
			t.Fatalf("expected vpn-a port %s released, got %v", addr, lErr)
		}
		_ = ln.Close()
	}

	// DisconnectAll tears down vpn-b cleanly.
	if err := mgr.DisconnectAll(context.Background()); err != nil {
		t.Fatalf("DisconnectAll: %v", err)
	}
	if len(mgr.Statuses()) != 0 {
		t.Fatalf("expected 0 active statuses after DisconnectAll, got %+v", mgr.Statuses())
	}
	for _, addr := range []string{stB.SocksAddr, stB.HTTPAddr} {
		ln, lErr := net.Listen("tcp", addr)
		if lErr != nil {
			t.Fatalf("expected vpn-b port %s released, got %v", addr, lErr)
		}
		_ = ln.Close()
	}
}
