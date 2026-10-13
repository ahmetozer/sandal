//go:build linux

package net

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

const (
	uv4Placeholder = "%uv4%"
	uv6Placeholder = "%uv6%"
)

// upstreamIPv4Fn is indirected so tests can stub the netlink path.
var upstreamIPv4Fn = upstreamIPv4

// SplitHostNet partitions SANDAL_HOST_NET into static and dynamic entries.
//
// Static entries (no "%uv6%" token) are returned with "%uv4%" already
// resolved — the renumber service can use them as-is to drive bridge state.
// Dynamic entries (containing "%uv6%") are returned in their raw template
// form so the renumber service can call ResolveDynamic on each upstream
// prefix event.
//
// When no upstream IPv4 is available, "%uv4%" expands to "0:0" and a warning
// is emitted once per call.
func SplitHostNet(hostNet string) (static []string, dynamic []string) {
	ipv4 := upstreamIPv4Fn()
	warned := false
	for _, part := range strings.Split(hostNet, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if isDynamicEntry(trimmed) {
			dynamic = append(dynamic, trimmed)
			continue
		}
		if strings.Contains(trimmed, uv4Placeholder) {
			if ipv4 == nil && !warned {
				slog.Warn("hostnet: no upstream IPv4 found; %uv4% expanded to 0:0")
				warned = true
			}
			trimmed = strings.ReplaceAll(trimmed, uv4Placeholder, ipv4ToHextets(ipv4))
		}
		static = append(static, trimmed)
	}
	return
}

// ResolveDynamic substitutes both "%uv4%" and "%uv6%" in a single template
// entry. The prefix argument is the upstream /64 (or sandal-carved /64 in
// PD mode). When prefix is nil, "%uv6%" expands to "::" so the resulting
// string is unroutable; callers MUST withhold the entry from sandal0 in
// that case instead of applying an unrouted global.
func ResolveDynamic(template string, prefix *net.IPNet) string {
	v6sub := "::"
	if prefix != nil {
		v6sub = prefixToHextets(prefix)
	}
	return strings.NewReplacer(
		uv4Placeholder, ipv4ToHextets(upstreamIPv4Fn()),
		uv6Placeholder, v6sub,
	).Replace(template)
}

// ipv4ToHextets formats an IPv4 address as two zero-suppressed hex hextets
// suitable for embedding in an IPv6 IID. Returns "0:0" for nil / non-IPv4.
func ipv4ToHextets(ip net.IP) string {
	if ip == nil {
		return "0:0"
	}
	v4 := ip.To4()
	if v4 == nil {
		return "0:0"
	}
	hi := uint16(v4[0])<<8 | uint16(v4[1])
	lo := uint16(v4[2])<<8 | uint16(v4[3])
	return fmt.Sprintf("%x:%x", hi, lo)
}

// prefixToHextets formats the upstream prefix's first 4 hextets (= high 64
// bits) as a colon-joined string without a trailing colon — designed so a
// template like "%uv6%:%uv4%::1/64" reads naturally after substitution.
func prefixToHextets(prefix *net.IPNet) string {
	if prefix == nil {
		return "::"
	}
	ip := prefix.IP.To16()
	if ip == nil {
		return "::"
	}
	return fmt.Sprintf("%x:%x:%x:%x",
		uint16(ip[0])<<8|uint16(ip[1]),
		uint16(ip[2])<<8|uint16(ip[3]),
		uint16(ip[4])<<8|uint16(ip[5]),
		uint16(ip[6])<<8|uint16(ip[7]),
	)
}

// upstreamIPv4 returns the first global, non-loopback, non-link-local IPv4
// on the upstream interface. Honours env.UpstreamInterface; falls back to
// the default-route interface via DetectUpstream. Returns nil if no
// candidate is available.
func upstreamIPv4() net.IP {
	iface := env.UpstreamInterface
	if iface == "" {
		if name, err := DetectUpstream(); err == nil {
			iface = name
		}
	}
	if iface == "" {
		return nil
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return nil
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		ip := a.IP.To4()
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		return ip
	}
	return nil
}

// dynamicAddrProbe is a stand-in prefix used only to validate syntax.
var dynamicAddrProbe = &net.IPNet{IP: net.ParseIP("2001:db8::"), Mask: net.CIDRMask(64, 128)}

