//go:build linux

package overlayfs

import (
	"os"
	"path/filepath"
	"strings"
)

// SubMountUpperDir represents a sub-mount's upper directory and its relative
// path within the container filesystem.
type SubMountUpperDir struct {
	// RelPath is the relative path (e.g. "root" for /root).
	RelPath string
	// UpperDir is the absolute path to this sub-mount's overlay upper dir.
	UpperDir string
}

// GetSubMountUpperDirs returns all sub-mount upper directories for a container's
// change dir. These are the directories where COW writes for sub-mounted paths
// are stored (at changeDir/submount-upper/<safeName>/upper/).
func GetSubMountUpperDirs(changeDir string) []SubMountUpperDir {
	base := filepath.Join(changeDir, "submount-upper")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}

	var dirs []SubMountUpperDir
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		upper := filepath.Join(base, e.Name(), "upper")
		if _, err := os.Stat(upper); err != nil {
			continue
		}
		// Reverse the safeName encoding: underscores back to slashes.
		relPath := strings.ReplaceAll(e.Name(), "_", "/")
		dirs = append(dirs, SubMountUpperDir{
			RelPath:  relPath,
			UpperDir: upper,
		})
	}
	return dirs
}
