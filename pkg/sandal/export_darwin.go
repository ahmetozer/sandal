//go:build darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func exportNative(c *config.Config, outputPath string, includes, excludes []string) (string, error) {
	return "", fmt.Errorf("native container export is not available on macOS")
}
