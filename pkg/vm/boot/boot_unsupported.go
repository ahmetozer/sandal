//go:build !linux && !darwin

package boot

import (
	"fmt"
	"io"

	"github.com/ahmetozer/sandal/pkg/container/forward"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
)

func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return fmt.Errorf("VM boot is not supported on this platform")
}

func BootWithForwards(name string, cfg vmconfig.VMConfig, forwards []forward.PortMapping, ready func()) error {
	return fmt.Errorf("VM boot is not supported on this platform")
}
