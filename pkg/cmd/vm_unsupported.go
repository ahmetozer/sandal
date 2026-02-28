//go:build !darwin

package cmd

import "fmt"

func VM(args []string) error {
	return fmt.Errorf("vm command is only available on macOS (requires Apple Virtualization framework)")
}
