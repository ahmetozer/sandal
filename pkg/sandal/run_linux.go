//go:build linux

package sandal

func platformRun(args []string) error {
	return parseAndRunContainer(args)
}

// requiresVM returns false on Linux — native containers are supported.
func requiresVM() bool { return false }
