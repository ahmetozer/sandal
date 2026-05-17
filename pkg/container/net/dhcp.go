//go:build linux

package net

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/dhcp"
	"github.com/vishvananda/netlink"
)

// configureDHCP runs DHCPv4 and/or DHCPv6 on the link's interface,
// updating l.Addr and l.Route with the obtained lease information.
func (l *Link) configureDHCP() error {
	if l.DHCPv4 {
		if err := l.configureDHCPv4(); err != nil {
			return fmt.Errorf("dhcpv4: %w", err)
		}
	}
	if l.DHCPv6 {
		if err := l.configureDHCPv6(); err != nil {
			// DHCPv6 failure is non-fatal — log and continue
			// (many networks don't have DHCPv6, IPv6 may come from SLAAC)
			slog.Warn("dhcpv6 failed, skipping", "interface", l.Id, "err", err)
		}
	}
	return nil
}

func (l *Link) configureDHCPv4() error {
	client, err := dhcp.NewClient(l.Id)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lease, err := client.ObtainLease(ctx)
	if err != nil {
		return err
	}

	slog.Debug("dhcpv4 lease obtained", "interface", l.Id, "ip", lease.CIDR(), "router", lease.Router, "dns", lease.DNS)

	// Apply the obtained IP to the interface
	nLink, err := netlink.LinkByName(l.Id)
	if err != nil {
		return err
	}

	addr := Addr{
		IP:    lease.ClientIP,
		IPNet: lease.IPNet(),
	}
	if err := addr.Add(nLink); err != nil {
		return fmt.Errorf("adding dhcp address: %w", err)
	}
	l.Addr = append(l.Addr, addr)

	// Set router as gateway for FindGateways()
	if lease.Router != nil {
		l.Route = append(l.Route, Addr{
			IP:    lease.Router,
			IPNet: lease.IPNet(),
		})
	}

	return nil
}

func (l *Link) configureDHCPv6() error {
	client, err := dhcp.NewClient6(l.Id)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lease, err := client.ObtainLease(ctx)
	if err != nil {
		return err
	}

	slog.Info("dhcpv6 lease obtained", "interface", l.Id, "ip", lease.CIDR(), "dns", lease.DNS,
		"t1", lease.T1, "t2", lease.T2, "valid", lease.ValidLifetime)

	nLink, err := netlink.LinkByName(l.Id)
	if err != nil {
		return err
	}

	addr := Addr{
		IP:    lease.ClientIP,
		IPNet: lease.IPNet(),
	}
	if err := addr.Add(nLink); err != nil {
		return fmt.Errorf("adding dhcpv6 address: %w", err)
	}
	l.Addr = append(l.Addr, addr)

	// Spawn a self-restarting renewal supervisor. If the lease loop exits
	// (lease fully expired), the supervisor reattempts ObtainLease with
	// exponential-ish backoff. Cancelled from CancelAllDHCPv6Loops.
	loopCtx, loopCancel := context.WithCancel(context.Background())
	registerDHCPv6LoopCancel(l.Id, loopCancel)

	go func(initialLease *dhcp.Lease6) {
		current := initialLease
		backoff := 5 * time.Second
		for {
			r := newNARefresher(client, nLink, current, l.Id)
			loop := dhcp.NewLeaseLoop(r, dhcp.LeaseTimes{T1: current.T1, T2: current.T2, Valid: current.ValidLifetime})
			loop.Run(loopCtx)
			if loopCtx.Err() != nil {
				return
			}
			// Loop exited due to expiry. Sleep, then re-Solicit.
			select {
			case <-loopCtx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			solicitCtx, cancel := context.WithTimeout(loopCtx, 30*time.Second)
			next, err := client.ObtainLease(solicitCtx)
			cancel()
			if err != nil {
				slog.Warn("dhcp6: re-solicit failed", "iface", l.Id, "err", err)
				continue
			}
			// New lease obtained — replace kernel addr if changed.
			if !current.ClientIP.Equal(next.ClientIP) {
				old := &netlink.Addr{IPNet: &net.IPNet{IP: current.ClientIP, Mask: net.CIDRMask(128, 128)}}
				_ = netlink.AddrDel(nLink, old)
			}
			add := &netlink.Addr{IPNet: &net.IPNet{IP: next.ClientIP, Mask: net.CIDRMask(128, 128)}}
			if err := netlink.AddrAdd(nLink, add); err != nil {
				slog.Warn("dhcp6: addr add after re-solicit", "iface", l.Id, "err", err)
			}
			current = next
			backoff = 5 * time.Second
		}
	}(lease)

	return nil
}

// ResolvDHCP returns DNS server addresses from both v4 and v6 leases
// formatted for /etc/resolv.conf. This is available for future use.
func (l *Link) ResolvDHCP() []net.IP {
	// DNS servers are embedded in the lease during ObtainLease;
	// they're not stored on the Link currently.
	// This is a placeholder for when DNS propagation is added.
	return nil
}

var (
	dhcpv6LoopMu      sync.Mutex
	dhcpv6LoopCancels = make(map[string]context.CancelFunc)
)

func registerDHCPv6LoopCancel(iface string, cancel context.CancelFunc) {
	dhcpv6LoopMu.Lock()
	defer dhcpv6LoopMu.Unlock()
	if old, ok := dhcpv6LoopCancels[iface]; ok {
		old()
	}
	dhcpv6LoopCancels[iface] = cancel
}

// CancelAllDHCPv6Loops cancels every running DHCPv6 lease loop in this process.
// Called from container shutdown so loops exit and send Release.
func CancelAllDHCPv6Loops() {
	dhcpv6LoopMu.Lock()
	defer dhcpv6LoopMu.Unlock()
	for iface, cancel := range dhcpv6LoopCancels {
		cancel()
		delete(dhcpv6LoopCancels, iface)
	}
}
