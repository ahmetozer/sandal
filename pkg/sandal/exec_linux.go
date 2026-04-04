//go:build linux

package sandal

import (
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	containerexec "github.com/ahmetozer/sandal/pkg/container/exec"
)

// execNative runs exec directly by entering the container's namespaces.
func execNative(c *config.Config, args []string, user, dir string) error {
	return containerexec.ExecInContainer(c, args, user, dir, os.Stdin, os.Stdout, os.Stderr)
}
