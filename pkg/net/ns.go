package net

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/vishvananda/netlink"
)

func SetNs(iface config.NetIface, pid int) error {
	if iface.ALocFor != config.ALocForPod {
		return nil
	}
	link, err := netlink.LinkByName(iface.Name)
	if err != nil {
		return fmt.Errorf("error getting link %s by name: %v", iface.Name, err)
	}

	if err := netlink.LinkSetNsPid(link, pid); err != nil {
		return fmt.Errorf("error setting link %s to pid %d: %v", iface.Name, pid, err)
	}
	return nil
}
