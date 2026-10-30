//go:build linux || darwin

package sandal

import (
	"fmt"
	"io"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
)

// Attach connects to a container's console and relays I/O.
// The caller is responsible for terminal setup (raw mode), signal handling,
// and creating the done channel. This keeps Attach reusable from CLI,
// web endpoints, daemons, etc.
//
// stdin/stdout/stderr: I/O handles for the attach session.
// done: closed when the caller wants to detach (e.g., container exited).
func Attach(c *config.Config, stdin io.Reader, stdout, stderr io.Writer, done <-chan struct{}) error {
	if c.VM != "" {
		// VMs are attached through the host-side FIFO console that
		// forkVMProcess (background) and RunInKVM (foreground) create.
		if _, err := os.Stat(console.ModePath(c.Name)); err != nil {
			return fmt.Errorf("container %q has no attachable console (foreground macOS VMs cannot be attached)", c.Name)
		}
	}
	return attachNative(c, stdin, stdout, stderr, done)
}
