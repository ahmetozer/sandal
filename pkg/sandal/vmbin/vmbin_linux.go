//go:build linux

package vmbin

import (
	"fmt"
	"os"
	"path/filepath"
)

// Linux returns the bytes of the sandal binary to be used as /init in
// a VM guest. On linux this is the running executable.
func Linux() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving self binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return nil, fmt.Errorf("reading self binary %s: %w", exe, err)
	}
	return data, nil
}
