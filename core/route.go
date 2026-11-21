package core

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// EvaluateRoute determines whether target (URL, host:port, domain, or IP)
// matches the server-pushed resource snapshot and will be routed through VPN.
func EvaluateRoute(snap ResourceSnapshot, rawTarget string, defaultPort int, network string) RouteCheckResult {
	res := RouteCheckResult{
		Input:   rawTarget,
		Network: strings.ToLower(strings.TrimSpace(network)),
	}
	if res.Network == "" {
		res.Network = "tcp"
	}
	if defaultPort <= 0 {
		defaultPort = 443
	}

	host, port := parseTargetHostPort(rawTarget, defaultPort)
	res.Host = host
	res.Port = port
	if host == "" {
		res.Reason = "invalid target address"
		return res
	}

	// Check IP directly if target is an IP literal.
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			for _, item := range snap.IPResources {
				if matchIPRange(ip4, item, res.Network, port) {
					res.ViaVPN = true
					res.Reason = "matched IP resource"
					res.MatchedRule = formatIPRule(item)
					return res
				}
			}
			if snap.ProxyAll {
				if len(snap.IPResources) > 0 {
					res.ViaVPN = false
					res.Reason = "global proxy enabled, but IP is outside server ACL whitelist"
					return res
				}
				res.ViaVPN = true
				res.Reason = "global proxy (proxy_all) enabled"
				res.MatchedRule = "proxy_all"
				return res
			}
			res.ViaVPN = false
			res.Reason = "direct (no matching IP resource)"
			return res
		}
		res.ViaVPN = false
		res.Reason = "direct (IPv6 traffic bypasses VPN)"
		return res
	}

	// Hostname: check domain resources.
	normHost := strings.TrimSuffix(strings.ToLower(host), ".")
	if matched, rule := matchDomainRule(normHost, snap.DomainResources, res.Network, port); matched {
		res.ViaVPN = true
		res.Reason = "matched domain resource"
		res.MatchedRule = rule
		return res
	}

	// Check DNS overrides -> IP resources.
	for _, ov := range snap.DNSOverrides {
		if strings.EqualFold(strings.TrimSuffix(ov.Host, "."), normHost) {
			for _, ipStr := range ov.IPs {
				if ip4 := net.ParseIP(ipStr).To4(); ip4 != nil {
					for _, item := range snap.IPResources {
						if matchIPRange(ip4, item, res.Network, port) {
							res.ViaVPN = true
							res.Reason = fmt.Sprintf("matched DNS override (%s) -> IP resource", ipStr)
							res.MatchedRule = formatIPRule(item)
							return res
						}
					}
				}
			}
		}
	}

	if snap.ProxyAll {
		res.ViaVPN = true
		res.Reason = "global proxy (proxy_all) enabled"
		res.MatchedRule = "proxy_all"
		return res
	}

	res.ViaVPN = false
	res.Reason = "direct (no matching domain or IP resource)"
	return res
}

func parseTargetHostPort(raw string, defaultPort int) (string, int) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", defaultPort
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			port := defaultPort
			if p := u.Port(); p != "" {
				if v, err := strconv.Atoi(p); err == nil && v > 0 {
					port = v
				}
			} else if strings.EqualFold(u.Scheme, "http") {
				port = 80
			} else if strings.EqualFold(u.Scheme, "https") {
				port = 443
			}
			return u.Hostname(), port
		}
	}
	// Strip path if user typed "example.com/foo" or "example.com:8080/foo"
	if idx := strings.IndexByte(s, '/'); idx >= 0 {
		s = s[:idx]
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		port := defaultPort
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			port = v
		}
		return h, port
	}
	return strings.Trim(s, "[]"), defaultPort
}

func matchIPRange(ip4 net.IP, item IPResourceItem, network string, port int) bool {
	if !protocolMatches(item.Protocol, network) {
		return false
	}
	if network != "icmp" && (port < item.PortMin || port > item.PortMax) {
		return false
	}
	minIP := net.ParseIP(item.IPMin).To4()
	maxIP := net.ParseIP(item.IPMax).To4()
	if minIP == nil || maxIP == nil {
		return false
	}
	return bytes.Compare(ip4, minIP) >= 0 && bytes.Compare(ip4, maxIP) <= 0
}

func matchDomainRule(host string, rules []DomainResourceItem, network string, port int) (bool, string) {
	for _, r := range rules {
		if !protocolMatches(r.Protocol, network) {
			continue
		}
		if network != "icmp" && (port < r.PortMin || port > r.PortMax) {
			continue
		}
		pattern := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.Domain)), ".")
		if pattern == "" {
			continue
		}
		// Match exact, leading wildcard (*.example.com), or domain suffix (example.com -> sub.example.com).
		clean := strings.TrimPrefix(pattern, "*.")
		clean = strings.TrimPrefix(clean, ".")
		if host == clean || strings.HasSuffix(host, "."+clean) {
			return true, fmt.Sprintf("%s (%s:%d-%d)", r.Domain, r.Protocol, r.PortMin, r.PortMax)
		}
	}
	return false, ""
}

func protocolMatches(ruleProto, network string) bool {
	p := strings.ToLower(strings.TrimSpace(ruleProto))
	return p == "" || p == "all" || p == network
}

func formatIPRule(item IPResourceItem) string {
	addr := item.CIDR
	if addr == "" {
		if item.IPMin == item.IPMax {
			addr = item.IPMin
		} else {
			addr = item.IPMin + "-" + item.IPMax
		}
	}
	return fmt.Sprintf("%s (%s:%d-%d)", addr, item.Protocol, item.PortMin, item.PortMax)
}
