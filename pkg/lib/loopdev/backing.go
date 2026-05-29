//go:build linux

package loopdev

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// BackingFile returns the path the loop device is currently backing, or
// "" when the device is not attached (or sysfs is unreadable). The value
// comes from /sys/block/loopN/loop/backing_file; the kernel appends
// " (deleted)" when the backing inode has been unlinked, which is trimmed.
func BackingFile(no int) string {
	data, err := os.ReadFile("/sys/block/loop" + strconv.Itoa(no) + "/loop/backing_file")
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(data))
	s = strings.TrimSuffix(s, " (deleted)")
	if s == "" {
		return ""
	}
	return filepath.Clean(s)
}
