//go:build linux

package renumber

import (
	"net"
	"time"
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
