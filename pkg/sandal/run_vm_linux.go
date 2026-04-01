//go:build linux

package sandal

// RunInVM starts a VM using KVM on Linux.
func RunInVM(args []string) error {
	return RunInKVM(args)
}
