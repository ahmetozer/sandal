//go:build linux

package renumber

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/sysctl"
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
	if _, err := sysctl.Ensure("net.ipv6.conf.all.forwarding", "1"); err != nil {
		slog.Warn("ra: cannot enable IPv6 forwarding; ndp-proxy mode will not forward upstream traffic", "err", err)
	}
	if _, err := sysctl.Ensure("net.ipv6.conf."+s.UpstreamIf+".accept_ra", "2"); err != nil {
		slog.Warn("ra: cannot set accept_ra=2 on upstream interface", "iface", s.UpstreamIf, "err", err)
	}

	for {
		// Poll on every iteration — not just at startup — so that any
		// prefix change that landed during a netlink-subscription gap
		// (ENOBUFS, transient subscribe failure, retry sleep) is picked
		// up on the next pass (F12).
		if p := s.poll(); p != nil {
			if s.current == nil || !cidrEqual(s.current, p) {
				s.current = p
				select {
				case s.out <- p:
				case <-ctx.Done():
					return
				}
			}
		}
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
	// Use sync.Once to make closing doneSub idempotent: both the deferred
	// close below and the ErrorCallback (which closes to force re-subscribe
	// on ENOBUFS) may want to close it.
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(doneSub) }) }

	if err := netlink.AddrSubscribeWithOptions(updates, doneSub, netlink.AddrSubscribeOptions{
		ListExisting: false,
		// Surface netlink errors that the library would otherwise swallow
		// (e.g. ENOBUFS on socket overflow). Closing doneSub forces a
		// re-subscribe; the outer loop's re-poll (F12) then catches up on
		// any prefix change that occurred during the blind window (F13).
		ErrorCallback: func(err error) {
			slog.Warn("ra: netlink subscription error", "err", err)
			closeDone()
		},
	}); err != nil {
		closeDone()
		return err
	}
	defer closeDone()

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
