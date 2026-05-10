//go:build linux

package net

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/dhcp"
	"github.com/vishvananda/netlink"
)

// naRefresher adapts dhcp.Client6 IA_NA Renew/Release into the LeaseRefresher
// contract used by the lease loop. It owns the lease state across iterations.
type naRefresher struct {
	client    *dhcp.Client6
	link      netlink.Link
	current   *dhcp.Lease6
	ifaceName string
}

func newNARefresher(client *dhcp.Client6, link netlink.Link, lease *dhcp.Lease6, ifaceName string) *naRefresher {
	return &naRefresher{client: client, link: link, current: lease, ifaceName: ifaceName}
}

func (r *naRefresher) Renew(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	newLease, err := r.client.RenewLease(ctx, r.current)
	if err != nil {
		return 0, 0, 0, err
	}
	r.applyAddrChange(newLease)
	r.current = newLease
	return newLease.T1, newLease.T2, newLease.ValidLifetime, nil
}

func (r *naRefresher) Rebind(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	// Existing client6.go has no RebindLease method; treat Rebind same as Renew.
	// Renew is unicast to ServerDUID; if that server is gone, we fail and the
	// loop exits with "lease expired" — caller can re-Solicit if it likes.
	return r.Renew(ctx)
}

func (r *naRefresher) Release() error {
	if r.current == nil {
		return nil
	}
	return r.client.ReleaseLease(r.current)
}

// applyAddrChange swaps the kernel address only when the lease IP changed.
func (r *naRefresher) applyAddrChange(newLease *dhcp.Lease6) {
	if r.current != nil && r.current.ClientIP.Equal(newLease.ClientIP) {
		return
	}
	if r.current != nil {
		old := &netlink.Addr{IPNet: &net.IPNet{IP: r.current.ClientIP, Mask: net.CIDRMask(128, 128)}}
		_ = netlink.AddrDel(r.link, old)
	}
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: newLease.ClientIP, Mask: net.CIDRMask(128, 128)}}
	if err := netlink.AddrAdd(r.link, addr); err != nil {
		slog.Warn("dhcp6: addr swap failed", "iface", r.ifaceName, "err", err)
	}
}
