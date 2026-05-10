//go:build linux

package renumber

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// RASource watches the upstream interface for IPv6 address changes and emits
// the currently-selected global prefix.
type RASource struct {
	UpstreamIf string
	out        chan *net.IPNet
	current    *net.IPNet
}

func NewRASource(upstream string) *RASource {
	return &RASource{UpstreamIf: upstream, out: make(chan *net.IPNet, 1)}
}

func (s *RASource) Run(ctx context.Context) <-chan *net.IPNet {
	go s.run(ctx)
	return s.out
}

func (s *RASource) Stop() {}

func (s *RASource) run(ctx context.Context) {
	// Ensure accept_ra and forwarding are sane.
	_, _ = EnsureSysctl("net.ipv6.conf.all.forwarding", "1")
	_, _ = EnsureSysctl("net.ipv6.conf."+s.UpstreamIf+".accept_ra", "2")

	// Initial poll.
	if p := s.poll(); p != nil {
		s.current = p
		select {
		case s.out <- p:
		case <-ctx.Done():
			return
		}
	}

	for {
		if err := s.subscribeOnce(ctx); err != nil {
			slog.Warn("ra: subscribe error, retrying", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *RASource) subscribeOnce(ctx context.Context) error {
	updates := make(chan netlink.AddrUpdate, 16)
	doneSub := make(chan struct{})
	if err := netlink.AddrSubscribeWithOptions(updates, doneSub, netlink.AddrSubscribeOptions{
		ListExisting: false,
	}); err != nil {
		close(doneSub)
		return err
	}
	defer close(doneSub)

	link, err := netlink.LinkByName(s.UpstreamIf)
	if err != nil {
		return err
	}
	upstreamIdx := link.Attrs().Index

	for {
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-updates:
			if !ok {
				return nil
			}
			if u.LinkIndex != upstreamIdx {
				continue
			}
			if u.LinkAddress.IP.To4() != nil {
				continue
			}
			next := s.poll()
			if next == nil {
				continue
			}
			if s.current != nil && cidrEqual(s.current, next) {
				continue
			}
			s.current = next
			select {
			case s.out <- next:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func (s *RASource) poll() *net.IPNet {
	link, err := netlink.LinkByName(s.UpstreamIf)
	if err != nil {
		return nil
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V6)
	if err != nil {
		return nil
	}
	now := time.Now()
	var cands []Candidate
	for _, a := range addrs {
		if a.Flags&unix.IFA_F_TEMPORARY != 0 {
			continue
		}
		if a.Scope != int(netlink.SCOPE_UNIVERSE) {
			continue
		}
		var validUntil time.Time
		if a.ValidLft > 0 && a.ValidLft != 0xffffffff {
			validUntil = now.Add(time.Duration(a.ValidLft) * time.Second)
		} else {
			validUntil = now.Add(24 * time.Hour) // treat permanent as far future
		}
		prefix := &net.IPNet{
			IP:   a.IP.Mask(net.CIDRMask(64, 128)),
			Mask: net.CIDRMask(64, 128),
		}
		cands = append(cands, Candidate{Prefix: prefix, ValidUntil: validUntil})
	}
	return Select(cands, s.current)
}
