//go:build linux

package net

import (
	"fmt"
	"log/slog"
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

	// In VM mode, bridge eth0 (virtio-net from macOS NAT) into sandal0
	// so containers have L2 connectivity to the host network.
	if os.Getenv("SANDAL_VM_MOUNTS") != "" {
		if err := bridgeUplinkToSandal0(masterlink); err != nil {
			slog.Warn("CreateDefaultBridge", slog.String("action", "bridge eth0"), slog.Any("error", err))
		}
	}

	return netlink.LinkByName(DefaultBridgeInterface)
}

// bridgeUplinkToSandal0 adds eth0 as a port of the sandal0 bridge and enables IP forwarding.
func bridgeUplinkToSandal0(bridge netlink.Link) error {
	eth0, err := netlink.LinkByName("eth0")
	if err != nil {
		return fmt.Errorf("finding eth0: %w", err)
	}

	// Flush addresses from eth0 — the bridge holds the addresses now
	addrs, err := netlink.AddrList(eth0, netlink.FAMILY_ALL)
	if err == nil {
		for i := range addrs {
			netlink.AddrDel(eth0, &addrs[i])
		}
	}

	// Add eth0 as a bridge port and bring it up
	if err := netlink.LinkSetMaster(eth0, bridge); err != nil {
		return fmt.Errorf("setting eth0 master to %s: %w", DefaultBridgeInterface, err)
	}
	if err := netlink.LinkSetUp(eth0); err != nil {
		return fmt.Errorf("bringing up eth0: %w", err)
	}

	// Enable IP forwarding
	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)
	os.WriteFile("/proc/sys/net/ipv6/conf/all/forwarding", []byte("1"), 0644)

	slog.Info("CreateDefaultBridge", slog.String("action", "bridged eth0 into "+DefaultBridgeInterface))
	return nil
}
