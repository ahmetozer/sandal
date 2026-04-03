//go:build darwin

package sandal

import "github.com/ahmetozer/sandal/pkg/container/config"

// RunInVM starts a VM using Apple Virtualization.framework on macOS.
func RunInVM(c *config.Config) error {
	return RunInVZ(c)
}
