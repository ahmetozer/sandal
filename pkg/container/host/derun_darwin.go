//go:build darwin

package host

import (
	"log/slog"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/host/clean"
	"github.com/ahmetozer/sandal/pkg/env"
)

// CleanupResources on macOS removes console directories.
// No cgroups, netlink, or mount cleanup needed — those are inside the VM.
func CleanupResources(c *config.Config) {
	consoleDir := path.Join(env.RunDir, "console", c.Name)
	os.RemoveAll(consoleDir)
}

// DeRunContainer cleans up a container on macOS.
func DeRunContainer(c *config.Config) {
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
			os.RemoveAll(name)
		}
		removeAll(c.RootfsDir)
		removeAll(c.ChangeDir)
		if c.ChangeDir != "" {
			removeAll(c.ChangeDir + ".img")
		}
		removeAll(c.ConfigFileLoc())
	}
}
