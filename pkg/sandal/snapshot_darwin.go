//go:build darwin

package sandal

import (
	"fmt"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func snapshotNative(c *config.Config, filePath string, includes, excludes []string) (string, error) {
	return "", fmt.Errorf("native container snapshot is not available on macOS")
}
