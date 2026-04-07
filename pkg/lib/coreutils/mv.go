package coreutils

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Mv renames src to dst. When src and dst live on different filesystems
// os.Rename returns EXDEV; in that case Mv falls back to Cp followed by
// removing the source. The destination's parent directory is created if
// missing.
func Mv(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	// Cross-device: copy then remove source.
	if err := Cp(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}
