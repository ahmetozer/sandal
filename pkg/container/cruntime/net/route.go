package net

import (
	"net"

	"github.com/vishvananda/netlink"
)

func (l Links) FindGateways() (IPv4 Addr, IPv6 Addr) {

	IPv4.IP = nil
	IPv6.IP = nil
	// Firstly search is there any default route presented
	for i := range l {

		for _, gw := range l[i].Route {
			if gw.IPNet.IP.To4() == nil {
				IPv6 = gw
				IPv6.IPNet.IP = net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
				IPv6.IPNet.Mask = net.CIDRMask(0, 128)
				// IPv6.IP, IPv6.IPNet, _ = net.ParseCIDR("fd34:135:127::1/0")
			} else {
				IPv4 = gw
				IPv4.IPNet.IP = net.IP{0, 0, 0, 0}
				IPv4.IPNet.Mask = net.CIDRMask(0, 32)
				// IPv4.IP, IPv4.IPNet, _ = net.ParseCIDR("172.19.0.1/0")
			}
			// Return earlies as possible
			if IPv4.IP != nil && IPv6.IP != nil {
				return
			}
		}
	}

	return
}

func HasRoute(ip net.IP) ([]netlink.Route, bool, error) {
	route, err := netlink.RouteGet(ip)
	if err != nil {
		return route, false, err
	}
	return route, len(route) != 0, err
}

// test ips for getting default gateway
func Ipv4DefaultGatewayTestIp() net.IP {
	return net.IP{192, 88, 99, 0}
}

func Ipv6DefaultGatewayTestIp() net.IP {
	return net.IP{32, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
}
