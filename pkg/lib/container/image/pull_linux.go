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
	baseDir, err := os.MkdirTemp("", "sandal-layers-*")
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

	// Try overlayfs merge: stack layers as lowerdir (last layer = highest priority).
	if err := mergeWithOverlayfs(layerDirs, dir); err != nil {
		slog.Debug("mergeLayers", slog.String("action", "overlayfs-fallback"), slog.Any("error", err))
		// Overlayfs not available — fall back to manual merge with whiteout processing.
		return mergeLayerDirs(layerDirs, dir)
	}
	return nil
}

// mergeWithOverlayfs mounts an overlayfs with all layer dirs stacked as
// lowerdir entries and a tmpfs-backed upperdir/workdir, then copies the
// merged result to outDir. The tmpfs ensures upperdir/workdir are on a real
// filesystem (required by overlayfs) and keeps everything in memory.
func mergeWithOverlayfs(layerDirs []string, outDir string) error {
	if len(layerDirs) == 0 {
		return fmt.Errorf("no layers")
	}

	// Create a tmpfs for upper/work dirs — overlayfs requires these on a
	// real filesystem, and tmpfs auto-cleans on unmount.
	tmpfsDir, err := os.MkdirTemp("", "sandal-ovl-tmpfs-*")
	if err != nil {
		return fmt.Errorf("creating tmpfs dir: %w", err)
	}
	defer os.RemoveAll(tmpfsDir)

	if err := cmount.Mount("tmpfs", tmpfsDir, "tmpfs", 0, "size=64k"); err != nil {
		return fmt.Errorf("tmpfs mount: %w", err)
	}
	defer unix.Unmount(tmpfsDir, 0)

	upperDir := filepath.Join(tmpfsDir, "upper")
	workDir := filepath.Join(tmpfsDir, "work")
	os.MkdirAll(upperDir, 0755)
	os.MkdirAll(workDir, 0755)

	mountDir, err := os.MkdirTemp("", "sandal-ovl-mount-*")
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
		if strings.HasPrefix(base, ".wh.") {
			targetName := base[len(".wh."):]
			target := filepath.Join(dir, filepath.FromSlash(nameDir), targetName)
			os.MkdirAll(filepath.Dir(target), 0755)
			os.Remove(target)
			// Create character device 0/0 — the overlayfs whiteout format.
			unix.Mknod(target, unix.S_IFCHR|0666, 0)
			continue
		}

		target := filepath.Join(dir, filepath.FromSlash(name))

		// Prevent path traversal.
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, os.FileMode(hdr.Mode))
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			os.Remove(target)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
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
			return os.MkdirAll(dst, info.Mode())
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

		// Regular file, symlink, etc — copy to outDir.
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			os.Remove(dst)
			return os.Symlink(linkTarget, dst)
		}

		os.MkdirAll(filepath.Dir(dst), 0755)
		os.Remove(dst)
		return copyFile(srcPath, dst, info.Mode())
	})
}
