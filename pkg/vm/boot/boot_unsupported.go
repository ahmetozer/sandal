//go:build !linux && !darwin

package boot

import (
	"fmt"
	"io"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
)

func Boot(name string, cfg vmconfig.VMConfig, stdin io.Reader, stdout io.Writer) error {
	return fmt.Errorf("VM boot is not supported on this platform")
}
