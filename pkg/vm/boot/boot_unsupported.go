//go:build !linux && !darwin

package boot

import (
	"fmt"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
)

func Boot(name string, cfg vmconfig.VMConfig) error {
	return fmt.Errorf("VM boot is not supported on this platform")
}
