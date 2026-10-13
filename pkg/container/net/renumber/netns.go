//go:build linux

package renumber

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// SetContainerGlobals enters the container's netns, finds the link that
// carries any of the lookup addresses (falling back to ifaceName), and
// converges the globals the renumber service owns on that link to exactly
// want. IPv4, link-local, ULA and static (non-owned) globals are untouched.
func SetContainerGlobals(contPid int, lookup []net.IP, ifaceName string, want []net.IPNet) error {
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

	target, err := findLinkByAddrs(lookup, ifaceName)
	if err != nil {
		return err
	}
	have, err := netlink.AddrList(target, netlink.FAMILY_V6)
	if err != nil {
		return fmt.Errorf("addr list: %w", err)
	}
	del, add := diffGlobals(have, want)
	for i := range del {
		if err := netlink.AddrDel(target, &del[i]); err != nil {
			return fmt.Errorf("addr del %s: %w", del[i].IPNet, err)
		}
	}
	for i := range add {
		if err := netlink.AddrAdd(target, &netlink.Addr{IPNet: &add[i]}); err != nil && !errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("addr add %s: %w", add[i].String(), err)
		}
	}
	return nil
}

// findLinkByAddrs must run inside the target netns.
func findLinkByAddrs(lookup []net.IP, ifaceName string) (netlink.Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("link list: %w", err)
	}
	for _, l := range links {
		addrs, err := netlink.AddrList(l, netlink.FAMILY_ALL)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			for _, ip := range lookup {
				if a.IP.Equal(ip) {
					return l, nil
				}
			}
		}
	}
	if ifaceName == "" {
		return nil, fmt.Errorf("no link carries %v and no interface name given", lookup)
	}
	l, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("link %q in container netns: %w", ifaceName, err)
	}
	return l, nil
}

// diffGlobals returns the owned globals in have that are not wanted, and
// the wanted addresses missing from have.
func diffGlobals(have []netlink.Addr, want []net.IPNet) (del []netlink.Addr, add []net.IPNet) {
	owned := ownedBy(want)
	var cur []net.IPNet
	for _, h := range have {
		if !isGlobal(h.IP) {
			continue
		}
		n := net.IPNet{IP: h.IP, Mask: h.IPNet.Mask}
		cur = append(cur, n)
		if owned(h.IP) && !containsAddr(want, n) {
			del = append(del, h)
		}
	}
	for _, w := range want {
		if !containsAddr(cur, w) {
			add = append(add, w)
		}
	}
	return del, add
}
