//go:build !linux && !darwin

package sandal

import (
	"context"
	"fmt"
)

func platformRun(ctx context.Context, args []string, rio *RunIO) error {
	return fmt.Errorf("run command is only available on Linux and macOS")
}
