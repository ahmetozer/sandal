//go:build linux

package overlayfs

import (
	"encoding/hex"
	"os"
	"path/filepath"
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
// are stored (at changeDir/submount-upper/<hex-encoded-relPath>/upper/).
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
		// Decode hex-encoded relPath.
		relPath, err := hex.DecodeString(e.Name())
		if err != nil {
			continue
		}
		dirs = append(dirs, SubMountUpperDir{
			RelPath:  string(relPath),
			UpperDir: upper,
		})
	}
	return dirs
}
