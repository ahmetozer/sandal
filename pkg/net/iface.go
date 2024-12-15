package net

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
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
		if unix.Errno(err.(unix.Errno)) != unix.ENODEV {
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

func WaitInterface(name string) error {

	interfaceReady := make(chan bool)

	go func(killed chan<- bool) {
		for {
			link, err := netlink.LinkByName(name)
			if err != nil {
				if _, ok := err.(netlink.LinkNotFoundError); !ok {
					slog.Info("interface not found", slog.String("name", name))
				}
			}
			if err == nil && link != nil {
				interfaceReady <- true
				break
			}
			time.Sleep(time.Second)
		}
	}(interfaceReady)

	select {
	case ret := <-interfaceReady:
		if !ret {
			return fmt.Errorf("unable to get interface")
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("unable to get interface %s in 5 second", name)
	}

}
