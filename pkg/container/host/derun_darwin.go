//go:build darwin

package host

import (
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
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
		os.RemoveAll(c.RootfsDir)
		os.RemoveAll(c.ChangeDir)
		os.Remove(c.ChangeDir + ".img")
		os.Remove(c.ConfigFileLoc())
	}
}
