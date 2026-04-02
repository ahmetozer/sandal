//go:build linux

package boot

import (
	"io"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

// Boot starts a VM using KVM on Linux.
// If stdin/stdout are nil, os.Stdin/os.Stdout are used for console I/O.
func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return kvm.Boot(name, cfg, stdin, stdout)
}
