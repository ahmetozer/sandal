//go:build darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func execNative(c *config.Config, args []string, user, dir string, tty bool) error {
	return fmt.Errorf("native container exec is not available on macOS")
}
