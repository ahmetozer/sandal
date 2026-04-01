//go:build !linux && !darwin

package sandal

import "fmt"

func platformRun(args []string) error {
	return fmt.Errorf("run command is only available on Linux and macOS")
}
