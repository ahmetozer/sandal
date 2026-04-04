//go:build linux

package sandal

import (
	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/snapshot"
)

func snapshotNative(c *config.Config, filePath string, includes, excludes []string) (string, error) {
	return snapshot.Create(c, filePath, snapshot.FilterOptions{
		Includes: includes,
		Excludes: excludes,
	})
}