// isDynamicEntry reports whether a SANDAL_HOST_NET entry or -net ip= value
// carries "%uv6%" and must be re-resolved on every upstream prefix event.
func isDynamicEntry(s string) bool {
	return strings.Contains(s, uv6Placeholder)
}

// parseDynamicAddr validates a container dynamic address from -net ip=
// (e.g. "%uv6%:192:0:2:23") and returns it without a mask. The renumber
// service picks the installed mask from the mode (/128 in ndp-proxy, the
// prefix /64 in pd), so the only mask accepted here is an explicit /128.
func parseDynamicAddr(s string) (string, error) {
	if !isDynamicEntry(s) {
		return "", fmt.Errorf("dynamic address %q: missing %s", s, uv6Placeholder)
	}
	if addr, mask, ok := strings.Cut(s, "/"); ok {
		if mask != "128" {
			return "", fmt.Errorf("dynamic address %q: mask must be omitted or /128", s)
		}
		s = addr
	}
	probe := strings.NewReplacer(
		uv6Placeholder, prefixToHextets(dynamicAddrProbe),
		uv4Placeholder, "0:0",
	).Replace(s)
	ip := net.ParseIP(probe)
	if ip == nil || ip.To4() != nil {
		return "", fmt.Errorf("dynamic address %q: not an IPv6 address", s)
	}
	if !dynamicAddrProbe.Contains(ip) {
		return "", fmt.Errorf("dynamic address %q: %s must fill the high 64 bits", s, uv6Placeholder)
	}
	return s, nil
}

// ResolveDynamicAddr resolves a container dynamic address against prefix.
func ResolveDynamicAddr(s string, prefix *net.IPNet) (net.IP, error) {
	if prefix == nil {
		return nil, fmt.Errorf("dynamic address %q: no upstream prefix", s)
	}
	ip := net.ParseIP(ResolveDynamic(s, prefix))
	if ip == nil {
		return nil, fmt.Errorf("dynamic address %q: invalid after resolve", s)
	}
	return ip, nil
}

// dynamicAddrFromIID returns the dynamic address that keeps ip's interface
// identifier (low 64 bits) under whatever upstream prefix is current.
func dynamicAddrFromIID(ip net.IP) string {
	v6 := ip.To16()
	return fmt.Sprintf("%s:%x:%x:%x:%x", uv6Placeholder,
		uint16(v6[8])<<8|uint16(v6[9]),
		uint16(v6[10])<<8|uint16(v6[11]),
		uint16(v6[12])<<8|uint16(v6[13]),
		uint16(v6[14])<<8|uint16(v6[15]),
	)
}

// staticBridgeAddrs drops sandal0 addresses that the renumber service
// manages (the global mirrored from upstream). They rotate with the
// upstream prefix, so they must never seed IPRequest or become a gateway;
// containers and VMs get their global from DynamicAddr instead. IPv4, ULA
// and addresses declared statically in SANDAL_HOST_NET are kept.
func staticBridgeAddrs(addrs Addrs, hostNet string) Addrs {
	static, _ := SplitHostNet(hostNet)
	keep := make(Addrs, 0, len(addrs))
	for _, a := range addrs {
		if !isRenumberManaged(a, static) {
			keep = append(keep, a)
		}
	}
	return keep
}

// bridgeDynamicPrefix returns the /64 of sandal0's renumber-managed global
// and that global's mask (/128 in ndp-proxy mode, the prefix mask in pd
// mode), or nil when the renumber service has not applied a prefix yet.
func bridgeDynamicPrefix(addrs Addrs, hostNet string) (*net.IPNet, net.IPMask) {
	static, _ := SplitHostNet(hostNet)
	for _, a := range addrs {
		if !isRenumberManaged(a, static) {
			continue
		}
		m64 := net.CIDRMask(64, 128)
		return &net.IPNet{IP: a.IP.Mask(m64), Mask: m64}, a.IPNet.Mask
	}
	return nil, nil
}

// isRenumberManaged reports whether a is a global IPv6 address that is not
// declared statically in SANDAL_HOST_NET.
func isRenumberManaged(a Addr, static []string) bool {
	if a.IP.To4() != nil || isULAv6(a.IP) || a.IP.IsLinkLocalUnicast() {
		return false
	}
	for _, c := range static {
		if cip, _, err := net.ParseCIDR(c); err == nil && cip.Equal(a.IP) {
			return false
		}
	}
	return true
}
