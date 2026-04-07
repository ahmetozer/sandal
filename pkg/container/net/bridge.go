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

	// Bare Linux: assign static IPs from SANDAL_HOST_NET
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
