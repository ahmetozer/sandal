//go:build linux

package renumber

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	cnet "github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/vishvananda/netlink"
)

// DefaultApplier renumbers sandal0 + all running containers under a new prefix
// and reconciles the NDP proxy table on the upstream interface (when configured).
type DefaultApplier struct {
	BridgeName string    // typically "sandal0"
	Proxy      *NDPProxy // nil when mode != ndp-proxy
}

// Apply runs the bridge → containers → NDP proxy sequence.
func (a *DefaultApplier) Apply(ctx context.Context, prefix *net.IPNet) error {
	var outerErr error
	WithApplyLock(func() {
		outerErr = a.applyLocked(ctx, prefix)
	})
	return outerErr
}

func (a *DefaultApplier) applyLocked(ctx context.Context, prefix *net.IPNet) error {
	if prefix == nil {
		return fmt.Errorf("apply: nil prefix")
	}
	slog.Info("renumber: applying", "prefix", prefix.String())

	if _, err := a.renumberBridge(prefix); err != nil {
		return fmt.Errorf("bridge: %w", err)
	}

	conts, err := controller.Containers()
	if err != nil {
		return fmt.Errorf("controller.Containers: %w", err)
	}

	// shadow is a live slice of pointers — mutations to c.Net inside
	// renumberContainer are immediately visible to subsequent IPRequest calls,
	// preventing two containers without a preserved IID from picking the same IID.
	shadow := make([]*config.Config, len(conts))
	copy(shadow, conts)

	containerIPs := make([]net.IP, 0, len(conts))
	for _, c := range conts {
		if !IsRunning(c) {
			continue
		}
		// VM containers have their own kernel and renumber via in-VM RA
		// on sandal0; host must not poke the netns.
		if c.VM != "" {
			continue
		}
		if c.NS.Get("net").IsHost {
			continue
		}
		ips, err := a.renumberContainer(c, prefix, &shadow)
		// Append IPs before checking err: the in-kernel netns is the
		// source of truth for what the proxy must advertise. Disk-write
		// failures in renumberContainer leave the kernel state advanced,
		// so the proxy table should still reflect it.
		containerIPs = append(containerIPs, ips...)
		if err != nil {
			slog.Warn("renumber: container failed", "name", c.Name, "err", err)
			continue
		}
	}

	if a.Proxy != nil {
		a.Proxy.Reconcile(containerIPs)
	}
	return nil
}

func (a *DefaultApplier) renumberBridge(prefix *net.IPNet) (*net.IPNet, error) {
	link, err := netlink.LinkByName(a.BridgeName)
	if err != nil {
		return nil, fmt.Errorf("link %q: %w", a.BridgeName, err)
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V6)
	if err != nil {
		return nil, fmt.Errorf("addr list: %w", err)
	}

	iid := bridgeIID(addrs)
	newIP := withIID(prefix, iid)
	newAddr := &net.IPNet{IP: newIP, Mask: prefix.Mask}

	for _, a := range addrs {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		if isULA(a.IP) {
			continue
		}
		if err := netlink.AddrDel(link, &a); err != nil {
			slog.Warn("bridge: addr del failed", "addr", a, "err", err)
		}
	}
	add := &netlink.Addr{IPNet: newAddr}
	if err := netlink.AddrAdd(link, add); err != nil && !strings.Contains(err.Error(), "exists") {
		return nil, fmt.Errorf("addr add %s: %w", newAddr, err)
	}
	return newAddr, nil
}

