package overlayfs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func IsOverlayFS(path string) (bool, error) {
	// Use unix.Statfs to get filesystem information about the path
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return false, fmt.Errorf("error getting statfs info for path %s: %w", path, err)
	}

	// Filesystem type is stored in stat.Type, check if it equals the value for overlayfs
	const overlayfs = 0x794c7630 // This is the magic number for overlayfs
	return stat.Type == overlayfs, nil
}

func (c ChangesDir) IsOverlayFS() (bool, error) {
	return IsOverlayFS(c.upper)
}
