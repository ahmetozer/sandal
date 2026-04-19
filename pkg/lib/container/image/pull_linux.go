//go:build linux

package squash

import (
	"archive/tar"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/progress"
	"golang.org/x/sys/unix"
)

// mergeLayers merges OCI image layers into a single directory.
// It first tries overlayfs (kernel handles whiteouts natively, fastest),
// then falls back to manual disk-based extraction with whiteout processing.
func mergeLayers(layers []io.Reader, dir string, progressCh ...chan<- progress.Event) error {
	var pch chan<- progress.Event
	if len(progressCh) > 0 {
		pch = progressCh[0]
	}
	// Extract each layer to its own directory.
	layerDirs := make([]string, len(layers))
	baseDir, err := os.MkdirTemp(env.BaseTempDir, "sandal-layers-*")
	if err != nil {
		return fmt.Errorf("creating layers base dir: %w", err)
	}
	defer os.RemoveAll(baseDir)

	for i, layer := range layers {
		layerDir := filepath.Join(baseDir, fmt.Sprintf("layer-%d", i))
		if err := os.MkdirAll(layerDir, 0755); err != nil {
			return fmt.Errorf("creating layer %d dir: %w", i, err)
		}
		slog.Debug("mergeLayers", slog.String("action", "extracting"), slog.Int("layer", i+1), slog.Int("total", len(layers)))
		if pch != nil {
			select {
			case pch <- progress.Event{Phase: progress.PhaseExtract, TaskID: "extract", Current: int64(i), Total: int64(len(layers))}:
			default:
			}
		}
		if err := extractLayerRaw(layer, layerDir); err != nil {
			return fmt.Errorf("extracting layer %d: %w", i, err)
		}
		layerDirs[i] = layerDir
	}

	if pch != nil {
		select {
		case pch <- progress.Event{Phase: progress.PhaseExtract, TaskID: "extract", Current: int64(len(layers)), Total: int64(len(layers)), Done: true}:
		default:
		}
	}

	if pch != nil {
		select {
		case pch <- progress.Event{Phase: progress.PhaseMerge, TaskID: "merge", Current: 0, Total: 0}:
		default:
		}
	}

	// Try overlayfs merge: stack layers as lowerdir (last layer = highest priority).
	mergeErr := mergeWithOverlayfs(layerDirs, dir)
	if mergeErr != nil {
		slog.Debug("mergeLayers", slog.String("action", "overlayfs-fallback"), slog.Any("error", mergeErr))
		// Overlayfs not available — fall back to manual merge with whiteout processing.
		mergeErr = mergeLayerDirs(layerDirs, dir)
	}

	if pch != nil && mergeErr == nil {
		totalSize, _ := dirSize(dir)
		select {
		case pch <- progress.Event{Phase: progress.PhaseMerge, TaskID: "merge", Current: totalSize, Total: totalSize, Done: true}:
		default:
		}
	}

	return mergeErr
}

// mergeWithOverlayfs mounts an overlayfs with all layer dirs stacked as
// lowerdir entries and a temporary upperdir/workdir, then copies the
// merged result to outDir.
//
// The upper/work dirs are created on BaseTempDir which is ext4-backed
// when -csize is set. No tmpfs is used — inside a VM every tmpfs byte
// comes from guest RAM. If the underlying filesystem does not support
// overlayfs (e.g. virtiofs without -csize), the mount fails and the
// caller falls back to manual merge.
func mergeWithOverlayfs(layerDirs []string, outDir string) error {
	if len(layerDirs) == 0 {
		return fmt.Errorf("no layers")
	}

	ovlDir, err := os.MkdirTemp(env.BaseTempDir, "sandal-ovl-upper-*")
	if err != nil {
		return fmt.Errorf("creating overlay upper dir: %w", err)
	}
	defer os.RemoveAll(ovlDir)

	upperDir := filepath.Join(ovlDir, "upper")
	workDir := filepath.Join(ovlDir, "work")
	os.MkdirAll(upperDir, 0755)
	os.MkdirAll(workDir, 0755)

	mountDir, err := os.MkdirTemp(env.BaseTempDir, "sandal-ovl-mount-*")
	if err != nil {
		return fmt.Errorf("creating overlay mount dir: %w", err)
	}
	defer os.RemoveAll(mountDir)

	// Build lowerdir option: last layer first (highest priority).
	lowerParts := make([]string, len(layerDirs))
	for i, d := range layerDirs {
		lowerParts[len(layerDirs)-1-i] = d
	}
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		strings.Join(lowerParts, ":"), upperDir, workDir)

	slog.Debug("mergeWithOverlayfs", slog.String("action", "mounting"), slog.Int("layers", len(layerDirs)))
	if err := cmount.Mount("overlay", mountDir, "overlay", 0, opts); err != nil {
		return fmt.Errorf("overlayfs mount: %w", err)
	}
	defer unix.Unmount(mountDir, 0)

	// Copy merged filesystem to output directory.
	return copyDir(mountDir, outDir)
}

