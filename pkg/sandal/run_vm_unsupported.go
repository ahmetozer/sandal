//go:build !linux && !darwin

package sandal

import "fmt"

// RunInVM is not supported on this platform.
func RunInVM(args []string) error {
	return fmt.Errorf("VM is not supported on this platform")
}
