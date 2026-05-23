//go:build linux

package renumber

import (
	"fmt"
	"log/slog"
	"net"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// SwapContainerAddrByOldIP enters the container's netns and finds the link
// that currently carries `oldIP`, then adds `newAddr`. The anchor (`oldIP`)
// is used purely to locate the right interface; whether it is deleted
// afterwards depends on its scope:
//
//   - If `oldIP` is a global IPv6 (i.e. the normal renumber case where a
//     previous global is being replaced), it is deleted before `newAddr`
//     is added — the bridge can hold only one global per prefix at a time.
//   - If `oldIP` is a ULA (the gap-state case where the container only
//     has its SANDAL_HOST_NET ULA), it is KEPT alongside `newAddr` so the
//     container retains intra-bridge connectivity after the renumber. This
//     mirrors how the bridge itself coexists ULA + public.
//
// Link-local addresses are never touched.
//
// If `oldIP` is nil, the function falls back to looking up by `ifaceName`
// (caller-supplied) for backward compatibility / first-time use cases.
func SwapContainerAddrByOldIP(contPid int, ifaceName string, oldIP net.IP, newAddr *net.IPNet) error {
	if contPid <= 0 {
		return fmt.Errorf("invalid contPid %d", contPid)
	}
	runtime.LockOSThread()
	unlockOK := true
	defer func() {
		if unlockOK {
			runtime.UnlockOSThread()
		}
	}()

	hostNS, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get host netns: %w", err)
	}
	defer hostNS.Close()

	contNS, err := netns.GetFromPid(contPid)
	if err != nil {
		return fmt.Errorf("open netns for pid %d: %w", contPid, err)
	}
	defer contNS.Close()

	if err := netns.Set(contNS); err != nil {
		return fmt.Errorf("setns container: %w", err)
	}
	defer func() {
		if err := netns.Set(hostNS); err != nil {
			slog.Error("netns: failed to restore host ns, tainting thread", "err", err)
			unlockOK = false
		}
	}()

	var target netlink.Link
	if oldIP != nil {
		links, err := netlink.LinkList()
		if err != nil {
			return fmt.Errorf("link list: %w", err)
		}
		for _, l := range links {
			addrs, err := netlink.AddrList(l, netlink.FAMILY_V6)
			if err != nil {
				continue
			}
			for _, a := range addrs {
				if a.IP.Equal(oldIP) {
					target = l
					break
				}
			}
			if target != nil {
				break
			}
		}
	}
	if target == nil && ifaceName != "" {
		// Fall back to by-name lookup (first-time renumber may not have an
		// old IP, or oldIP could be on the host side rather than the netns).
		var err error
		target, err = netlink.LinkByName(ifaceName)
		if err != nil {
			return fmt.Errorf("link %q in container netns: %w", ifaceName, err)
		}
	}
	if target == nil {
		return fmt.Errorf("could not find interface for old IP %v / name %q in container netns", oldIP, ifaceName)
	}

	addrs, err := netlink.AddrList(target, netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("addr list: %w", err)
	}
	for _, a := range addrs {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		// Keep ULAs on the container — they are the SANDAL_HOST_NET
		// fallback used for intra-bridge connectivity and are not in
		// scope for prefix-level renumber. The bridge itself coexists
		// ULA + public via the same rule; containers should too.
		if isULA(a.IP) {
			continue
		}
		if oldIP != nil && !a.IP.Equal(oldIP) {
			continue
		}
		if err := netlink.AddrDel(target, &a); err != nil {
			return fmt.Errorf("addr del %s: %w", a, err)
		}
	}

	add := &netlink.Addr{IPNet: newAddr}
	if err := netlink.AddrAdd(target, add); err != nil {
		return fmt.Errorf("addr add %s: %w", newAddr, err)
	}
	return nil
}
