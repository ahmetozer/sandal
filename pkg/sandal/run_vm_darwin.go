//go:build darwin

package sandal

import (
	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

// RunInVM starts a VM using Apple Virtualization.framework on macOS.
func RunInVM(c *config.Config, netFlags []string, s *crt.RunSession) error {
	return RunInVZ(c, netFlags, s)
}
