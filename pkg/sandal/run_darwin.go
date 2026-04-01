//go:build darwin

package sandal

// platformRun on macOS always boots a VM (no native container support).
func platformRun(args []string) error {
	return RunInVM(args)
}
