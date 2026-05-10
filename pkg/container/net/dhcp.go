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

	// Spawn the renewal loop in a detached goroutine. The loop's lifetime is
	// the container's PID 1 — process exit cancels via container teardown.
	loopCtx, loopCancel := context.WithCancel(context.Background())
	registerDHCPv6LoopCancel(l.Id, loopCancel)

	r := newNARefresher(client, nLink, lease, l.Id)
	loop := dhcp.NewLeaseLoop(r, dhcp.LeaseTimes{T1: lease.T1, T2: lease.T2, Valid: lease.ValidLifetime})
	go loop.Run(loopCtx)

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
