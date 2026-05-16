//go:build linux

package net

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// DetectUpstream returns the name of the interface carrying the default
// route to the public internet. It asks the kernel which interface it would
// use to reach a known public IPv6 address; if no IPv6 default route exists,
// it falls back to the IPv4 default route. Returns an error when neither
// family has a usable default route.
func DetectUpstream() (string, error) {
	if name, err := defaultRouteLink(Ipv6DefaultGatewayTestIp()); err == nil {
		return name, nil
	}
	if name, err := defaultRouteLink(Ipv4DefaultGatewayTestIp()); err == nil {
		return name, nil
	}
	return "", fmt.Errorf("no default IPv6 or IPv4 route found")
}

func defaultRouteLink(probe []byte) (string, error) {
	routes, err := netlink.RouteGet(probe)
	if err != nil {
		return "", err
	}
	for _, r := range routes {
		if r.LinkIndex == 0 {
			continue
		}
		link, err := netlink.LinkByIndex(r.LinkIndex)
		if err != nil {
			return "", err
		}
		return link.Attrs().Name, nil
	}
	return "", fmt.Errorf("no route for %s", probe)
}
