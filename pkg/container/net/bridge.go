//go:build linux

package net

import (
	"os"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

const (
	DefaultBridgeInterface = "sandal0"
)

// Expected to run once
// In case of existence, returns error instead of nil to prevent
// any multi ip configuration at deamonless execution
func CreateDefaultBridge() (netlink.Link, error) {

	// VM mode: no bridge needed — eth0 is moved directly into the container netns.
	if isVM, _ := env.IsVM(); isVM {
		return nil, nil
	}

	masterlink, err := netlink.LinkByName(DefaultBridgeInterface)
	if err == nil {
		return masterlink, os.ErrExist
	}

	masterlink = &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: DefaultBridgeInterface}}

	err = netlink.LinkAdd(masterlink)
	if err != nil {
		return nil, err
	}

	// Disable STP and set forward delay to 0 so new ports forward immediately.
	bridgeSysPath := "/sys/class/net/" + DefaultBridgeInterface + "/bridge/"
	os.WriteFile(bridgeSysPath+"stp_state", []byte("0"), 0644)
	os.WriteFile(bridgeSysPath+"forward_delay", []byte("0"), 0644)

	err = netlink.LinkSetUp(masterlink)
	if err != nil {
		return nil, err
	}

	// Bare Linux: assign static IPs from SANDAL_HOST_NET. The full set
	// (IPv4 + ULA IPv6) is always applied so the bridge has a usable
	// fallback when dynamic IPv6 is configured but the renumber service
	// is dormant (e.g. upstream RA hasn't arrived yet, or upstream has no
	// global IPv6). The renumber service's renumberBridge() ADDS its
	// dynamic global prefix on top of the ULA — both coexist.
	addrs, err := stringToAddrs(env.DefaultHostNet)
	if err != nil {
		return nil, err
	}
	err = addrs.Add(masterlink)
	if err != nil {
		return nil, err
	}

	return netlink.LinkByName(DefaultBridgeInterface)
}
