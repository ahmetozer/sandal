//go:build linux

package net

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

// loadVMNetLinks decodes SANDAL_VM_NET (set on the kernel cmdline by the host
// in RunInKVM) into a slice of Link descriptors. The host writes a JSON array,
// one entry per host -net flag. For backwards compatibility with VMs booted by
// older sandal builds that wrote a single Link object, the helper falls back
// to decoding a single object and wrapping it in a one-element slice.
func loadVMNetLinks() []Link {
	spec := os.Getenv("SANDAL_VM_NET")
	if spec == "" {
		return nil
	}
	var arr []Link
	if err := json.Unmarshal([]byte(spec), &arr); err == nil && arr != nil {
		return arr
	}
	var one Link
	if err := json.Unmarshal([]byte(spec), &one); err == nil {
		return []Link{one}
	}
	return nil
}

// vmLinkIndex extracts the numeric suffix from an interface name like "eth3".
// Returns -1 if the name does not match the expected ethN pattern.
func vmLinkIndex(name string) int {
	if !strings.HasPrefix(name, "eth") {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "eth"))
	if err != nil || n < 0 {
		return -1
	}
	return n
}

type Link struct {
	Mtu    int
	Id     string
	Master string
	Type   string
	Name   string
	Ether  net.HardwareAddr

	Addr  Addrs
	Route Addrs

	DHCPv4 bool `json:",omitempty"`
	DHCPv6 bool `json:",omitempty"`
}

type Links []Link

func (l Link) defaults(conts *[]*config.Config) Link {

	if isVM, _ := env.IsVM(); isVM {
		// VM mode: the VMM has already pre-attached one virtio-net device
		// per -net flag. We don't create a veth — we just adopt the matching
		// ethN that the kernel enumerated.
		if l.Type == "" || l.Type == LinkTypeVeth {
			l.Type = LinkTypePassthru
		}
		if l.Type == LinkTypePassthru {
			l.Master = ""
			// ParseFlag stamps Id/Name to ethN by index in VM mode; only
			// fall back to eth0 if for some reason that didn't happen
			// (e.g. a Link constructed by code outside the parser).
			if l.Id == "" {
				l.Id = "eth0"
			}
			if l.Name == "" {
				l.Name = l.Id
			}
			// KVM: host pre-allocates per-NIC addrs into SANDAL_VM_NET (a
			// JSON array of Links — index N corresponds to ethN).
			// VZ:  no SANDAL_VM_NET, container will DHCP on eth0.
			if len(l.Addr) == 0 && !l.DHCPv4 && !l.DHCPv6 {
				if vmLinks := loadVMNetLinks(); len(vmLinks) > 0 {
					idx := vmLinkIndex(l.Id)
					if idx >= 0 && idx < len(vmLinks) {
						l.Addr = vmLinks[idx].Addr
						l.Route = vmLinks[idx].Route
						if l.Ether == nil && len(vmLinks[idx].Ether) > 0 {
							l.Ether = vmLinks[idx].Ether
						}
					}
				} else {
					l.DHCPv4 = true
				}
			}
		}
		return l
	}

	if l.Type == "" {
		l.Type = LinkTypeVeth
	}

	if l.Master == "" && (l.Type == "veth") {
		l.Master = DefaultBridgeInterface
	}

	if l.Master == DefaultBridgeInterface {
		CreateDefaultBridge()
	}

	hostAddrs, err := GetAddrsByName(l.Master)
	slog.Debug("hostAddrs", "interface", l.Master, slog.Any("addrs", hostAddrs))

	// Allocate IP addresses to container for each subnet
	if len(l.Addr) == 0 && !l.DHCPv4 && !l.DHCPv6 {
		contAddrs := make(Addrs, 0)
		if err == nil {
			for i := range hostAddrs {
				IP, err := IPRequest(conts, hostAddrs[i].IPNet)
				if err != nil {
					slog.Warn("unable to allocate ip", "err", err)
					continue
				}
				contAddrs = append(contAddrs, Addr{
					IP:    IP,
					IPNet: hostAddrs[i].IPNet,
				})
			}
			l.Addr = contAddrs
			l.Route = append(hostAddrs, l.Route...)
		}
	}

	// add route if its not present, it will used by FindGateways()
	if len(l.Route) == 0 && !l.DHCPv4 && !l.DHCPv6 && err == nil {
		l.Route = hostAddrs
	}

	return l
}

const (
	LinkTypeVeth     = "veth"
	LinkTypePassthru = "passthru" // move existing interface into container netns
)

