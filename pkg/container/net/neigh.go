//go:build linux

package net

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// FlushNeighForIPs deletes any entry in the kernel neighbor table on iface
// whose IP matches one of ips. Missing entries (ENOENT) are not errors. The
// list is scanned for both AF_INET and AF_INET6 families in one pass so
// callers don't need to know which family each IP belongs to.
//
// Use case: when a container is restarted reusing its previous IPv4/IPv6,
// the host's kernel may still hold STALE/FAILED entries against the dead
// veth's MAC. Flushing those before the new veth comes up prevents a
// brief window where host→container traffic black-holes.
func FlushNeighForIPs(iface string, ips []net.IP) error {
	if iface == "" || len(ips) == 0 {
		return nil
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("flush-neigh: link %q: %w", iface, err)
	}
	idx := link.Attrs().Index

	want := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		want[ip.String()] = struct{}{}
	}
	if len(want) == 0 {
		return nil
	}

	// FAMILY_ALL returns both v4 and v6 in one call.
	entries, err := netlink.NeighList(idx, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("flush-neigh: list %s: %w", iface, err)
	}
	for i := range entries {
		e := &entries[i]
		if e.IP == nil {
			continue
		}
		if _, ok := want[e.IP.String()]; !ok {
			continue
		}
		if err := netlink.NeighDel(e); err != nil && err != unix.ENOENT {
			return fmt.Errorf("flush-neigh: del %s on %s: %w", e.IP, iface, err)
		}
	}
	return nil
}
