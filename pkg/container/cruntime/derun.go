package cruntime

import (
	"log/slog"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/vishvananda/netlink"
)

func DeRunContainer(c *config.Config) {
	if err := UmountRootfs(c); err != nil {
		for _, e := range err {
			slog.Debug("deRunContainer", "umount", slog.Any("error", e))
		}
	}

	Kill(c, 9, 5)

	if c.NS["net"].Value != "host" {
		ifaces, err := net.ToLinks(&(c.Net))

		if err == nil {
			for i := range *ifaces {
				link, err := netlink.LinkByName("s-" + (*ifaces)[i].Id)
				if err == nil {
					netlink.LinkDel(link)

				}
			}
		}

	}

	if c.Remove {

		removeAll := func(name string) {
			if err := os.RemoveAll(name); err != nil {
				slog.Debug("deRunContainer", "removeall", slog.String("file", name), slog.Any("error", err))
			}
		}

		removeAll(c.RootfsDir)
		removeAll(c.ChangeDir)
		removeAll(c.ConfigFileLoc())
	}

}
