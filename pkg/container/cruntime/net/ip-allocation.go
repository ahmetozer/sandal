package net

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func ipRequest(conts *[]*config.Config, x *net.IPNet) (ip net.IP, err error) {
	var IPnet net.IPNet
	err = deepCopy(x, &IPnet)
	if err != nil {
		return nil, err
	}

	brodcastSupport := false
	cidr, bits := IPnet.Mask.Size()

	ip = IPnet.IP

	// Increment at below is bypass first ip like 192.168.2.0
	switch bits {
	case 128:
		if cidr <= 120 {
			ip, err = ipIncrement(ip, &IPnet)
			if err != nil {
				return nil, err
			}
		}
	case 32:
		if cidr <= 24 {
			brodcastSupport = true
			ip, err = ipIncrement(ip, &IPnet)
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unexpected bit size %d for ip net", bits)
	}

	// Ip allocation
	for {
		if !addrInUse(conts, ip) {
			if ip.To4() != nil && brodcastSupport {
				if ip.Equal(lastIP(&IPnet)) {
					return nil, fmt.Errorf("allocatable unicast ip block is exhausted")
				}
			}
			// IP succesfully allocated
			return ip, nil
		}
		ip, err = ipIncrement(ip, &IPnet)
		if err != nil {
			return nil, err
		}
	}
}

func deepCopy(src, dst interface{}) error {
	bytes, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, dst)
}

func addrInUse(configs *[]*config.Config, ip net.IP) bool {

	addrs, _ := net.InterfaceAddrs()
	for i := range addrs {
		usedIp, _, _ := net.ParseCIDR(addrs[i].String())
		if ip.Equal(usedIp) {
			return true
		}
	}

ContainerInstances:
	for config := range *configs {
		l, err := ToLinks(&((*configs)[config].Net))
		links := *l
		if err != nil {
			continue ContainerInstances
		}
	NetworkInterfaces:
		for _, iface := range links {
			if iface.Id == "lo" {
				continue NetworkInterfaces
			}
			for _, addr := range iface.Addr {
				if addr.IP.Equal(ip) {
					return true
				}
			}
		}

	}
	return false
}
