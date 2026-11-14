//go:build linux

package sandal

import (
	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

// RunInVM starts a VM using KVM on Linux.
func RunInVM(c *config.Config, netFlags []string, s *crt.RunSession) error {
	return RunInKVM(c, netFlags, s)
}
