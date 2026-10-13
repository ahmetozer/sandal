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
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

// DefaultApplier renumbers sandal0 + all running containers under a new prefix
// and reconciles the NDP proxy table on the upstream interface (when configured).
type DefaultApplier struct {
	BridgeName string    // typically "sandal0"
	Proxy      *NDPProxy // nil when mode != ndp-proxy
	// HostRoute installs sandal0's and containers' globals as /128. Set in
	// ndp-proxy mode: the upstream /64 is on-link on the upstream interface,
	// and a second on-link /64 on sandal0 would split the host's connected
	// route and break neighbor discovery toward LAN hosts in that /64. In pd
	// mode sandal owns the routed /64, so globals keep the prefix mask.
	HostRoute bool
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

	bridgeIPs, err := a.renumberBridge(prefix)
	if err != nil {
		return fmt.Errorf("bridge: %w", err)
	}

	conts, err := controller.Containers()
	if err != nil {
		return fmt.Errorf("controller.Containers: %w", err)
	}

	mask := a.globalMask(prefix)
	for _, c := range conts {
		// VM addresses are fixed at boot (resolved in Link.defaults); only
		// containers have a netns the host can update.
		if !eligible(c, nil) || c.VM != "" {
			continue
		}
		if err := reconcileContainer(c, prefix, mask); err != nil {
			slog.Warn("renumber: container failed", "name", c.Name, "err", err)
		}
	}
	activePrefix, activeMask, activeBridgeIPs = prefix, mask, bridgeIPs

	if a.Proxy != nil {
		a.Proxy.Reconcile(nil)
	}
	return nil
}

// renumberBridge picks between two paths based on whether SANDAL_HOST_NET
// declares any dynamic (%uv6%-bearing) entries:
//
//  1. Dynamic entries present → reconcile the bridge against the resolved
//     templates. The user's template fully owns the address (prefix + IID);
//     bridgeIID/withIID is bypassed.
//  2. No dynamic entries → fall back to the historical bridgeIID + withIID
//     stamp. This preserves backward compatibility for hand-crafted
//     SANDAL_HOST_NET values and exercises the ULA-IID fallback added in
//     the prior spec.
func (a *DefaultApplier) renumberBridge(prefix *net.IPNet) ([]net.IP, error) {
	_, dynamic := cnet.SplitHostNet(env.DefaultHostNet)
	if len(dynamic) > 0 {
		return a.reconcileDynamicEntries(prefix, dynamic)
	}
	return a.renumberBridgeFallback(prefix)
}

// reconcileDynamicEntries resolves each %uv6%-bearing template against the
// current upstream prefix and converges sandal0's global address set to
// exactly those resolved values.
func (a *DefaultApplier) reconcileDynamicEntries(prefix *net.IPNet, templates []string) ([]net.IP, error) {
	link, err := netlink.LinkByName(a.BridgeName)
	if err != nil {
		return nil, fmt.Errorf("link %q: %w", a.BridgeName, err)
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V6)
	if err != nil {
		return nil, fmt.Errorf("addr list: %w", err)
	}

	type want struct {
		ip   net.IP
		mask net.IPMask
	}
	wanted := make(map[string]want, len(templates))
	for _, tpl := range templates {
		resolved := cnet.ResolveDynamic(tpl, prefix)
		ip, ipnet, perr := net.ParseCIDR(resolved)
		if perr != nil {
			slog.Warn("renumber: dynamic entry parse failed", "template", tpl, "resolved", resolved, "err", perr)
			continue
		}
		mask := a.bridgeMask(ipnet.Mask)
		key := (&net.IPNet{IP: ip, Mask: mask}).String()
		wanted[key] = want{ip: ip, mask: mask}
	}

	// Drop bridge globals that are not in the wanted set; keep LL and ULA
	// (static entries) untouched.
	for _, existing := range addrs {
		if existing.IP.IsLinkLocalUnicast() {
			continue
		}
		if isULA(existing.IP) {
			continue
		}
		key := (&net.IPNet{IP: existing.IP, Mask: existing.IPNet.Mask}).String()
		if _, keep := wanted[key]; keep {
			continue
		}
		if err := netlink.AddrDel(link, &existing); err != nil {
			slog.Warn("bridge: addr del failed", "addr", existing, "err", err)
		}
	}

	// Add wanted entries that aren't already on the link.
	have := make(map[string]bool, len(addrs))
	for _, existing := range addrs {
		have[(&net.IPNet{IP: existing.IP, Mask: existing.IPNet.Mask}).String()] = true
	}
	for key, w := range wanted {
		if have[key] {
			continue
		}
		add := &netlink.Addr{IPNet: &net.IPNet{IP: w.ip, Mask: w.mask}}
		if err := netlink.AddrAdd(link, add); err != nil && !strings.Contains(err.Error(), "exists") {
			return nil, fmt.Errorf("addr add %s: %w", add.IPNet, err)
		}
	}

	ips := make([]net.IP, 0, len(wanted))
	for _, w := range wanted {
		ips = append(ips, w.ip)
	}
	return ips, nil
}

// renumberBridgeFallback is the historical bridgeIID/withIID path. Used when
// SANDAL_HOST_NET has no dynamic entries.
func (a *DefaultApplier) renumberBridgeFallback(prefix *net.IPNet) ([]net.IP, error) {
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
	newAddr := &net.IPNet{IP: newIP, Mask: a.bridgeMask(prefix.Mask)}

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
	return []net.IP{newIP}, nil
}

