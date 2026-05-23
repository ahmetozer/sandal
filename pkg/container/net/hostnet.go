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

const v4Placeholder = "%v4%"

// upstreamIPv4Fn is indirected so tests can stub the netlink path.
var upstreamIPv4Fn = upstreamIPv4

// ResolveHostNet replaces "%v4%" tokens in s with the upstream interface's
// IPv4 written as two colon-separated hex hextets, in zero-suppressed form
// (e.g. 192.168.1.15 -> "c0a8:10f"). When no IPv4 is available, substitutes
// "0:0" and emits a warning so the bridge falls back to today's static
// behaviour. Returns s unchanged when no placeholder is present.
func ResolveHostNet(s string) string {
	if !strings.Contains(s, v4Placeholder) {
		return s
	}
	ip := upstreamIPv4Fn()
	if ip == nil {
		slog.Warn("hostnet: no upstream IPv4 found; %v4% expanded to 0:0")
	}
	return strings.ReplaceAll(s, v4Placeholder, ipv4ToHextets(ip))
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
