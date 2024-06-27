package net

import (
	"fmt"
	"net"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func AddAddress(name string, addrs string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	return addAddress(link, addrs)
}

func addAddress(link netlink.Link, addrs string) error {

	if addrs == "" {
		return nil
	}

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
			if unix.Errno(err.(unix.Errno)) != unix.EEXIST {
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

func FindFreePodIPs(hostIpsText string) (string, error) {
	configs, err := config.AllContainers()
	if err != nil {
		return "", err
	}
	hostIPs := strings.Split(hostIpsText, ";")
	Ips := make([]net.IP, len(hostIPs))
	podIps := make([]string, len(Ips))
MasterLoop:
	for ipNo, ip := range hostIPs {
		HostIp, HostNet, err := net.ParseCIDR(ip)
		if err != nil {
			continue MasterLoop
		}
		Ips[ipNo] = HostIp
		// CIDR is not enough, IP's are exousted
		if Ips[ipNo].Equal(lastIP(HostNet)) {
			continue MasterLoop
		}
	Incrementer:
		for {
			Ips[ipNo], err = ipIncrement(Ips[ipNo], *HostNet)
			if err != nil {
				break Incrementer
			}
			// Instances:
			for _, c := range configs {
			PodIfaces:
				for _, iface := range c.Ifaces {
					if iface.ALocFor != config.ALocForPod {
						continue PodIfaces
					}
					for _, addr := range strings.Split(iface.IP, ";") {
						podIp, _, err := net.ParseCIDR(addr)
						if err != nil {
							continue PodIfaces
						}
						if podIp.Equal(Ips[ipNo]) {
							continue Incrementer
						}
					}

				}
			}
			break Incrementer
		}
		podIps[ipNo] = Ips[ipNo].String() + "/" + netmaskToCIDR(HostNet.Mask)
		continue MasterLoop
	}

	return strings.Join(podIps, ";"), nil
}

func ipIncrement(i net.IP, n net.IPNet) (net.IP, error) {

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

func netmaskToCIDR(mask net.IPMask) string {
	// Convert mask to binary string
	binary := ""
	for _, b := range mask {
		binary += fmt.Sprintf("%08b", b)
	}

	// Count the number of consecutive '1's
	count := strings.Count(binary, "1")

	// Convert count to CIDR notation
	cidr := fmt.Sprintf("%d", count)

	return cidr
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
