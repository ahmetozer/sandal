package net

import (
	"fmt"
	"net"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/vishvananda/netlink"
)

func AddDefaultRoutes(iface config.NetIface) error {
	if len(iface.Main) == 0 {
		return fmt.Errorf("no main interface")
	}
	gw4 := getGw(iface.Main[0].IP, IP_TYPE_V4)
	gw6 := getGw(iface.Main[0].IP, IP_TYPE_V6)

	if gw4 == "" && gw6 == "" {
		return fmt.Errorf("no gateway")
	}

	link, err := netlink.LinkByName(iface.Name)
	if err != nil {
		return err
	}

	if gw4 != "" {
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Dst:       &net.IPNet{IP: net.IPv4(0, 0, 0, 0), Mask: net.IPv4Mask(0, 0, 0, 0)},
			Gw:        net.ParseIP(gw4),
		})
		if err != nil {
			return err
		}
	}
	if gw6 != "" {
		err = netlink.RouteAdd(&netlink.Route{
			LinkIndex: link.Attrs().Index,
			Dst:       &net.IPNet{IP: net.ParseIP("::"), Mask: net.CIDRMask(0, 0)},
			Gw:        net.ParseIP(gw6),
		})
		if err != nil {
			return err
		}
	}

	return nil
}
