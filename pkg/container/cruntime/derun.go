//go:build linux

package cruntime

import (
	"log/slog"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/console"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/resources"
	"github.com/vishvananda/netlink"
)

// CleanupResources releases mounts, cgroups, and network interfaces
// for a container whose process is already dead. It is idempotent —
// calling it multiple times is safe (unmounting an already-unmounted
// path is a no-op).
func CleanupResources(c *config.Config) {
	if err := UmountRootfs(c); err != nil {
		for _, e := range err {
			slog.Debug("cleanupResources", "umount", slog.Any("error", e))
		}
	}

	// Clean up resource limits
	if c.MemoryLimit != "" || c.CPULimit != "" {
		cgroupPath := "/sys/fs/cgroup/sandal/" + c.Name
		if err := resources.RemoveCgroup(cgroupPath); err != nil {
			slog.Debug("cgroup cleanup", "path", cgroupPath, "error", err)
		}

		// Clean up proc files
		resources.CleanupProcFiles(c.RootfsDir)
	}

	// Clean up console directory (FIFOs, socket, log files)
	os.RemoveAll(console.Dir(c.Name))

	if !c.NS.Get("net").IsHost {
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
}

func DeRunContainer(c *config.Config) {
	CleanupResources(c)

	Kill(c, 9, 5)

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
