//go:build linux

package boot

import (
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

// Boot starts a VM using KVM on Linux.
func Boot(name string, cfg vmconfig.VMConfig) error {
	return kvm.Boot(name, cfg)
}
