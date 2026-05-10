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

// SwapContainerAddr enters the container's netns identified by contPid, finds
// the named interface, removes any global IPv6 address whose prefix matches
// `oldPrefix` (or all globals if oldPrefix is nil), and adds `newAddr`.
// Idempotent: if newAddr is already present, no-op for the add.
func SwapContainerAddr(contPid int, ifaceName string, oldPrefix *net.IPNet, newAddr *net.IPNet) error {
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

	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return fmt.Errorf("link %q in container netns: %w", ifaceName, err)
	}

	addrs, err := netlink.AddrList(link, netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("addr list: %w", err)
	}
	for _, a := range addrs {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		if oldPrefix != nil && !oldPrefix.Contains(a.IP) {
			continue
		}
		if err := netlink.AddrDel(link, &a); err != nil {
			return fmt.Errorf("addr del %s: %w", a, err)
		}
	}

	add := &netlink.Addr{IPNet: newAddr}
	if err := netlink.AddrAdd(link, add); err != nil {
		return fmt.Errorf("addr add %s: %w", newAddr, err)
	}
	return nil
}
