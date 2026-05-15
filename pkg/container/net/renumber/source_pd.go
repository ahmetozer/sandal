//go:build linux

package renumber

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/dhcp"
)

// PDSource runs a DHCPv6-PD client on the upstream interface and emits a
// sub-allocated /64 each time the delegation changes.
type PDSource struct {
	UpstreamIf string
	HintLen    string // SANDAL_IPV6_PD_HINT — empty = no hint

	out         chan *net.IPNet
	client      *dhcp.Client6
	lease       *dhcp.PDLease
	releaseOnce sync.Once
}

func NewPDSource(upstream, hintLen string) *PDSource {
	return &PDSource{UpstreamIf: upstream, HintLen: hintLen, out: make(chan *net.IPNet, 1)}
}

func (s *PDSource) Run(ctx context.Context) <-chan *net.IPNet {
	go s.run(ctx)
	return s.out
}

// Stop releases any in-flight DHCPv6-PD lease. Safe to call from multiple
// goroutines — only the first call actually issues a Release (F6). Both
// Service.Run's deferred Source.Stop and the lease loop's deferred Release
// (via pdRefresher.Release) route through here.
func (s *PDSource) Stop() {
	s.releaseOnce.Do(func() {
		if s.lease != nil && s.client != nil {
			_ = s.client.ReleasePDLease(s.lease)
		}
	})
}

func (s *PDSource) run(ctx context.Context) {
	for {
		if err := s.cycle(ctx); err != nil {
			slog.Warn("pd: cycle failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func (s *PDSource) cycle(ctx context.Context) error {
	client, err := dhcp.NewClient6(s.UpstreamIf)
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}
	s.client = client
	var hint *dhcp.PrefixHint
	if s.HintLen != "" {
		v, err := strconv.ParseUint(s.HintLen, 10, 8)
		switch {
		case err != nil:
			slog.Warn("pd: invalid SANDAL_IPV6_PD_HINT, ignoring", "value", s.HintLen, "err", err)
		case v == 0 || v > 128:
			slog.Warn("pd: SANDAL_IPV6_PD_HINT out of range (1..128), ignoring", "value", v)
		default:
			hint = &dhcp.PrefixHint{Length: uint8(v)}
		}
	}
	solicit, cancel := context.WithTimeout(ctx, 30*time.Second)
	lease, err := client.ObtainPDLease(solicit, hint)
	cancel()
	if err != nil {
		return fmt.Errorf("obtain pd: %w", err)
	}
	s.lease = lease
	if err := s.publish(ctx, lease.Prefix); err != nil {
		return err
	}

	loop := dhcp.NewLeaseLoop(&pdRefresher{src: s}, dhcp.LeaseTimes{T1: lease.T1, T2: lease.T2, Valid: lease.ValidLifetime})
	loop.Run(ctx)
	return nil
}

func (s *PDSource) publish(ctx context.Context, delegated *net.IPNet) error {
	sub := subAllocate64(delegated)
	if sub == nil {
		return fmt.Errorf("pd: cannot sub-allocate /64 from %s", delegated)
	}
	// Drain-and-replace: never block the lease loop on a slow consumer (F8).
	// If a stale prefix is parked in the cap-1 channel, drop it; the next
	// reader gets the latest value.
	for {
		select {
		case s.out <- sub:
			return nil
		case <-s.out:
			// drop stale value, retry send
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// subAllocate64 picks a deterministic /64 inside `delegated`. Default: the
// first /64 inside the delegation (delegated.IP truncated to /64).
func subAllocate64(delegated *net.IPNet) *net.IPNet {
	if delegated == nil {
		return nil
	}
	ones, _ := delegated.Mask.Size()
	if ones > 64 {
		return nil
	}
	return &net.IPNet{IP: delegated.IP.To16(), Mask: net.CIDRMask(64, 128)}
}

type pdRefresher struct {
	src *PDSource
}

func (r *pdRefresher) Renew(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	newLease, err := r.src.client.RenewPDLease(ctx, r.src.lease)
	if err != nil {
		return 0, 0, 0, err
	}
	if !cidrEqual(newLease.Prefix, r.src.lease.Prefix) {
		_ = r.src.publish(ctx, newLease.Prefix)
	}
	r.src.lease = newLease
	return newLease.T1, newLease.T2, newLease.ValidLifetime, nil
}

func (r *pdRefresher) Rebind(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	newLease, err := r.src.client.RebindPDLease(ctx, r.src.lease)
	if err != nil {
		return 0, 0, 0, err
	}
	if !cidrEqual(newLease.Prefix, r.src.lease.Prefix) {
		_ = r.src.publish(ctx, newLease.Prefix)
	}
	r.src.lease = newLease
	return newLease.T1, newLease.T2, newLease.ValidLifetime, nil
}

func (r *pdRefresher) Release() error {
	// Route through the same sync.Once gate as PDSource.Stop so Service
	// shutdown and lease-loop deferred Release cannot double-Release (F6).
	r.src.Stop()
	return nil
}
