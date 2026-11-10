//go:build linux

package sandal

import (
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	containerexec "github.com/ahmetozer/sandal/pkg/container/exec"
)

// execNative runs exec directly by entering the container's namespaces,
// with the -ns-<kind> values in ns applied.
func execNative(c *config.Config, args []string, user, dir string, tty bool, extraEnv []string, ns map[string]string) error {
	join, err := containerexec.JoinNamespaces(c, ns)
	if err != nil {
		return err
	}
	return containerexec.ExecInContainer(c, join, args, user, dir, tty, extraEnv, os.Stdin, os.Stdout, os.Stderr)
}
