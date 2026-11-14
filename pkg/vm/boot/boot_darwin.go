//go:build darwin

package boot

import (
	"io"

	"github.com/ahmetozer/sandal/pkg/container/forward"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

// Boot starts a VM using Apple Virtualization.framework on macOS.
// stdin/stdout are accepted for API compatibility but not used by VZ (it manages its own console).
func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return vz.Boot(name, cfg)
}

// BootWithForwards is Boot plus the -p mappings of a `run -vm` container.
// ready is not called: macOS never forks a VMM that waits for it (`run -vm
// -d` is rejected there).
func BootWithForwards(name string, cfg vmconfig.VMConfig, forwards []forward.PortMapping, ready func()) error {
	return vz.BootWithForwards(name, cfg, nil, nil, forwards)
}
