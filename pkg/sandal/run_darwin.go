//go:build darwin

package sandal

import "context"

func platformRun(ctx context.Context, args []string, rio *RunIO) error {
	return parseAndRunContainer(ctx, args, rio)
}

// requiresVM returns true on macOS — native containers are not supported,
// all containers must run inside a VM.
func requiresVM() bool { return true }
