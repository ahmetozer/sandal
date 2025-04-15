package net

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/vishvananda/netlink"
)

type Link struct {
	Id     string
	Master string
	Type   string
	Name   string

	Addr  Addrs
	Route Addrs
}

type Links []Link

func (l Link) defaults(conts *[]*config.Config) Link {

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
	// Allocate IP addresses to container for each subnet
	if len(l.Addr) == 0 {
		// hostAddrs, err := stringToAddrs(env.DefaultHostNet) //
		contAddrs := make(Addrs, 0)

		if err == nil {
			for i := range hostAddrs {

				IP, err := ipRequest(conts, hostAddrs[i].IPNet)
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
	if len(l.Route) == 0 && err == nil {
		l.Route = hostAddrs
	}

	return l
}

const (
	LinkTypeVeth = "veth"
)

func (l Link) Create() error {
	switch l.Type {
	case LinkTypeVeth:
		return l.createVeth()
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
func (l Link) Configure() error {

	nLink, err := netlink.LinkByName(l.Id)
	if err != nil {
		return err
	}
	for i := range l.Addr {
		l.Addr[i].Add(nLink)
	}
	netlink.LinkSetUp(nLink)
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

func (links Links) RenameLinks() error {
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
