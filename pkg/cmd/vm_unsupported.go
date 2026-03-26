//go:build !darwin && !linux

package cmd

import "fmt"

func VM(args []string) error {
	return fmt.Errorf("vm command is only available on macOS and Linux")
}
