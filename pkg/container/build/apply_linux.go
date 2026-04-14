//go:build linux

package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// applyOverlayUpper applies overlayfs upper-dir changes to dst in place.
//
// Overlayfs encodes filesystem mutations in the upper directory as:
//   - regular files / dirs / symlinks → present
//   - whiteout (deleted entry)        → character device with rdev == 0
//   - opaque dir (replaced contents)  → trusted.overlay.opaque xattr == "y"
//
// We translate each into the corresponding mutation on dst.
func applyOverlayUpper(upper, dst string) error {
	return filepath.Walk(upper, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(upper, srcPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dstPath := filepath.Join(dst, rel)

		// Check whiteout (char device 0/0).
		if info.Mode()&os.ModeCharDevice != 0 {
			if st, ok := info.Sys().(*unix.Stat_t); ok && st.Rdev == 0 {
				_ = os.RemoveAll(dstPath)
				return nil
			}
		}

		if info.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			// Opaque whiteout: replace dst dir with upper's contents.
			val := make([]byte, 1)
			sz, _ := unix.Getxattr(srcPath, "trusted.overlay.opaque", val)
			if sz > 0 && val[0] == 'y' {
				if err := removeDirContents(dstPath); err != nil {
					return err
				}
			}
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return err
		}

		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			_ = os.Remove(dstPath)
			return os.Symlink(target, dstPath)
		}

		if !info.Mode().IsRegular() {
			// Skip other special types.
			return nil
		}

		sf, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer sf.Close()
		_ = os.Remove(dstPath)
		df, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(df, sf); err != nil {
			df.Close()
			return err
		}
		return df.Close()
	})
}

// mountStageRootTmpfs mounts a tmpfs at dir so the stage rootfs is on a
// real, non-overlay filesystem. Without this, RUN's overlay would exceed
// the kernel's max stacking depth on hosts where /var/lib/sandal sits on
// overlayfs (devcontainers, nested sandal, Docker-in-Docker).
//
// Size is generous (no built-in limit) — tmpfs is sparse, only consumes
// RAM as data is written.
func mountStageRootTmpfs(dir string) error {
	return unix.Mount("tmpfs", dir, "tmpfs", 0, "")
}

// UnmountStageRoot detaches the tmpfs (or whatever was mounted) at dir.
// Safe to call even if nothing is mounted — returns nil in that case.
func UnmountStageRoot(dir string) error {
	if err := unix.Unmount(dir, 0); err != nil {
		// EINVAL = nothing mounted there; treat as no-op.
		if err == unix.EINVAL {
			return nil
		}
		return err
	}
	return nil
}

func removeDirContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("removing %s: %w", e.Name(), err)
		}
	}
	return nil
}
