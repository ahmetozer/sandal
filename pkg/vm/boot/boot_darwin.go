//go:build darwin

package boot

import (
	"io"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

// Boot starts a VM using Apple Virtualization.framework on macOS.
// stdin/stdout are accepted for API compatibility but not used by VZ (it manages its own console).
func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return vz.Boot(name, cfg)
}
