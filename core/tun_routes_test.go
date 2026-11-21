package core

import (
	"net"
	"testing"

	"inet.af/netaddr"
)

func TestComputeSafeTUNRoutesLoonCoexistence(t *testing.T) {
	// Simulate VPN server pushing 192.168.0.0/16, 10.0.0.0/8, and 198.18.0.0/15
	// while local Wi-Fi is 192.168.0.0/24 and VPN server is 202.100.1.5.
	var sb netaddr.IPSetBuilder
	sb.AddPrefix(netaddr.MustParseIPPrefix("192.168.0.0/16"))
	sb.AddPrefix(netaddr.MustParseIPPrefix("10.0.0.0/8"))
	sb.AddPrefix(netaddr.MustParseIPPrefix("198.18.0.0/15"))
	serverSet, _ := sb.IPSet()

	routes := ComputeSafeTUNRoutes(
		serverSet,
		[]string{"172.20.5.0/24"},
		[]string{"10.99.0.0/16"},
		[]netaddr.IPPrefix{netaddr.MustParseIPPrefix("192.168.0.0/24")},
		[]net.IP{net.ParseIP("10.1.2.3")},
	)

	var resultBuilder netaddr.IPSetBuilder
	for _, r := range routes {
		if r == netaddr.MustParseIPPrefix("10.0.0.0/8") || r == netaddr.MustParseIPPrefix("192.168.0.0/16") {
			t.Fatalf("supernet %s should have been split/trimmed to avoid colliding with Loon routes", r)
		}
		if r.Bits() == 0 {
			t.Fatalf("must never install default route 0.0.0.0/0")
		}
		resultBuilder.AddPrefix(r)
	}
	resSet, _ := resultBuilder.IPSet()

	// Target VPN host 192.168.215.55 MUST be routed via TUN.
	if !resSet.Contains(netaddr.MustParseIP("192.168.215.55")) {
		t.Fatal("expected 192.168.215.55 to be included in safe TUN routes")
	}
	// Custom route 172.20.5.10 MUST be routed via TUN.
	if !resSet.Contains(netaddr.MustParseIP("172.20.5.10")) {
		t.Fatal("expected custom route 172.20.5.10 to be included")
	}
	// Local Wi-Fi router 192.168.0.1 MUST be excluded!
	if resSet.Contains(netaddr.MustParseIP("192.168.0.1")) {
		t.Fatal("local LAN 192.168.0.1 must NOT be routed via TUN")
	}
	// Loon Fake-IP 198.19.0.1 MUST be excluded!
	if resSet.Contains(netaddr.MustParseIP("198.19.0.1")) {
		t.Fatal("Loon Fake-IP 198.19.0.1 must NOT be routed via TUN")
	}
	// VPN server IP 10.1.2.3 MUST be excluded to prevent routing loop!
	if resSet.Contains(netaddr.MustParseIP("10.1.2.3")) {
		t.Fatal("VPN server IP 10.1.2.3 must NOT be routed via TUN")
	}
	// User-excluded 10.99.1.1 MUST be excluded!
	if resSet.Contains(netaddr.MustParseIP("10.99.1.1")) {
		t.Fatal("user-excluded 10.99.1.1 must NOT be routed via TUN")
	}
}

func TestComputeSafeTUNRoutesBisectsPureSupernet(t *testing.T) {
	var sb netaddr.IPSetBuilder
	sb.AddPrefix(netaddr.MustParseIPPrefix("10.0.0.0/8"))
	serverSet, _ := sb.IPSet()

	routes := ComputeSafeTUNRoutes(serverSet, nil, nil, nil, nil)
	if len(routes) != 2 {
		t.Fatalf("expected 10.0.0.0/8 to bisect into 2 /9 routes, got %v", routes)
	}
	if routes[0] != netaddr.MustParseIPPrefix("10.0.0.0/9") ||
		routes[1] != netaddr.MustParseIPPrefix("10.128.0.0/9") {
		t.Fatalf("unexpected bisected routes: %v", routes)
	}
}
