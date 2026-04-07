//go:build linux

package controller

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// secureSocketListen creates a Unix socket listener with restricted permissions.
// On Linux, umask is used to prevent the socket from being world-accessible.
func secureSocketListen(path string) (net.Listener, error) {
	oldMask := unix.Umask(0o177)
	ln, err := net.Listen("unix", path)
	unix.Umask(oldMask)
	if err != nil {
		return nil, err
	}
	// Defense-in-depth: explicitly set socket to owner-only.
	if chErr := os.Chmod(path, 0o600); chErr != nil {
		ln.Close()
		return nil, chErr
	}
	return ln, nil
}
