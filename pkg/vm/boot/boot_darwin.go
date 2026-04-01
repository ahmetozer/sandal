//go:build darwin

package boot

import (
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

// Boot starts a VM using Apple Virtualization.framework on macOS.
func Boot(name string, cfg vmconfig.VMConfig) error {
	return vz.Boot(name, cfg)
}
