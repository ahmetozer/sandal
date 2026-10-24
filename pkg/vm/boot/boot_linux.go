//go:build linux

package boot

import (
	"io"

	"github.com/ahmetozer/sandal/pkg/container/forward"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

// Boot starts a VM using KVM on Linux.
// If stdin/stdout are nil, os.Stdin/os.Stdout are used for console I/O.
func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return kvm.Boot(name, cfg, stdin, stdout)
}

// BootWithForwards is Boot on the process console plus the -p mappings of a
// `run -vm` container. ready, when not nil, is called once the VM is running
// and its forwards are listening.
func BootWithForwards(name string, cfg vmconfig.VMConfig, forwards []forward.PortMapping, ready func()) error {
	return kvm.BootWithForwards(name, cfg, nil, nil, forwards, ready)
}
