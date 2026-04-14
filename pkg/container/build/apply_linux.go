//go:build linux

package build

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/overlayfs"
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

// BackingCleanup releases storage set up by MountStageBacking.
// Calling it more than once is a no-op.
type BackingCleanup func() error

// MountStageBacking prepares storage at dir for the stage rootfs or a
// per-RUN change dir. Backing is chosen in this order:
//
//  1. tmpSize > 0 → tmpfs of tmpSize MB (explicit opt-in).
//  2. Otherwise check whether the system supports nesting a new
//     overlayfs at dir. The test: is dir's parent filesystem already
//     overlayfs? If so, the kernel rejects a nested overlay with
//     EINVAL ("max stacking depth exceeded"), so we MUST fall back.
//       - supports overlayfs  → plain directory (no mount)
//       - does not support    → loop-mounted ext4 image (size = csize,
//                                default 8g); same mechanism `sandal
//                                run -chdir-type image` uses.
//
// dir is created if missing. Returns a cleanup function that undoes
// whatever mount was made (no-op for the plain-directory case).
func MountStageBacking(dir string, tmpSize uint, csize string) (BackingCleanup, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// 1. Explicit tmpfs request.
	if tmpSize > 0 {
		opts := fmt.Sprintf("size=%dm", tmpSize)
		if err := unix.Mount("tmpfs", dir, "tmpfs", 0, opts); err != nil {
			return nil, fmt.Errorf("tmpfs at %s: %w", dir, err)
		}
		return func() error { return unmountIgnoreENOENT(dir) }, nil
	}

	// 2. Does the system support stacking a new overlayfs here?
	if supportsOverlayfs(dir) {
		return func() error { return nil }, nil
	}

	// 3. Fall back to loop-mounted ext4 image (csize-sized).
	if csize == "" {
		csize = "8g"
	}
	mount, err := overlayfs.PrepareImageChangeDir(dir, csize)
	if err != nil {
		return nil, fmt.Errorf("image backing at %s: %w", dir, err)
	}
	return func() error { return mount.Cleanup() }, nil
}

// supportsOverlayfs reports whether a new overlayfs can be mounted with
// dir as upperdir. The test is "is dir's parent already overlayfs?" —
// because the kernel refuses to nest overlayfs beyond its max stacking
// depth, which is the only practical blocker we've seen. If detection
// itself errors, assume no support (safer to use the disk fallback).
func supportsOverlayfs(dir string) bool {
	parent := filepath.Dir(dir)
	parentIsOverlay, err := overlayfs.IsOverlayFS(parent)
	if err != nil {
		return false
	}
	return !parentIsOverlay
}

func unmountIgnoreENOENT(dir string) error {
	if err := unix.Unmount(dir, 0); err != nil {
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