func (l Link) Create() error {
	switch l.Type {
	case LinkTypeVeth:
		return l.createVeth()
	case LinkTypePassthru:
		return nil // interface already exists, nothing to create
	default:
		return fmt.Errorf("interface type is not found")
	}
}

func (l Link) SetNsPid(pid int) error {
	if pid == 1 {
		return nil
	}

	link, err := netlink.LinkByName(l.Id)
	if err != nil {
		return fmt.Errorf("error getting link %s by name: %v", l.Id, err)
	}

	if err := netlink.LinkSetNsPid(link, pid); err != nil {
		return fmt.Errorf("error setting link %s to pid %d: %v", l.Id, pid, err)
	}
	return nil
}

// Assign Ip addresses and set interface up
func (l *Link) Configure() error {

	nLink, err := netlink.LinkByName(l.Id)
	if err != nil {
		return err
	}

	// Set MAC before bringing up — needed for VM mode where VZ NAT
	// only forwards frames from the assigned MAC.
	if l.Ether != nil {
		if err := netlink.LinkSetHardwareAddr(nLink, l.Ether); err != nil {
			slog.Warn("setting MAC on "+l.Id, "err", err)
		}
	}

	for i := range l.Addr {
		l.Addr[i].Add(nLink)
	}
	netlink.LinkSetUp(nLink)

	// Run DHCP if requested
	if l.DHCPv4 || l.DHCPv6 {
		if err := l.configureDHCP(); err != nil {
			return fmt.Errorf("dhcp on %s: %w", l.Id, err)
		}
	}
	return nil
}

func ToLinks(d *any) (*Links, error) {
	c := Links{}

	s, err := json.Marshal(*d)
	if err != nil {
		return &c, err
	}

	err = json.Unmarshal(s, &c)
	*d = c
	return &c, err

}

func (links *Links) Append(link Link) {
	(*links) = append((*links), link)
}

func (l Link) WaitUntilCreated(second uint) (link netlink.Link, err error) {
	interfaceReady := make(chan bool)
	go func(killed chan<- bool) {
		for {
			link, err := netlink.LinkByName(l.Id)
			if err != nil {
				if _, ok := err.(netlink.LinkNotFoundError); !ok {
					slog.Info("interface not found", slog.String("id", l.Id))
				}
			}
			if err == nil && link != nil {
				interfaceReady <- true
				break
			}
			time.Sleep(time.Second / 10)
		}
	}(interfaceReady)

	select {
	case ret := <-interfaceReady:
		if !ret {
			err = fmt.Errorf("unable to get interface")
			return
		}
		return
	case <-time.After(time.Duration(second) * time.Second):
		err = fmt.Errorf("unable to get interface %s in %d second", l.Id, second)
		return
	}

}

func (l Links) WaitUntilCreated(second uint) (links []netlink.Link, err error) {
	interfaceReady := make(chan bool)

	go func(killed chan<- bool) {
		var haserror error
		for i := range l {
			link, err := l[i].WaitUntilCreated(second)
			if err == nil {
				links = append(links, link)
				continue
			}
			haserror = err
		}
		if haserror == nil {
			interfaceReady <- true
		}
	}(interfaceReady)

	select {
	case ret := <-interfaceReady:
		if !ret {
			err = fmt.Errorf("unable to get interface")
			return
		}
		return
	case <-time.After(time.Duration(second) * time.Second):
		err = fmt.Errorf("unable to get interfaces in %d second", second)
		return
	}
}

func (l Link) findFreeNewEthName() string {
	for i := range 1000 {
		name := fmt.Sprintf("eth%d", i)
		link, err := netlink.LinkByName(name)
		if link == nil && err != nil {
			return name
		}
	}
	return l.Id
}

func (links Links) FinalizeLinks() error {
	for i := range links {
		link, err := netlink.LinkByName(links[i].Id)
		if err != nil {
			return err
		}

		if links[i].Ether != nil {
			err = netlink.LinkSetHardwareAddr(link, links[i].Ether)
			if err != nil {
				return err
			}
		}

		if links[i].Mtu > 0 {
			err = netlink.LinkSetMTU(link, links[i].Mtu)
			if err != nil {
				return err
			}
		}

	}
	for i := range links {
		if links[i].Name != "" {
			link, err := netlink.LinkByName(links[i].Id)
			if err != nil {
				return err
			}
			err = netlink.LinkSetName(link, links[i].Name)
			if err != nil {
				return err
			}
		}
	}
	for i := range links {
		if links[i].Name == "" {
			link, err := netlink.LinkByName(links[i].Id)
			if err != nil {
				return err
			}
			err = netlink.LinkSetName(link, links[i].findFreeNewEthName())
			if err != nil {
				return err
			}
		}
	}
	return nil
}
