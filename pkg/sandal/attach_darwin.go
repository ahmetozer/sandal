//go:build darwin

package sandal

import (
	"fmt"
	"io"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func attachNative(c *config.Config, stdin io.Reader, stdout, stderr io.Writer, done <-chan struct{}) error {
	return fmt.Errorf("native container attach is not available on macOS")
}
