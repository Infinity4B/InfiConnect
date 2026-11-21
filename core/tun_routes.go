package core

import (
	"net"
	"strings"

	"inet.af/netaddr"
)

// loonAndSystemExcludedPrefixes are always subtracted from split-TUN routes so
// InfiConnect's TUN never interferes with loopback, link-local, multicast, or
// Loon / Clash / Surge Fake-IP ranges (198.18.0.0/15).
var loonAndSystemExcludedPrefixes = []netaddr.IPPrefix{
	netaddr.MustParseIPPrefix("0.0.0.0/8"),
	netaddr.MustParseIPPrefix("127.0.0.0/8"),
	netaddr.MustParseIPPrefix("169.254.0.0/16"),
	netaddr.MustParseIPPrefix("198.18.0.0/15"),
	netaddr.MustParseIPPrefix("224.0.0.0/4"),
	netaddr.MustParseIPPrefix("240.0.0.0/4"),
}

// supernetsToBisect are the exact RFC1918 aggregate routes commonly installed by
// Loon / Clash / Surge TUN exclusion tables (pointing to en0). Bisecting them
// into two halves (/9, /13, /17) guarantees macOS `route -n add` never fails
// with EEXIST and wins cleanly via longest-prefix match without modifying Loon's
// existing routes.
var supernetsToBisect = map[netaddr.IPPrefix]bool{
	netaddr.MustParseIPPrefix("10.0.0.0/8"):     true,
	netaddr.MustParseIPPrefix("172.16.0.0/12"):  true,
	netaddr.MustParseIPPrefix("192.168.0.0/16"): true,
}

// ComputeSafeTUNRoutes computes split-tunnel IPv4 prefixes for TUN mode that:
//   - Only route server-pushed VPN IP resources plus optional user customRoutes
//   - Never install a 0.0.0.0/0 default route (keeps Loon/Clash default TUN intact)
//   - Exclude loopback, link-local, multicast, 198.18.0.0/15 (Loon/Clash Fake-IP),
//     active local physical LAN subnets, VPN server IPs, and user excludeRoutes
//   - Bisect broad RFC1918 supernets (10/8, 172.16/12, 192.168/16) so they shadow
//     Loon's exclusion routes cleanly without conflicting or overwriting them.
func ComputeSafeTUNRoutes(
	serverIPSet *netaddr.IPSet,
	customRoutes []string,
	excludeRoutes []string,
	localSubnets []netaddr.IPPrefix,
	vpnServerIPs []net.IP,
) []netaddr.IPPrefix {
	var b netaddr.IPSetBuilder
	if serverIPSet != nil {
		for _, p := range serverIPSet.Prefixes() {
			if p.IP().Is4() && p.Bits() > 0 {
				b.AddPrefix(p)
			}
		}
	}
	for _, raw := range customRoutes {
		if p, ok := parseIPv4PrefixOrHost(raw); ok && p.Bits() > 0 {
			b.AddPrefix(p)
		}
	}

	// Subtract protected system & proxy (Loon/Clash Fake-IP) ranges.
	for _, excl := range loonAndSystemExcludedPrefixes {
		b.RemovePrefix(excl)
	}
	// Subtract local physical LAN subnets (e.g. en0 192.168.0.0/24).
	for _, lan := range localSubnets {
		if lan.IP().Is4() && lan.Bits() > 0 {
			b.RemovePrefix(lan)
		}
	}
	// Subtract VPN server IPs so tunnel underlay traffic can never loop back into TUN.
	for _, ip := range vpnServerIPs {
		if ip4 := ip.To4(); ip4 != nil {
			if addr, ok := netaddr.FromStdIP(ip4); ok {
				b.RemovePrefix(netaddr.IPPrefixFrom(addr, 32))
			}
		}
	}
	// Subtract user-specified exclusions.
	for _, raw := range excludeRoutes {
		if p, ok := parseIPv4PrefixOrHost(raw); ok && p.Bits() > 0 {
			b.RemovePrefix(p)
		}
	}

	set, err := b.IPSet()
	if err != nil || set == nil {
		return nil
	}

	var out []netaddr.IPPrefix
	for _, p := range set.Prefixes() {
		if !p.IP().Is4() || p.Bits() == 0 {
			continue
		}
		if supernetsToBisect[p] {
			left, right, ok := bisectPrefix(p)
			if ok {
				out = append(out, left, right)
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// CollectLocalLANSubnets inspects active non-loopback, non-TUN interfaces and
// returns their IPv4 subnets so split-TUN routes never hijack local Wi-Fi/Ethernet LAN.
func CollectLocalLANSubnets() []netaddr.IPPrefix {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netaddr.IPPrefix
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		lower := strings.ToLower(iface.Name)
		if strings.HasPrefix(lower, "utun") ||
			strings.HasPrefix(lower, "tun") ||
			strings.HasPrefix(lower, "tap") ||
			strings.HasPrefix(lower, "awdl") ||
			strings.HasPrefix(lower, "llw") ||
			strings.HasPrefix(lower, "anpi") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			ones, bits := ipNet.Mask.Size()
			if bits != 32 || ones <= 0 || ones > 32 {
				continue
			}
			// Guard against weird /0 or /8 masks on virtual interfaces; only protect /16..32 LANs.
			if ones < 16 {
				continue
			}
			if addr, ok := netaddr.FromStdIP(ip4); ok {
				out = append(out, netaddr.IPPrefixFrom(addr, uint8(ones)).Masked())
			}
		}
	}
	return out
}

func parseIPv4PrefixOrHost(raw string) (netaddr.IPPrefix, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netaddr.IPPrefix{}, false
	}
	if strings.Contains(s, "/") {
		p, err := netaddr.ParseIPPrefix(s)
		if err != nil || !p.IP().Is4() {
			return netaddr.IPPrefix{}, false
		}
		return p.Masked(), true
	}
	ip, err := netaddr.ParseIP(s)
	if err != nil || !ip.Is4() {
		return netaddr.IPPrefix{}, false
	}
	return netaddr.IPPrefixFrom(ip, 32), true
}

func bisectPrefix(p netaddr.IPPrefix) (netaddr.IPPrefix, netaddr.IPPrefix, bool) {
	if !p.IP().Is4() || p.Bits() >= 32 {
		return netaddr.IPPrefix{}, netaddr.IPPrefix{}, false
	}
	p = p.Masked()
	newBits := p.Bits() + 1
	left := netaddr.IPPrefixFrom(p.IP(), newBits)

	raw := p.IP().As4()
	u := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
	u |= 1 << (32 - newBits)
	rightIP := netaddr.IPv4(byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
	right := netaddr.IPPrefixFrom(rightIP, newBits)
	return left, right, true
}