// setContainerFn is indirected so tests can observe persistence.
var setContainerFn = controller.SetContainer

// reconcileContainer converges c's owned globals onto prefix (with mask)
// and persists the config when anything changed. c.Net is updated in memory
// even when persisting fails, because the netns already reflects it.
func reconcileContainer(c *config.Config, prefix *net.IPNet, mask net.IPMask) error {
	links, err := cnet.ToLinks(&c.Net)
	if err != nil {
		return fmt.Errorf("ToLinks: %w", err)
	}
	changed := false
	for i, l := range *links {
		want, ok := wantGlobals(l, prefix, mask)
		if !ok || sameAddrs(ownedGlobals(l, want), want) {
			continue
		}
		if c.ContPid > 0 {
			ifname := l.Name
			if ifname == "" {
				ifname = l.Id
			}
			if err := SetContainerGlobals(c.ContPid, lookupAddrs(l.Addr), ifname, want); err != nil {
				return fmt.Errorf("link %s: %w", ifname, err)
			}
		}
		(*links)[i].Addr = l.Addr.ReplaceGlobals(ownedBy(want), want)
		changed = true
	}
	if !changed {
		return nil
	}
	c.Net = *links
	if err := setContainerFn(c); err != nil {
		return fmt.Errorf("controller.SetContainer: %w", err)
	}
	return nil
}

// bridgeIID extracts an IID from the bridge's existing addresses. Prefers
// an existing global address (so subsequent renumbers keep the same tail),
// then falls back to the ULA so the first renumber preserves whatever IID
// structure SANDAL_HOST_NET established — e.g. a %uv4%-derived embedding.
// Returns [...::1] only when neither global nor ULA exists.
func bridgeIID(addrs []netlink.Addr) []byte {
	var ulaIID []byte
	for _, a := range addrs {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		if isULA(a.IP) {
			if ulaIID == nil {
				ip := a.IP.To16()
				ulaIID = make([]byte, 8)
				copy(ulaIID, ip[8:16])
			}
			continue
		}
		ip := a.IP.To16()
		iid := make([]byte, 8)
		copy(iid, ip[8:16])
		return iid
	}
	if ulaIID != nil {
		return ulaIID
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

func (a *DefaultApplier) globalMask(prefix *net.IPNet) net.IPMask {
	return a.bridgeMask(prefix.Mask)
}

func (a *DefaultApplier) bridgeMask(m net.IPMask) net.IPMask {
	if a.HostRoute {
		return net.CIDRMask(128, 128)
	}
	return m
}

// eligible reports whether the renumber service tracks c: running, alive
// (when isAlive is given) and not sharing the host netns. VMs are eligible
// for NDP proxying but their addresses are fixed at boot, so callers that
// enter a netns must skip c.VM != "" themselves.
func eligible(c *config.Config, isAlive func(*config.Config) bool) bool {
	if !IsRunning(c) {
		return false
	}
	if isAlive != nil && !isAlive(c) {
		return false
	}
	return !c.NS.Get("net").IsHost
}

// wantGlobals resolves the globals l should carry under prefix. ok=false
// means the link is not managed: dynamic=false, in-container DHCPv6, not on
// sandal0, no upstream prefix yet, or no dynamic address (literal globals
// are static).
func wantGlobals(l cnet.Link, prefix *net.IPNet, mask net.IPMask) ([]net.IPNet, bool) {
	if !l.Dynamic || l.DHCPv6 || prefix == nil || len(l.DynamicAddr) == 0 {
		return nil, false
	}
	if l.Master != cnet.DefaultBridgeInterface {
		return nil, false
	}
	want := make([]net.IPNet, 0, len(l.DynamicAddr))
	for _, d := range l.DynamicAddr {
		ip, err := cnet.ResolveDynamicAddr(d, prefix)
		if err != nil {
			slog.Warn("renumber: dynamic address resolve failed", "addr", d, "err", err)
			continue
		}
		want = append(want, net.IPNet{IP: ip, Mask: mask})
	}
	return want, true
}

// ownedGlobals returns l's persisted globals that the renumber service owns:
// those whose IID matches a wanted address. Static literal globals with
// other IIDs are never returned.
func ownedGlobals(l cnet.Link, want []net.IPNet) []net.IPNet {
	owned := ownedBy(want)
	var out []net.IPNet
	for _, a := range l.Addr {
		if isGlobal(a.IP) && owned(a.IP) {
			out = append(out, net.IPNet{IP: a.IP, Mask: a.IPNet.Mask})
		}
	}
	return out
}

func ownedBy(want []net.IPNet) func(net.IP) bool {
	return func(ip net.IP) bool {
		for _, w := range want {
			if iidEqual(ip, w.IP) {
				return true
			}
		}
		return false
	}
}

// sameAddrs compares two address lists ignoring order; IP and mask must match.
func sameAddrs(a, b []net.IPNet) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		if !containsAddr(b, x) {
			return false
		}
	}
	return true
}

func containsAddr(list []net.IPNet, x net.IPNet) bool {
	for _, y := range list {
		if x.IP.Equal(y.IP) && x.Mask.String() == y.Mask.String() {
			return true
		}
	}
	return false
}

// lookupAddrs lists every non-link-local address of a link. Any of them
// locates the link inside the container netns, including IPv4-only links.
func lookupAddrs(a cnet.Addrs) []net.IP {
	var out []net.IP
	for _, e := range a {
		if e.IP.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, e.IP)
	}
	return out
}
