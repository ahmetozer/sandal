//go:build linux

package net

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

// vmUplinkMAC stores eth0's original MAC before it is changed.
// Containers use this MAC so VZ NAT accepts their frames.
var vmUplinkMAC net.HardwareAddr

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

	if isVM, _ := env.IsVM(); isVM {
		// VM mode: sandal0 is a pure L2 bridge between eth0 and container veths.
		// No IPs on the bridge — containers DHCP directly through it.
		if err := bridgeUplinkToSandal0(masterlink); err != nil {
			slog.Warn("CreateDefaultBridge", slog.String("action", "bridge eth0"), slog.Any("error", err))
		}
	} else {
		// Bare Linux: assign static IPs from SANDAL_HOST_NET
		addrs, err := stringToAddrs(env.DefaultHostNet)
		if err != nil {
			return nil, err
		}
		err = addrs.Add(masterlink)
		if err != nil {
			return nil, err
		}
	}

	return netlink.LinkByName(DefaultBridgeInterface)
}

// bridgeUplinkToSandal0 bridges eth0 into sandal0 as a pure L2 switch.
// No IP is assigned to the bridge — containers DHCP directly through it
// to the upstream VZ NAT server.
//
// eth0's original MAC is saved in vmUplinkMAC and assigned to containers
// so VZ NAT accepts their frames. eth0 itself gets a new random MAC to
// avoid a MAC conflict on the bridge (two ports with the same MAC would
// cause the bridge FDB to capture replies instead of forwarding them).
func bridgeUplinkToSandal0(bridge netlink.Link) error {
	eth0, err := netlink.LinkByName("eth0")
	if err != nil {
		return fmt.Errorf("finding eth0: %w", err)
	}

	// Save the original VZ-assigned MAC for containers.
	origMAC := eth0.Attrs().HardwareAddr
	vmUplinkMAC = make(net.HardwareAddr, len(origMAC))
	copy(vmUplinkMAC, origMAC)

	// Give eth0 a different MAC so the bridge FDB doesn't have a
	// permanent-local entry for the VZ MAC on the eth0 port.
	newMAC := make(net.HardwareAddr, len(origMAC))
	copy(newMAC, origMAC)
	newMAC[0] |= 0x02 // locally-administered
	newMAC[5] ^= 0xFF // ensure different
	if err := netlink.LinkSetHardwareAddr(eth0, newMAC); err != nil {
		slog.Warn("changing eth0 MAC", "err", err)
	}

	// Bring eth0 up
	if err := netlink.LinkSetUp(eth0); err != nil {
		return fmt.Errorf("bringing up eth0: %w", err)
	}

	// Bridge eth0 into sandal0 — pure L2, no IP on bridge
	if err := netlink.LinkSetMaster(eth0, bridge); err != nil {
		return fmt.Errorf("setting eth0 master to %s: %w", DefaultBridgeInterface, err)
	}

	slog.Debug("CreateDefaultBridge", slog.String("action", "bridged eth0 into "+DefaultBridgeInterface),
		slog.String("vmMAC", vmUplinkMAC.String()))
	return nil
}
