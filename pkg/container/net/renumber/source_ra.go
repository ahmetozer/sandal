//go:build linux

package renumber

import (
	"context"
	"log/slog"
	"net"
	"sync"
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
	// Host sysctls (accept_ra, forwarding, proxy_ndp) are configured
	// centrally in daemon/start.go via renumber.ApplyHostSysctls before
	// this goroutine starts.

	for {
		// Poll on every iteration — not just at startup — so that any
		// prefix change that landed during a netlink-subscription gap
		// (ENOBUFS, transient subscribe failure, retry sleep) is picked
		// up on the next pass.
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
		// re-subscribe; the outer loop's re-poll then catches up on any
		// prefix change that occurred during the blind window.
		ErrorCallback: func(err error) {
			slog.Warn("ra: netlink subscription error", "err", err)
			closeDone()
		},
	}); err != nil {
		closeDone()
		return err
	}
	defer closeDone()

	routeUpdates := make(chan netlink.RouteUpdate, 16)
	if err := netlink.RouteSubscribeWithOptions(routeUpdates, doneSub, netlink.RouteSubscribeOptions{
		ErrorCallback: func(err error) {
			slog.Warn("ra: netlink route subscription error", "err", err)
			closeDone()
		},
	}); err != nil {
		closeDone()
		return err
	}

	for {
		var linkIndex int
		select {
		case <-ctx.Done():
			return nil
		case u, ok := <-updates:
			if !ok {
				return nil
			}
			if u.LinkAddress.IP.To4() != nil {
				continue
			}
			linkIndex = u.LinkIndex
		case r, ok := <-routeUpdates:
			if !ok {
				return nil
			}
			if r.Dst == nil || r.Dst.IP.To4() != nil {
				continue
			}
			linkIndex = r.LinkIndex
		}
		// Resolve per event: the uplink may appear after we subscribe or be
		// recreated with a new index.
		link, err := netlink.LinkByName(s.UpstreamIf)
		if err != nil || linkIndex != link.Attrs().Index {
			continue
		}
		next := s.poll()
		if next == nil || (s.current != nil && cidrEqual(s.current, next)) {
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
		if a.ValidLft > 0 && uint32(a.ValidLft) != 0xffffffff {
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
	if routes, err := netlink.RouteList(link, netlink.FAMILY_V6); err == nil {
		cands = append(cands, routeCandidates(routes, now)...)
	}
	return Select(cands, s.current)
}
