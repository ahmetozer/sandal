package net

import (
	"net"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
)

func AddAddress(name string, addrs string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	return addAddress(link, addrs)
}

func addAddress(link netlink.Link, addrs string) error {

	for _, ip := range strings.Split(addrs, ";") {

		addr, ipNet, err := net.ParseCIDR(ip)
		if err != nil {
			return err
		}
		addrNet := &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   addr,
				Mask: ipNet.Mask,
			},
		}
		if err := netlink.AddrAdd(link, addrNet); err != nil {
			// if its not file exists error, return error
			if syscall.Errno(err.(syscall.Errno)) != syscall.EEXIST {
				return err
			}
		}

	}
	return nil

}

type IP_TYPE uint8

const (
	IP_TYPE_V4 IP_TYPE = 4
	IP_TYPE_V6 IP_TYPE = 6
)

func getGw(addrs string, t IP_TYPE) string {
	ctns := "."
	if t == IP_TYPE_V6 {
		ctns = ":"
	}
	for _, ip := range strings.Split(addrs, ";") {
		if strings.Contains(ip, ctns) {
			single := strings.Split(ip, "/")
			if len(single) > 0 {
				return single[0]
			}
		}
	}
	return ""
}
