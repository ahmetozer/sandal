package squash

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
	"golang.org/x/sys/unix"
)

// Pull downloads a container image, flattens its layers, and creates a
// squashfs image cached under imageDir. Returns the path to the squashfs file.
// If a cached image already exists for the reference, it is returned directly.
func Pull(ctx context.Context, imageRef string, imageDir string) (string, error) {
	platform := registry.Platform{
		OS:           "linux",
		Architecture: runtime.GOARCH,
	}
	//!FEAT This needs to support other arch like aarch64 and x64 x86
	if runtime.GOARCH == "arm" {
		platform.Variant = "v7"
	}

	// Sanitize image reference into a safe filename for caching.
	cacheFile := filepath.Join(imageDir, sanitizeRef(imageRef)+".sqfs")

	// Return cached image if it exists.
	if _, err := os.Stat(cacheFile); err == nil {
		slog.Info("Pull", slog.String("action", "cache-hit"), slog.String("image", imageRef), slog.String("path", cacheFile))
		return cacheFile, nil
	}

	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return "", fmt.Errorf("creating image directory: %w", err)
	}

	srcRef, err := registry.ParseReference(imageRef)
	if err != nil {
		return "", fmt.Errorf("parse image reference: %w", err)
	}

	slog.Info("Pull", slog.String("action", "pulling"), slog.String("image", imageRef), slog.String("os", platform.OS), slog.String("arch", platform.Architecture))

	client := registry.NewClient(srcRef.Registry)

	manifest, err := resolveManifest(ctx, client, srcRef, platform)
	if err != nil {
		return "", err
	}

	layers, err := downloadLayers(ctx, client, srcRef.Repository, manifest.Layers)
	if err != nil {
		return "", err
	}
	defer cleanupLayers(layers)

	slog.Debug("Pull", slog.String("action", "extracting-layers"), slog.String("image", imageRef))

	tmpDir, err := os.MkdirTemp("", "sandal-image-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Extract layers directly to disk instead of loading all file bodies
	// into RAM via Flatten(). This keeps memory usage O(1) and prevents
	// OOM on memory-constrained devices (e.g. Raspberry Pi).
	if err := mergeLayers(layers, tmpDir); err != nil {
		return "", fmt.Errorf("merging layers: %w", err)
	}

	slog.Debug("Pull", slog.String("action", "creating-squashfs"), slog.String("image", imageRef))

	// Write to temp file first, then rename for atomic cache update.
	tmpFile, err := os.CreateTemp(imageDir, ".pull-*.sqfs.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp squashfs file: %w", err)
	}
	tmpPath := tmpFile.Name()

	w, err := squashfs.NewWriter(tmpFile, squashfs.WithCompression(squashfs.CompGzip))
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(tmpDir); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("creating squashfs image: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, cacheFile); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("caching squashfs image: %w", err)
	}

	slog.Info("Pull", slog.String("action", "cached"), slog.String("image", imageRef), slog.String("path", cacheFile))
	return cacheFile, nil
}

// ExportImageSquashfs downloads a container image, flattens its layers,
// and writes a squashfs image to the given file.
func ExportImageSquashfs(ctx context.Context, imageRef string, outFile *os.File) error {
	tmpDir, err := pullToDir(ctx, imageRef)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	slog.Debug("ExportImageSquashfs", slog.String("action", "creating-squashfs"), slog.String("image", imageRef))
	w, err := squashfs.NewWriter(outFile, squashfs.WithCompression(squashfs.CompGzip))
	if err != nil {
		return fmt.Errorf("creating squashfs writer: %w", err)
	}
	return w.CreateFromDir(tmpDir)
}

// ExportImageTarGz downloads a container image, flattens its layers,
// and writes a gzip-compressed tar stream to w.
func ExportImageTarGz(ctx context.Context, imageRef string, w io.Writer) error {
	platform := registry.Platform{
		OS:           "linux",
		Architecture: runtime.GOARCH,
	}
	return Export(ctx, imageRef, platform, w)
}

// pullToDir downloads and flattens an image into a temporary directory.
// Caller is responsible for removing the returned directory.
func pullToDir(ctx context.Context, imageRef string) (string, error) {
	platform := registry.Platform{
		OS:           "linux",
		Architecture: runtime.GOARCH,
	}

	srcRef, err := registry.ParseReference(imageRef)
	if err != nil {
		return "", fmt.Errorf("parse image reference: %w", err)
	}

	slog.Info("pullToDir", slog.String("action", "pulling"), slog.String("image", imageRef), slog.String("os", platform.OS), slog.String("arch", platform.Architecture))

	client := registry.NewClient(srcRef.Registry)

	manifest, err := resolveManifest(ctx, client, srcRef, platform)
	if err != nil {
		return "", err
	}

	layers, err := downloadLayers(ctx, client, srcRef.Repository, manifest.Layers)
	if err != nil {
		return "", err
	}
	defer cleanupLayers(layers)

	slog.Debug("pullToDir", slog.String("action", "extracting-layers"), slog.String("image", imageRef))

	tmpDir, err := os.MkdirTemp("", "sandal-image-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}

	if err := mergeLayers(layers, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("merging layers: %w", err)
	}

	return tmpDir, nil
}

// IsImageReference returns true if the string looks like a container image
// reference rather than a local file path. It checks if it contains a
// registry host (has dots) or looks like a Docker Hub short name.
func IsImageReference(s string) bool {
	// Must not start with / or ./ (clearly a file path)
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return false
	}

	// If it parses as a valid reference and has a registry with dots or
	// is a simple name like "busybox" that resolves to Docker Hub, treat
	// it as an image reference.
	ref, err := registry.ParseReference(s)
	if err != nil {
		return false
	}

	// After parsing, Docker Hub refs get normalized to registry-1.docker.io
	return strings.Contains(ref.Registry, ".")
}

// sanitizeRef converts an image reference to a safe filename.
func sanitizeRef(ref string) string {
	h := sha256.Sum256([]byte(ref))
	// Use a readable prefix + hash suffix for uniqueness
	safe := strings.NewReplacer(
		"/", "_",
		":", "_",
		"@", "_",
	).Replace(ref)
	if len(safe) > 100 {
		safe = safe[:100]
	}
	return fmt.Sprintf("%s_%x", safe, h[:8])
}

// mergeLayers merges OCI image layers into a single directory.
// It first tries overlayfs (kernel handles whiteouts natively, fastest),
// then falls back to manual disk-based extraction with whiteout processing.
func mergeLayers(layers []io.Reader, dir string) error {
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
		if err := extractLayerRaw(layer, layerDir); err != nil {
			return fmt.Errorf("extracting layer %d: %w", i, err)
		}
		layerDirs[i] = layerDir
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

	if err := unix.Mount("tmpfs", tmpfsDir, "tmpfs", 0, "size=64k"); err != nil {
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
	if err := unix.Mount("overlay", mountDir, "overlay", 0, opts); err != nil {
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

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, srcPath)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, dstPath)
		}

		// Skip device nodes and other special files.
		if !info.Mode().IsRegular() {
			return nil
		}

		return copyFile(srcPath, dstPath, info.Mode())
	})
}

// copyFile copies a single file.
func copyFile(src, dst string, mode os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(df, sf); err != nil {
		df.Close()
		return err
	}
	return df.Close()
}

// removeDirectoryContents removes all children of a directory without
// removing the directory itself.
func removeDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
