//go:build darwin

package sandal

import crt "github.com/ahmetozer/sandal/pkg/container/runtime"

func platformRun(args []string, s *crt.RunSession) error {
	return parseAndRunContainer(args, s)
}

// requiresVM returns true on macOS — native containers are not supported,
// all containers must run inside a VM.
func requiresVM() bool { return true }
