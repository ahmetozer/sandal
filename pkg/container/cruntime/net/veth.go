package net

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/vishvananda/netlink"
)

func (l *Link) createVeth() error {

	var (
		Host string = "s-" + l.Id
		Cont string = l.Id
		err  error
	)

	contLink := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: Cont}, PeerName: Host}
	netlink.LinkDel(contLink)
	if err = netlink.LinkAdd(contLink); err != nil {
		return fmt.Errorf("error creating container host interface: %v", err)
	}

	hostLink, err := netlink.LinkByName(contLink.PeerName)
	if err != nil {
		slog.Error("hostlink not found")
		return err
	}

	err = netlink.LinkSetUp(hostLink)
	if err != nil {
		return err
	}

	if l.Master != "" && l.Master != "nil" {
		var masterlink netlink.Link
		if l.Master == DefaultBridgeInterface {
			masterlink, err = CreateDefaultBridge()
			if err != os.ErrExist {
				return err
			}
		} else {
			// return fmt.Errorf("currently multiple bridge interfaces are not supported")
			masterlink, err = netlink.LinkByName(l.Master)
			if err != nil {
				return err
			}
		}

		err = netlink.LinkSetMaster(hostLink, masterlink)
		if err != nil {
			return err
		}

	}

	return nil
}
