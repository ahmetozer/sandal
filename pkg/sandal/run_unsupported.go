//go:build !linux && !darwin

package sandal

import (
	"fmt"

	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
)

func platformRun(args []string, s *crt.RunSession) error {
	return fmt.Errorf("run command is only available on Linux and macOS")
}
