//go:build linux

package sandal

import (
	"fmt"
	"io"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
)

// attachNative attaches to a native Linux container's console.
func attachNative(c *config.Config, stdin io.Reader, stdout, stderr io.Writer, done <-chan struct{}) error {
	modeBytes, err := os.ReadFile(console.ModePath(c.Name))
	if err != nil {
		return fmt.Errorf("no console available for %q (was it started in background?)", c.Name)
	}
	mode := string(modeBytes)

	switch mode {
	case console.ModeSocket:
		// AttachSocket requires *os.File for stdin (uses Fd() for poll).
		stdinFile, ok := stdin.(*os.File)
		if !ok {
			return fmt.Errorf("socket attach requires *os.File for stdin")
		}
		return console.AttachSocket(c.Name, stdinFile, stdout, done)

	case console.ModeFIFO:
		return console.AttachFIFO(c.Name, stdin, stdout, stderr, done)

	default:
		return fmt.Errorf("unknown console mode: %s", mode)
	}
}
