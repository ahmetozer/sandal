//go:build linux

package loopdev

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysfsField reads /sys/block/loop<no>/loop/<field>, trimmed. Returns "" on
// error (device not attached, or sysfs unreadable). sysfs is keyed by the
// kernel block-device name (loopN), independent of where the device node lives
// (/dev vs a custom LOOP_DEVICE_PREFIX), so these reads work everywhere.
func sysfsField(no int, field string) string {
	data, err := os.ReadFile("/sys/block/loop" + strconv.Itoa(no) + "/loop/" + field)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// parseBackingFile cleans a /sys/.../backing_file value and reports whether the
// kernel appended " (deleted)" (the backing inode was unlinked since attach).
func parseBackingFile(raw string) (path string, deleted bool) {
	s := strings.TrimSpace(raw)
	if base, ok := strings.CutSuffix(s, " (deleted)"); ok {
		base = strings.TrimSpace(base)
		if base == "" {
			return "", true
		}
		return filepath.Clean(base), true
	}
	if s == "" {
		return "", false
	}
	return filepath.Clean(s), false
}

// BackingFile returns the path the loop device is currently backing, or "" when
// the device is not attached. The kernel's " (deleted)" suffix is trimmed.
func BackingFile(no int) string {
	path, _ := parseBackingFile(sysfsField(no, "backing_file"))
	return path
}

// BackingRaw returns the cleaned backing path AND whether the kernel marked it
// "(deleted)". The reuse lookup uses deleted=true to refuse a stale mount.
func BackingRaw(no int) (path string, deleted bool) {
	return parseBackingFile(sysfsField(no, "backing_file"))
}

// Offset returns the loop device's backing-file offset (0 for whole-file
// images, the partition start byte for partitioned images).
func Offset(no int) uint64 {
	v, _ := strconv.ParseUint(sysfsField(no, "offset"), 10, 64)
	return v
}

// SizeLimit returns the loop device's size limit (0 = to end of file).
func SizeLimit(no int) uint64 {
	v, _ := strconv.ParseUint(sysfsField(no, "sizelimit"), 10, 64)
	return v
}

// DevicePath returns the device node path for loop number no, honoring
// LOOP_DEVICE_PREFIX. Used to reconstruct a Config when reusing an existing
// loop discovered by number.
func DevicePath(no int) string {
	return LOOP_DEVICE_PREFIX + strconv.Itoa(no)
}