// renumberContainer rewrites the container's links under `prefix` and persists
// the change to its config. Returns the new container global IPv6 addresses.
// reserved is the full live slice of all containers; mutations to c.Net are
// visible to subsequent IPRequest calls so no two containers pick the same IID.
func (a *DefaultApplier) renumberContainer(c *config.Config, prefix *net.IPNet, reserved *[]*config.Config) ([]net.IP, error) {
	links, err := cnet.ToLinks(&c.Net)
	if err != nil {
		return nil, fmt.Errorf("ToLinks: %w", err)
	}
	var newIPs []net.IP

	for i, link := range *links {
		if !link.Dynamic {
			continue
		}
		// In-container DHCPv6 owns this link's IPv6; the renumber service
		// must not fight the client.
		if link.DHCPv6 {
			continue
		}
		var newIP net.IP
		if iid := pickIIDLocal(link.Addr); iid != nil {
			newIP = withIID(prefix, iid)
		} else {
			ip, err := cnet.IPRequest(reserved, prefix)
			if err != nil {
				return nil, fmt.Errorf("IPRequest: %w", err)
			}
			newIP = ip
		}

		oldGlobal := pickGlobalLocal(link.Addr)
		if c.ContPid > 0 {
			ifname := link.Name
			if ifname == "" {
				ifname = link.Id
			}
			var oldIP net.IP
			switch {
			case oldGlobal != nil:
				// Normal renumber path: replace the existing global
				// (its IID is preserved in newIP via withIID).
				oldIP = oldGlobal.IP
			default:
				// No global to replace — container was created during a
				// gap state (e.g. upstream had no public IPv6 when the
				// container started). Use any non-link-local IPv6 still
				// on the link (typically the ULA from SANDAL_HOST_NET) to
				// locate the netns link by address. The current
				// SwapContainerAddrByOldIP semantics then delete that
				// fallback address before adding newAddr, so the container
				// transitions ULA → global.
				if alt := pickLookupAddrLocal(link.Addr); alt != nil {
					oldIP = alt.IP
				}
			}
			if err := SwapContainerAddrByOldIP(c.ContPid, ifname, oldIP, &net.IPNet{IP: newIP, Mask: prefix.Mask}); err != nil {
				return nil, fmt.Errorf("SwapContainerAddrByOldIP: %w", err)
			}
		}

		(*links)[i].Addr = link.Addr.ReplaceGlobal(net.IPNet{IP: newIP, Mask: prefix.Mask})
		// Make this iteration's new IP visible to the next iteration's
		// IPRequest — guards against intra-container IID collisions when
		// one container has multiple dynamic links. c is a pointer also
		// in *reserved, so subsequent containers also see up-to-date state.
		c.Net = *links
		newIPs = append(newIPs, newIP)
	}

	if err := controller.SetContainer(c); err != nil {
		return newIPs, fmt.Errorf("controller.SetContainer: %w", err)
	}
	return newIPs, nil
}

// pickIIDLocal mirrors cnet.pickIID but works on the package-typed cnet.Addrs
// (returned by cnet.ToLinks). Kept here because cnet.pickIID is unexported and
// would require an exported wrapper to reach from this package.
func pickIIDLocal(a cnet.Addrs) []byte {
	g := pickGlobalLocal(a)
	if g == nil {
		return nil
	}
	ip := g.IP.To16()
	iid := make([]byte, 8)
	copy(iid, ip[8:16])
	return iid
}

func pickGlobalLocal(a cnet.Addrs) *cnet.Addr {
	_, ll, _ := net.ParseCIDR("fe80::/10")
	_, ula, _ := net.ParseCIDR("fc00::/7")
	for i := range a {
		ip := a[i].IP
		if ip.To4() != nil {
			continue
		}
		if ll.Contains(ip) || ula.Contains(ip) {
			continue
		}
		return &a[i]
	}
	return nil
}

// pickLookupAddrLocal returns the first non-IPv4, non-link-local address in
// the list — global or ULA. Used by the renumber path as a fallback "anchor"
// to locate a link inside a container netns when no global address exists
// yet (e.g. gap state where the container started before upstream IPv6 was
// available). The returned address is suitable for an address-based netns
// lookup; the caller decides whether to also delete it.
func pickLookupAddrLocal(a cnet.Addrs) *cnet.Addr {
	_, ll, _ := net.ParseCIDR("fe80::/10")
	for i := range a {
		ip := a[i].IP
		if ip.To4() != nil {
			continue
		}
		if ll.Contains(ip) {
			continue
		}
		return &a[i]
	}
	return nil
}

func bridgeIID(addrs []netlink.Addr) []byte {
	for _, a := range addrs {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		if isULA(a.IP) {
			continue
		}
		ip := a.IP.To16()
		iid := make([]byte, 8)
		copy(iid, ip[8:16])
		return iid
	}
	iid := make([]byte, 8)
	iid[7] = 1
	return iid
}

func withIID(prefix *net.IPNet, iid []byte) net.IP {
	out := make(net.IP, 16)
	src := prefix.IP.To16()
	copy(out, src)
	copy(out[8:16], iid)
	mask := prefix.Mask
	for i := 0; i < 16; i++ {
		out[i] = (src[i] & mask[i]) | (out[i] &^ mask[i])
	}
	return out
}

func IsRunning(c *config.Config) bool {
	if c == nil {
		return false
	}
	if c.ContPid == 0 {
		return false
	}
	if strings.HasPrefix(c.Status, "exit") || strings.HasPrefix(c.Status, "err") {
		return false
	}
	return true
}
