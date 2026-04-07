//go:build darwin

package sandal

func platformRun(args []string) error {
	return parseAndRunContainer(args)
}

// requiresVM returns true on macOS — native containers are not supported,
// all containers must run inside a VM.
func requiresVM() bool { return true }