// extractLayerRaw extracts a tar layer to a directory, converting OCI
// whiteout files to overlayfs-native whiteouts (char device 0/0).
// Does NOT process whiteouts — just converts the format for overlayfs.
func extractLayerRaw(layer io.Reader, dir string) error {
	tr := tar.NewReader(layer)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		name := cleanPath(hdr.Name)
		if name == "" {
			continue
		}

		nameDir := path.Dir(name)
		base := path.Base(name)

		// Convert OCI opaque whiteout to overlayfs trusted.overlay.opaque xattr.
		if base == ".wh..wh..opq" {
			targetDir := filepath.Join(dir, filepath.FromSlash(nameDir))
			os.MkdirAll(targetDir, 0755)
			// Set the overlayfs opaque xattr on the directory.
			unix.Setxattr(targetDir, "trusted.overlay.opaque", []byte("y"), 0)
			continue
		}

		// Convert OCI file whiteout (.wh.<name>) to overlayfs char device 0/0.
		// If mknod fails (no CAP_MKNOD), preserve the OCI .wh. marker file
		// so the manual merge fallback can process it.
		if strings.HasPrefix(base, ".wh.") {
			targetName := base[len(".wh."):]
			target := filepath.Join(dir, filepath.FromSlash(nameDir), targetName)
			os.MkdirAll(filepath.Dir(target), 0755)
			os.Remove(target)
			if err := unix.Mknod(target, unix.S_IFCHR|0666, 0); err != nil {
				// mknod failed — create an OCI-format .wh. marker file instead.
				whTarget := filepath.Join(dir, filepath.FromSlash(nameDir), base)
				os.WriteFile(whTarget, nil, 0644)
			}
			continue
		}

		target := filepath.Join(dir, filepath.FromSlash(name))

		// Prevent path traversal.
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
			if err := os.Chmod(target, tarMode(hdr.Mode)); err != nil {
				return fmt.Errorf("chmod %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
			}
			os.Remove(target)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("open %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
			if err := os.Chmod(target, tarMode(hdr.Mode)); err != nil {
				return fmt.Errorf("chmod %s: %w", target, err)
			}
		case tar.TypeSymlink:
			os.MkdirAll(filepath.Dir(target), 0755)
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			os.MkdirAll(filepath.Dir(target), 0755)
			linkTarget := filepath.Join(dir, filepath.FromSlash(filepath.Clean(hdr.Linkname)))
			os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			continue
		}
	}
}

// tarMode translates the 12-bit Unix mode from a tar header into an
// os.FileMode, preserving setuid/setgid/sticky which occupy different
// bit positions in Go's FileMode encoding than in on-disk Unix mode.
func tarMode(m int64) os.FileMode {
	mode := os.FileMode(m) & os.ModePerm
	if m&0o4000 != 0 {
		mode |= os.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

// mergeLayerDirs merges pre-extracted layer directories into outDir using
// manual whiteout processing. Used as fallback when overlayfs is unavailable.
// Layers are applied in order (layer 0 first, last layer wins).
func mergeLayerDirs(layerDirs []string, outDir string) error {
	slog.Debug("mergeLayerDirs", slog.String("action", "manual-merge"), slog.Int("layers", len(layerDirs)))
	for i, layerDir := range layerDirs {
		slog.Debug("mergeLayerDirs", slog.Int("layer", i+1), slog.Int("total", len(layerDirs)))
		if err := applyLayerDir(layerDir, outDir); err != nil {
			return fmt.Errorf("applying layer %d: %w", i, err)
		}
	}
	return nil
}

// applyLayerDir copies files from a single extracted layer directory into
// outDir, processing overlayfs-format whiteouts (char device 0/0 and
// trusted.overlay.opaque xattr).
func applyLayerDir(layerDir, outDir string) error {
	return filepath.Walk(layerDir, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(layerDir, srcPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		dst := filepath.Join(outDir, rel)

		// Check for overlayfs opaque xattr (converted from .wh..wh..opq).
		if info.IsDir() {
			val := make([]byte, 1)
			sz, _ := unix.Getxattr(srcPath, "trusted.overlay.opaque", val)
			if sz > 0 && val[0] == 'y' {
				// Opaque directory: remove all existing children in outDir.
				removeDirectoryContents(dst)
			}
			if err := os.MkdirAll(dst, 0755); err != nil {
				return fmt.Errorf("mkdir %s: %w", dst, err)
			}
			return os.Chmod(dst, info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
		}

		// Check for overlayfs whiteout (char device 0/0).
		if info.Mode()&os.ModeCharDevice != 0 {
			stat, ok := info.Sys().(*unix.Stat_t)
			if ok && stat.Rdev == 0 {
				// Whiteout: delete target in outDir.
				os.RemoveAll(dst)
				return nil
			}
		}

		// Check for OCI-format whiteout (.wh.<name> marker files).
		// These are created when mknod fails (no CAP_MKNOD).
		base := filepath.Base(srcPath)
		if strings.HasPrefix(base, ".wh.") {
			targetName := base[len(".wh."):]
			targetPath := filepath.Join(filepath.Dir(dst), targetName)
			os.RemoveAll(targetPath)
			return nil
		}

		// Regular file, symlink, etc — copy to outDir.
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", srcPath, err)
			}
			os.Remove(dst)
			if err := os.Symlink(linkTarget, dst); err != nil {
				return fmt.Errorf("symlink %s -> %s: %w", linkTarget, dst, err)
			}
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
		}
		os.Remove(dst)
		if err := copyFile(srcPath, dst, info.Mode()); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", srcPath, dst, err)
		}
		return nil
	})
}
