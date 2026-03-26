//go:build !linux && !darwin

package run

import "fmt"

func Run(args []string) error {
	return fmt.Errorf("run command is only available on Linux and macOS")
}
