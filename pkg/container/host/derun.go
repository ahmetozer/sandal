//go:build linux

package host

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/host/clean"
	"github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/container/resources"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
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

// reclaimStaleImmutableMounts unmounts squashfs/loop images recorded by a
// previous run of this container that the in-memory config doesn't know
// about. A fresh `sandal run` builds its config from CLI flags, so
// c.ImmutableImages is empty and CleanupResources would otherwise leave the
// prior run's loop mounts under /run/sandal/immutable orphaned.
//
// Each reclaim is gated on the loop device still backing the exact file the
// dead run recorded. The immutable mount dir is shared by loop number across
// all containers, so detaching a loop that has since been reused by another
// container would tear down that live container's rootfs — the backing-file
// check prevents that.
func reclaimStaleImmutableMounts(c *config.Config) {
	prev, err := controller.GetContainer(c.Name)
	if err != nil || prev == nil {
		return
	}

	for i := range prev.ImmutableImages {
		sq := prev.ImmutableImages[i]
		if c.ImmutableImages.Contains(sq) {
			continue // current run owns this image; normal cleanup handles it
		}
		if loopdev.BackingFile(sq.LoopConfig.No) != filepath.Clean(sq.File) {
			continue // loop detached or reused by another container — leave it
		}
		if err := diskimage.Umount(&sq); err != nil {
			slog.Debug("reclaimStaleImmutableMounts", slog.String("cont", c.Name), slog.String("file", sq.File), slog.Int("loop", sq.LoopConfig.No), slog.Any("error", err))
		}
	}
}

func DeRunContainer(c *config.Config) {
	reclaimStaleImmutableMounts(c)

	CleanupResources(c)

	Kill(c, 9, 5)

	if c.Remove {
		removeAll := func(name string) {
			if name == "" {
				return
			}
			ok, err := clean.IsInsideSandalArea(name)
			if err != nil {
				slog.Warn("deRunContainer: safety check failed", "path", name, "err", err)
				return
			}
			if !ok {
				slog.Warn("deRunContainer: refusing to delete path outside sandal dirs", "path", name)
				return
			}
			if err := os.RemoveAll(name); err != nil {
				slog.Debug("deRunContainer", "removeall", slog.String("file", name), slog.Any("error", err))
			}
		}

		removeAll(c.RootfsDir)
		removeAll(c.ChangeDir)
		if c.ChangeDir != "" {
			removeAll(c.ChangeDir + ".img") // VM disk image for change dir
		}
		removeAll(c.ConfigFileLoc())
	}

}
