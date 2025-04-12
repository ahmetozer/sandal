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
func createDefaultBridge() (netlink.Link, error) {

	masterlink, err := netlink.LinkByName(DefaultBridgeInterface)
	if err == nil {
		return masterlink, os.ErrExist
	}

	// _, linkNotFoundError := err.(netlink.LinkNotFoundError)
	// if !linkNotFoundError && err != nil {
	// 	return nil, err
	// }

	masterlink = &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: DefaultBridgeInterface}}

	addrs, err := stringToAddrs(env.DefaultHostNet)
	if err != nil {
		return nil, err
	}

	err = netlink.LinkAdd(masterlink)
	if err != nil {
		return nil, err
	}

	err = netlink.LinkSetUp(masterlink)
	if err != nil {
		return nil, err
	}

	err = addrs.Add(masterlink)
	if err != nil {
		return nil, err
	}

	return masterlink, nil
}
