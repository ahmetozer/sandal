package cruntime

import (
	"log/slog"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/net"
)

func DeRunContainer(c *config.Config) {
	if err := UmountRootfs(c); err != nil {
		for _, e := range err {
			slog.Debug("deRunContainer", "umount", slog.Any("error", e))
		}
	}
	if c.NS["net"].Value != "host" {
		net.Clear(c)
	}

	if c.Remove {

		removeAll := func(name string) {
			if err := os.RemoveAll(name); err != nil {
				slog.Debug("deRunContainer", "removeall", slog.String("file", name), slog.Any("error", err))
			}
		}

		removeAll(c.RootfsDir)
		removeAll(c.Workdir)
		removeAll(c.Upperdir)
		removeAll(c.ConfigFileLoc())
	}

}
