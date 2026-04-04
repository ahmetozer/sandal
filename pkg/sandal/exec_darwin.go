//go:build darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

// execNative is not available on macOS — containers always run inside VMs.
func execNative(c *config.Config, args []string, user, dir string) error {
	return fmt.Errorf("native container exec is not available on macOS")
}
