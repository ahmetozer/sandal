//go:build linux

package sandal

import "github.com/ahmetozer/sandal/pkg/container/config"

// RunInVM starts a VM using KVM on Linux.
func RunInVM(c *config.Config, netFlags []string) error {
	return RunInKVM(c, netFlags)
}
