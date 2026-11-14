//go:build darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

// RunContainer is not available on macOS — containers run inside VMs.
func RunContainer(c *config.Config, networkFlags []string, s *crt.RunSession) error {
	return fmt.Errorf("native container execution is not available on macOS (containers run inside VMs)")
}
