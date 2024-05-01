package net

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/vishvananda/netlink"
)

func CreateIface(c *config.Config, iface *config.NetIface) error {
	ifaceLink := netlink.Link(nil)
	err := error(nil)
	if c.NS.Net == "host" {
		return nil
	}

	// Allocated under host network
	if iface.Type == "bridge" {
		if ifaceLink, err = netlink.LinkByName(iface.Name); err != nil {
			ifaceLink = &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: iface.Name}}
			if err = netlink.LinkAdd(ifaceLink); err != nil {
				return fmt.Errorf("error creating master interface: %v", err)
			}
			c.Ifaces = append(c.Ifaces, *iface)
		}

		err = addAddress(ifaceLink, iface.IP)
		if err != nil {
			return fmt.Errorf("error add address interface: %v", err)
		}

		if err = netlink.LinkSetUp(ifaceLink); err != nil {
			return fmt.Errorf("error setting up master interface: %v", err)
		}

		return nil
	}

	if len(iface.Main) == 0 {
		return fmt.Errorf("main interface not found")
	}
	if len(iface.Main) > 1 {
		return fmt.Errorf("multiple main interface not supported")
	}

	if iface.Type == "macvlan" {
		parentLink, err := netlink.LinkByName(iface.Main[0].Name)
		if err != nil {
			return fmt.Errorf("error getting parent interface: %v", err)
		}

		ifaceLink = &netlink.Macvlan{
			LinkAttrs: netlink.LinkAttrs{Name: NewIfName(c).Cont, ParentIndex: parentLink.Attrs().Index},
			Mode:      netlink.MACVLAN_MODE_BRIDGE,
		}

		if _, err := netlink.LinkByName(iface.Name); err == nil {
			return fmt.Errorf("interface \"%s\" already exists", iface.Name)
		}

		if err = netlink.LinkAdd(ifaceLink); err != nil {
			return fmt.Errorf("error creating macvlan interface: %v", err)
		}
		c.Ifaces = append(c.Ifaces, config.NetIface{Name: iface.Name, Type: "macvlan", ALocFor: config.ALocForPod, IP: iface.IP})

		return nil
	}

	if iface.Type == "veth" && iface.Main[0].Type == "bridge" {
		masterlink, err := netlink.LinkByName(iface.Main[0].Name)
		if err != nil {
			return fmt.Errorf("error getting master interface: %v", err)
		}

		ifname := NewIfName(c)

		ifaceLink = &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: ifname.Host, MasterIndex: masterlink.Attrs().Index}, PeerName: ifname.Cont}
		if err = netlink.LinkAdd(ifaceLink); err != nil {
			return fmt.Errorf("error creating container host interface: %v", err)
		}

		c.Ifaces = append(c.Ifaces, config.NetIface{Name: ifname.Host, Type: "veth", ALocFor: config.ALocForHostPod, Main: iface.Main})

		c.Ifaces = append(c.Ifaces, config.NetIface{Name: ifname.Cont, Type: "veth", ALocFor: config.ALocForPod, Main: iface.Main, IP: iface.IP})
		// This will executed under container namespace
		// err = AddAddress(ifaceLink, c.HostIface.IP)
		// if err != nil {
		// 	return fmt.Errorf("error add address interface: %v", err)
		// }

		if err = netlink.LinkSetUp(ifaceLink); err != nil {
			return fmt.Errorf("error setting up master interface: %v", err)
		}

		return nil
	}

	return fmt.Errorf("unknown iface combination host %s pod %s", c.Ifaces[0].Type, iface.Type)

}
