//go:build linux

package renumber

import (
	"bytes"
	"net"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Candidate is one global IPv6 prefix observed on the upstream interface.
type Candidate struct {
	Prefix     *net.IPNet
	ValidUntil time.Time
}

// Select picks the prefix to mirror onto sandal0. Rules:
//  1. Filter out link-local (fe80::/10) and ULA (fc00::/7) — only public globals.
//  2. If `current` is still in the candidate set, keep it (sticky).
//  3. Otherwise pick the candidate with the longest remaining valid lifetime.
//  4. Tie-break on lexicographic order of the prefix bytes.
func Select(cands []Candidate, current *net.IPNet) *net.IPNet {
	var globals []Candidate
	for _, c := range cands {
		if c.Prefix == nil {
			continue
		}
		if isLinkLocal(c.Prefix.IP) || isULA(c.Prefix.IP) {
			continue
		}
		globals = append(globals, c)
	}
	if len(globals) == 0 {
		return nil
	}
	if current != nil {
		for _, c := range globals {
			if cidrEqual(c.Prefix, current) {
				return c.Prefix
			}
		}
	}
	best := globals[0]
	for _, c := range globals[1:] {
		if c.ValidUntil.After(best.ValidUntil) {
			best = c
			continue
		}
		if c.ValidUntil.Equal(best.ValidUntil) && cidrLess(c.Prefix, best.Prefix) {
			best = c
		}
	}
	return best.Prefix
}

func isLinkLocal(ip net.IP) bool {
	_, ll, _ := net.ParseCIDR("fe80::/10")
	return ll.Contains(ip)
}

func isULA(ip net.IP) bool {
	_, ula, _ := net.ParseCIDR("fc00::/7")
	return ula.Contains(ip)
}

// isGlobal reports a global unicast IPv6: not IPv4, link-local or ULA.
func isGlobal(ip net.IP) bool {
	return ip.To4() == nil && !isLinkLocal(ip) && !isULA(ip)
}

// iidEqual compares the interface identifiers (low 64 bits) of two IPv6s.
func iidEqual(a, b net.IP) bool {
	a16, b16 := a.To16(), b.To16()
	return a16 != nil && b16 != nil && bytes.Equal(a16[8:], b16[8:])
}

func cidrEqual(a, b *net.IPNet) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !a.IP.Equal(b.IP) {
		return false
	}
	ao, _ := a.Mask.Size()
	bo, _ := b.Mask.Size()
	return ao == bo
}

func cidrLess(a, b *net.IPNet) bool {
	for i := 0; i < len(a.IP) && i < len(b.IP); i++ {
		if a.IP[i] != b.IP[i] {
			return a.IP[i] < b.IP[i]
		}
	}
	ao, _ := a.Mask.Size()
	bo, _ := b.Mask.Size()
	return ao < bo
}

// routeCandidates extracts upstream prefixes from RA-installed on-link
// routes. An RA prefix option with L=1 makes the kernel install
// "<prefix>/64 dev <uplink>" as a proto-kernel connected route even when
// A=0 (stateful-DHCPv6-only networks). Netlink dumps expose neither
// RTM_F_PREFIX nor the route lifetime, so any proto-kernel global /64 on the
// uplink counts; when the uplink also holds an address in that /64 it is the
// same prefix the address yields. User-added static routes (proto
// boot/static) are excluded. The kernel removes RA routes on expiry.
func routeCandidates(routes []netlink.Route, now time.Time) []Candidate {
	var out []Candidate
	for _, r := range routes {
		if r.Dst == nil || r.Gw != nil {
			continue
		}
		if r.Protocol != unix.RTPROT_KERNEL {
			continue
		}
		if ones, bits := r.Dst.Mask.Size(); ones != 64 || bits != 128 {
			continue
		}
		if !isGlobal(r.Dst.IP) {
			continue
		}
		out = append(out, Candidate{
			Prefix:     &net.IPNet{IP: r.Dst.IP.Mask(r.Dst.Mask), Mask: r.Dst.Mask},
			ValidUntil: now.Add(24 * time.Hour),
		})
	}
	return out
}
