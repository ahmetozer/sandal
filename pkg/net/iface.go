package net

import (
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/vishvananda/netlink"
)

type InerfaceType uint8

const (
	InterfaceTypeBridge InerfaceType = iota
	InterfaceTypeVeth
	InterfaceTypeMacvlan
)

type NetInterface struct {
	PeerName string
	netlink.LinkAttrs
	InterfaceType InerfaceType
}

func DeleteInterface(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		if syscall.Errno(err.(syscall.Errno)) != syscall.ENODEV {
			return err
		}
	}
	return netlink.LinkDel(link)
}

type Ifname struct {
	Host string
	Cont string
}

func NewIfName(c *config.Config) Ifname {
	l := len(c.Name)
	if l > 10 {
		l = 10
	}
	return Ifname{
		Host: "san" + c.Name[:l],
		Cont: "veth" + c.Name[:l],
	}
}

func SetInterfaceUp(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	err = netlink.LinkSetUp(link)
	return err
}
