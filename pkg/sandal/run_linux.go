//go:build linux

package sandal

import "context"

func platformRun(ctx context.Context, args []string, rio *RunIO) error {
	return parseAndRunContainer(ctx, args, rio)
}

// requiresVM returns false on Linux — native containers are supported.
func requiresVM() bool { return false }
