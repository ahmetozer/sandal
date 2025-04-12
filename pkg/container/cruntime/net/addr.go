package net

import (
	"fmt"
	"net"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type Addr struct {
	IP    net.IP
	IPNet *net.IPNet
}

// func (a Addr) ipIncrement() (Addr, error) {
// 	var err error
// 	a.IP, err = ipIncrement(a.IP, a.IPNet)
// 	return a, err
// }

func ipIncrement(i net.IP, n *net.IPNet) (net.IP, error) {
	i = i.To16()
	if i == nil || n == nil {
		return nil, fmt.Errorf("ip cannot be incremented, ip=%s net=%s", i, n)
	}
	for j := 16; j >= 0; j-- {
		cur := j - 1
		if i[cur] == 255 {
			i[cur] = 0
			continue
		}
		i[cur]++
		break
	}

	if n.Contains(i) {
		return i, nil
	}

	return i, fmt.Errorf("ip exeeds the network range")
}

func lastIP(n *net.IPNet) net.IP {
	lastIp, lastMask, _ := net.ParseCIDR("fd::/128")
	size := 16
	if n.IP.To4() != nil {
		lastIp, lastMask, _ = net.ParseCIDR("10.0.0.1/32")
		size = 4
	}

	for i := 0; i < size; i++ {
		lastIp[i] = (n.IP[i] & n.Mask[i]) | (n.Mask[i] ^ lastMask.Mask[i])
	}
	return lastIp[:size]
}

func (a Addr) Add(link netlink.Link) error {

	addrNet := &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   a.IP,
			Mask: a.IPNet.Mask,
		},
	}

	if err := netlink.AddrAdd(link, addrNet); err != nil {
		// if its not file exists error, return error
		if unix.Errno(err.(unix.Errno)) != unix.EEXIST {
			return err
		}
	}
	return nil
}

type Addrs []Addr

func (a Addrs) Add(link netlink.Link) error {
	for i := range a {
		err := a[i].Add(link)
		if err != nil {
			return err
		}
	}
	return nil
}

// Example input "172.16.0.1/24,fd34:0135:0123::1/64"
func stringToAddrs(ips string) (Addrs, error) {
	var addrs Addrs

	for _, i := range strings.Split(ips, ",") {
		ip, ipnet, err := net.ParseCIDR(i)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, Addr{ip, ipnet})
	}
	return addrs, nil
}

func GetAddrsByName(InterfaceName string) (addrs Addrs, err error) {
	link, err := netlink.LinkByName(InterfaceName)
	if err != nil {
		return
	}

	nAddrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return
	}

	_, IPv6LinkLocalNet, _ := net.ParseCIDR("fe80::/10")

	for _, a := range nAddrs {
		if IPv6LinkLocalNet.Contains(a.IP) {
			continue
		}
		addrs = append(addrs, Addr{
			IP:    a.IP,
			IPNet: a.IPNet,
		})
	}

	return
}
