//go:build darwin

package sandal

// RunInVM starts a VM using Apple Virtualization.framework on macOS.
func RunInVM(args []string) error {
	return RunInVZ(args)
}
