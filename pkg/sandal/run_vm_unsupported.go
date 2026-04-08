//go:build !linux && !darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

// RunInVM is not supported on this platform.
func RunInVM(c *config.Config, netFlags []string) error {
	_ = netFlags
	return fmt.Errorf("VM is not supported on this platform")
}
