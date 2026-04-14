//go:build linux

package build

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/lib/dhcp"
	"github.com/vishvananda/netlink"
)

// EnsureVMHostNetwork brings up eth0 inside a sandal macOS VM guest so
// the build (which runs in the VM but outside any container) can reach
// OCI registries and DNS.
//
// Scope: macOS VZ VMs only (SANDAL_VM="mac"). On linux/KVM VMs, static
// per-NIC addressing is handled by the container-network path and any
// base image is pre-pulled on the host via PullFromArgs, so the build
// path inside KVM doesn't need eth0 configured up front. On macOS the
// build can't rely on either: `sandal build` doesn't pre-pull on the
// host, and container-network setup only runs for RUN steps (per-step)
// — by the time those fire, the FROM pull has already failed.
//
// No-op outside a VM or on non-"mac" VMs. Also no-op if eth0 already
// has an IPv4 address (safe to call more than once).
func EnsureVMHostNetwork(ctx context.Context) error {
	if os.Getenv("SANDAL_VM") != "mac" {
		return nil
	}

	const iface = "eth0"
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("guest net: %s: %w", iface, err)
	}

	// Already has an IPv4 address? Assume configured.
	if addrs, _ := netlink.AddrList(link, netlink.FAMILY_V4); len(addrs) > 0 {
		return nil
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("guest net: link up %s: %w", iface, err)
	}

	client, err := dhcp.NewClient(iface)
	if err != nil {
		return fmt.Errorf("guest net: dhcp client: %w", err)
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lease, err := client.ObtainLease(dctx)
	if err != nil {
		return fmt.Errorf("guest net: dhcp: %w", err)
	}

	// Apply IP + gateway.
	addr := &netlink.Addr{IPNet: lease.IPNet()}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("guest net: assign addr %s: %w", lease.CIDR(), err)
	}
	if lease.Router != nil {
		gw := lease.Router
		if err := netlink.RouteAdd(&netlink.Route{Gw: gw}); err != nil {
			return fmt.Errorf("guest net: default route %s: %w", gw, err)
		}
	}

	// Write /etc/resolv.conf from the DHCP-provided nameservers (fallback
	// to public DNS if the lease didn't include any). This overwrites any
	// pre-existing file that was staged from the macOS host's resolv.conf.
	dns := lease.DNS
	if len(dns) == 0 {
		dns = []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("8.8.8.8")}
	}
	var b strings.Builder
	for _, ns := range dns {
		if ns == nil {
			continue
		}
		b.WriteString("nameserver ")
		b.WriteString(ns.String())
		b.WriteByte('\n')
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("guest net: write resolv.conf: %w", err)
	}

	slog.Info("guest net configured",
		slog.String("iface", iface),
		slog.String("addr", lease.CIDR()),
		slog.Any("gateway", lease.Router),
		slog.Int("dns", len(dns)))
	return nil
}
