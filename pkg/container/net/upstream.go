//go:build linux

package net

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// DetectUpstream returns the name of the interface carrying the default
// route to the public internet. It asks the kernel which interface it would
// use to reach a known public IPv6 address; if no IPv6 default route exists,
// it falls back to the IPv4 default route. Returns an error when neither
// family has a usable default route.
func DetectUpstream() (string, error) {
	if name, err := defaultRouteLink(Ipv6DefaultGatewayTestIp()); err == nil {
		return name, nil
	}
	if name, err := defaultRouteLink(Ipv4DefaultGatewayTestIp()); err == nil {
		return name, nil
	}
	return "", fmt.Errorf("no default IPv6 or IPv4 route found")
}

func defaultRouteLink(probe []byte) (string, error) {
	routes, err := netlink.RouteGet(probe)
	if err != nil {
		return "", err
	}
	for _, r := range routes {
		if r.LinkIndex == 0 {
			continue
		}
		link, err := netlink.LinkByIndex(r.LinkIndex)
		if err != nil {
			return "", err
		}
		return link.Attrs().Name, nil
	}
	return "", fmt.Errorf("no route for %s", probe)
}

// IPv6ModeSignals captures the observable state of the upstream interface
// that the auto-detector inspects. Extracted from DetectIPv6Mode so the
// decision logic can be unit-tested without a live netlink subscriber.
type IPv6ModeSignals struct {
	// HasGlobal is true when the upstream interface holds at least one
	// global-unicast, non-ULA IPv6 address (typical SLAAC outcome or a
	// statically-pinned WAN address).
	HasGlobal bool
	// HasShortDelegation is true when at least one route via the upstream
	// interface points at a prefix shorter than /64 (e.g. a /56 or /60)
	// that is NOT a kernel-installed connected route. DHCPv6-PD clients
	// install such routes; pure-RA setups do not.
	HasShortDelegation bool
}

// DecideIPv6Mode applies the heuristic that maps observed upstream state to
// a SANDAL_IPV6_MODE value. Returns one of "ndp-proxy", "pd", or "off".
//
// Rules, in order:
//
//  1. A short (less-than-/64) routed delegation on the upstream → "pd".
//  2. Any global unicast address on the upstream → "ndp-proxy".
//  3. Neither → "ndp-proxy" still, on the assumption that an RA is on the
//     way and the operator wanted dynamic IPv6 (they set or auto-detected
//     SANDAL_UPSTREAM_IF). The renumber service stays dormant until eth0
//     gets a global; that's the correct fail-soft behavior. Use
//     SANDAL_IPV6_MODE=off to explicitly disable.
func DecideIPv6Mode(s IPv6ModeSignals) string {
	if s.HasShortDelegation {
		return "pd"
	}
	return "ndp-proxy"
}

// DetectIPv6Mode inspects the upstream interface's IPv6 state and returns
// the recommended SANDAL_IPV6_MODE. Best-effort: unrecoverable netlink
// errors fall through to "ndp-proxy" (the safe default).
func DetectIPv6Mode(upstreamIf string) string {
	link, err := netlink.LinkByName(upstreamIf)
	if err != nil {
		return "ndp-proxy"
	}

	var sig IPv6ModeSignals

	addrs, _ := netlink.AddrList(link, netlink.FAMILY_V6)
	for _, a := range addrs {
		if !a.IP.IsGlobalUnicast() {
			continue
		}
		if isULAv6(a.IP) {
			continue
		}
		sig.HasGlobal = true
		break
	}

	routes, _ := netlink.RouteList(link, netlink.FAMILY_V6)
	for _, r := range routes {
		if r.Dst == nil {
			continue
		}
		ones, _ := r.Dst.Mask.Size()
		// PD delegations are typically /48../60 (occasionally up to /63).
		// Reject default routes (ones == 0) and anything /64 or longer.
		if ones <= 0 || ones >= 64 {
			continue
		}
		// Kernel-installed connected routes, RA-installed routes, and
		// ICMP redirects are not PD. DHCPv6-PD clients usually install
		// with RTPROT_DHCP, RTPROT_STATIC, or RTPROT_BOOT.
		switch r.Protocol {
		case unix.RTPROT_KERNEL, unix.RTPROT_RA, unix.RTPROT_REDIRECT:
			continue
		}
		sig.HasShortDelegation = true
		break
	}

	return DecideIPv6Mode(sig)
}

// isULAv6 returns true for fc00::/7 unique-local addresses.
func isULAv6(ip net.IP) bool {
	_, ula, _ := net.ParseCIDR("fc00::/7")
	return ula.Contains(ip)
}
