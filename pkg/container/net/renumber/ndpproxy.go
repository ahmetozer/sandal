//go:build linux

package renumber

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NDPProxy manages NDP proxy entries on an upstream interface AND the
// matching /128 host routes that direct return-path traffic into the bridge
// instead of looping back out the upstream.
//
// Both entries are added/removed together so the kernel always has a
// coherent picture: proxy answers NS on behalf of containers, and the host
// route ensures forwarded packets to those addresses go out the bridge.
//
// Interface indices are looked up per-operation rather than cached at
// construct time so we survive a bridge or upstream interface being
// destroyed and recreated.
type NDPProxy struct {
	upstream string // upstream interface name (e.g. eth0)
	bridge   string // bridge interface name (e.g. sandal0)
}

// NewNDPProxy constructs a manager for the named upstream interface. Both
// link existences are verified up-front; the proxy_ndp sysctl is configured
// centrally in daemon/start.go via renumber.ApplyHostSysctls and is not
// re-applied here.
func NewNDPProxy(upstream, bridge string) (*NDPProxy, error) {
	if _, err := netlink.LinkByName(upstream); err != nil {
		return nil, fmt.Errorf("ndpproxy: link %q: %w", upstream, err)
	}
	if _, err := netlink.LinkByName(bridge); err != nil {
		return nil, fmt.Errorf("ndpproxy: bridge link %q: %w", bridge, err)
	}
	return &NDPProxy{upstream: upstream, bridge: bridge}, nil
}

// upstreamIndex returns the current kernel index for the upstream interface.
// Looked up per call so interface recreation doesn't strand stale indices.
func (p *NDPProxy) upstreamIndex() (int, error) {
	l, err := netlink.LinkByName(p.upstream)
	if err != nil {
		return 0, fmt.Errorf("ndpproxy: link %q: %w", p.upstream, err)
	}
	return l.Attrs().Index, nil
}

// bridgeIndex is the per-call lookup for the bridge interface, same rationale.
func (p *NDPProxy) bridgeIndex() (int, error) {
	l, err := netlink.LinkByName(p.bridge)
	if err != nil {
		return 0, fmt.Errorf("ndpproxy: bridge link %q: %w", p.bridge, err)
	}
	return l.Attrs().Index, nil
}

// Add installs a proxy entry for ip on the upstream interface AND a /128 host
// route via the bridge so return-path traffic reaches the container.
func (p *NDPProxy) Add(ip net.IP) error {
	upIdx, err := p.upstreamIndex()
	if err != nil {
		return err
	}
	brIdx, err := p.bridgeIndex()
	if err != nil {
		return err
	}
	n := &netlink.Neigh{
		LinkIndex: upIdx,
		Family:    unix.AF_INET6,
		Flags:     netlink.NTF_PROXY,
		IP:        ip.To16(),
		State:     netlink.NUD_PERMANENT,
	}
	if err := netlink.NeighAdd(n); err != nil && err != unix.EEXIST {
		return fmt.Errorf("ndpproxy: add %s: %w", ip, err)
	}

	route := &netlink.Route{
		LinkIndex: brIdx,
		Dst: &net.IPNet{
			IP:   ip.To16(),
			Mask: net.CIDRMask(128, 128),
		},
	}
	if err := netlink.RouteAdd(route); err != nil && err != unix.EEXIST {
		return fmt.Errorf("ndpproxy: add route %s: %w", ip, err)
	}
	return nil
}

// Remove deletes the proxy entry and the /128 host route. Missing entries
// (proxy or route) are not an error.
func (p *NDPProxy) Remove(ip net.IP) error {
	upIdx, err := p.upstreamIndex()
	if err != nil {
		return err
	}
	brIdx, err := p.bridgeIndex()
	if err != nil {
		return err
	}
	n := &netlink.Neigh{
		LinkIndex: upIdx,
		Family:    unix.AF_INET6,
		Flags:     netlink.NTF_PROXY,
		IP:        ip.To16(),
	}
	if err := netlink.NeighDel(n); err != nil && err != unix.ENOENT {
		return fmt.Errorf("ndpproxy: del %s: %w", ip, err)
	}

	route := &netlink.Route{
		LinkIndex: brIdx,
		Dst: &net.IPNet{
			IP:   ip.To16(),
			Mask: net.CIDRMask(128, 128),
		},
	}
	if err := netlink.RouteDel(route); err != nil && err != unix.ESRCH && err != unix.ENOENT {
		return fmt.Errorf("ndpproxy: del route %s: %w", ip, err)
	}
	return nil
}

// List returns all current proxy entries on the upstream interface. The
// proxy table is treated as the source of truth for the desired set; the
// /128 route table is kept in sync by Add/Remove and not separately listed.
func (p *NDPProxy) List() ([]net.IP, error) {
	upIdx, err := p.upstreamIndex()
	if err != nil {
		return nil, err
	}
	neighs, err := netlink.NeighProxyList(upIdx, unix.AF_INET6)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(neighs))
	for _, n := range neighs {
		if n.Flags&netlink.NTF_PROXY == 0 {
			continue
		}
		out = append(out, n.IP)
	}
	return out, nil
}

// Reconcile diffs the desired set against the current kernel state, adding
// missing entries and removing stale ones. Add/Remove keep proxy entries and
// /128 routes coherent. Per-entry errors are logged, not fatal.
func (p *NDPProxy) Reconcile(desired []net.IP) {
	current, err := p.List()
	if err != nil {
		slog.Warn("ndpproxy: list failed", "err", err)
		return
	}
	have := make(map[string]struct{}, len(current))
	for _, ip := range current {
		have[ip.String()] = struct{}{}
	}
	want := make(map[string]struct{}, len(desired))
	for _, ip := range desired {
		want[ip.String()] = struct{}{}
	}

	for _, ip := range desired {
		if _, ok := have[ip.String()]; !ok {
			if err := p.Add(ip); err != nil {
				slog.Warn("ndpproxy: add failed", "ip", ip, "err", err)
			}
		}
	}
	for _, ip := range current {
		if _, ok := want[ip.String()]; !ok {
			if err := p.Remove(ip); err != nil {
				slog.Warn("ndpproxy: remove failed", "ip", ip, "err", err)
			}
		}
	}
}
