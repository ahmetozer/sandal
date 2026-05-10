//go:build linux

package renumber

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NDPProxy manages `ip -6 neigh proxy …` entries on a single upstream interface.
type NDPProxy struct {
	link netlink.Link
}

// NewNDPProxy constructs a manager for the named upstream interface and ensures
// proxy_ndp is enabled. Returns an error if the interface cannot be found.
func NewNDPProxy(upstream string) (*NDPProxy, error) {
	link, err := netlink.LinkByName(upstream)
	if err != nil {
		return nil, fmt.Errorf("ndpproxy: link %q: %w", upstream, err)
	}
	if _, err := EnsureSysctl("net.ipv6.conf."+upstream+".proxy_ndp", "1"); err != nil {
		return nil, fmt.Errorf("ndpproxy: enable proxy_ndp: %w", err)
	}
	return &NDPProxy{link: link}, nil
}

// Add installs a proxy entry for ip on the upstream interface.
func (p *NDPProxy) Add(ip net.IP) error {
	n := &netlink.Neigh{
		LinkIndex: p.link.Attrs().Index,
		Family:    unix.AF_INET6,
		Flags:     netlink.NTF_PROXY,
		IP:        ip.To16(),
		State:     netlink.NUD_PERMANENT,
	}
	if err := netlink.NeighAdd(n); err != nil && err != unix.EEXIST {
		return fmt.Errorf("ndpproxy: add %s: %w", ip, err)
	}
	return nil
}

// Remove deletes a proxy entry. Missing entries are not an error.
func (p *NDPProxy) Remove(ip net.IP) error {
	n := &netlink.Neigh{
		LinkIndex: p.link.Attrs().Index,
		Family:    unix.AF_INET6,
		Flags:     netlink.NTF_PROXY,
		IP:        ip.To16(),
	}
	if err := netlink.NeighDel(n); err != nil && err != unix.ENOENT {
		return fmt.Errorf("ndpproxy: del %s: %w", ip, err)
	}
	return nil
}

// List returns all current proxy entries on the upstream interface.
func (p *NDPProxy) List() ([]net.IP, error) {
	neighs, err := netlink.NeighList(p.link.Attrs().Index, unix.AF_INET6)
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, n := range neighs {
		if n.Flags&netlink.NTF_PROXY != 0 {
			out = append(out, n.IP)
		}
	}
	return out, nil
}

// Reconcile diffs the desired set against the current kernel state, adding
// missing entries and removing stale ones. Per-entry errors are logged, not
// fatal — the caller can retry on the next pass.
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
