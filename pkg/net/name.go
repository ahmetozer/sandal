package net

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/vishvananda/netlink"
)

func SetName(c *config.Config, oldName string, newName string) error {
	for i, iface := range c.Ifaces {
		if iface.Name == oldName {
			c.Ifaces[i].Name = newName
			link, err := netlink.LinkByName(oldName)
			if err != nil {
				return fmt.Errorf("unable to find interface %s: %v", oldName, err)
			}
			err = netlink.LinkSetName(link, newName)
			if err != nil {
				return fmt.Errorf("unable to rename %s to %s: %v", oldName, newName, err)
			}
			return nil
		}
	}
	// c.SaveConftoDisk()
	return fmt.Errorf("interface with name %s not found", oldName)

}
