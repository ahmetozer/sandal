//go:build linux

package sandal

import crt "github.com/ahmetozer/sandal/pkg/container/runtime"

func platformRun(args []string, s *crt.RunSession) error {
	return parseAndRunContainer(args, s)
}

// requiresVM returns false on Linux — native containers are supported.
func requiresVM() bool { return false }
