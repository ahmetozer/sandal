//go:build darwin

package controller

import (
	"net"
	"os"
)

// secureSocketListen creates a Unix socket listener with restricted permissions.
// On macOS, umask is not used (single-user environment); we rely on chmod.
func secureSocketListen(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if chErr := os.Chmod(path, 0o600); chErr != nil {
		ln.Close()
		return nil, chErr
	}
	return ln, nil
}
