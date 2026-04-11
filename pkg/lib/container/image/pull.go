package squash

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
	"github.com/ahmetozer/sandal/pkg/lib/progress"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

// Pull downloads a container image, flattens its layers, and creates a
// squashfs image cached under imageDir. Returns the path to the squashfs file.
// If a cached image already exists for the reference, it is returned directly.
func Pull(ctx context.Context, imageRef string, imageDir string, progressCh chan<- progress.Event) (string, error) {
	platform := registry.Platform{
		OS:           "linux",
		Architecture: runtime.GOARCH,
	}
	// 32-bit ARM needs a variant; arm64 and amd64 have no variant in the OCI spec.
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

	if progressCh != nil {
		fmt.Fprintf(os.Stderr, "Pulling %s (%s/%s)\n", imageRef, platform.OS, platform.Architecture)
	}

	client := registry.NewClient(srcRef.Registry)

	manifest, err := resolveManifest(ctx, client, srcRef, platform)
	if err != nil {
		return "", err
	}

	layers, err := downloadLayers(ctx, client, srcRef.Repository, manifest.Layers, progressCh)
	if err != nil {
		return "", err
	}
	defer cleanupLayers(layers)

	slog.Debug("Pull", slog.String("action", "extracting-layers"), slog.String("image", imageRef))

	tmpDir, err := os.MkdirTemp(env.BaseTempDir, "sandal-image-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Extract layers directly to disk instead of loading all file bodies
	// into RAM via Flatten(). This keeps memory usage O(1) and prevents
	// OOM on memory-constrained devices (e.g. Raspberry Pi).
	if err := mergeLayers(layers, tmpDir, progressCh); err != nil {
		return "", fmt.Errorf("merging layers: %w", err)
	}

	slog.Debug("Pull", slog.String("action", "creating-squashfs"), slog.String("image", imageRef))

	// Write to temp file first, then rename for atomic cache update.
	tmpFile, err := os.CreateTemp(imageDir, ".pull-*.sqfs.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp squashfs file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Bridge squashfs writer progress to the event channel.
	var sqfsProgressFn func(int64)
	if progressCh != nil {
		totalBytes, _ := dirSize(tmpDir)
		var lastSend time.Time
		sqfsProgressFn = func(written int64) {
			now := time.Now()
			if now.Sub(lastSend) < 500*time.Millisecond {
				return
			}
			lastSend = now
			select {
			case progressCh <- progress.Event{Phase: progress.PhaseSquashfs, TaskID: "squashfs", Current: written, Total: totalBytes}:
			default:
			}
		}
	}

	opts := []squashfs.WriterOption{squashfs.WithCompression(squashfs.CompGzip)}
	if sqfsProgressFn != nil {
		opts = append(opts, squashfs.WithProgressFunc(sqfsProgressFn))
	}

	w, err := squashfs.NewWriter(tmpFile, opts...)
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

	if progressCh != nil {
		fi, _ := os.Stat(tmpPath)
		var finalSize int64
		if fi != nil {
			finalSize = fi.Size()
		}
		select {
		case progressCh <- progress.Event{Phase: progress.PhaseSquashfs, TaskID: "squashfs", Current: finalSize, Total: finalSize, Done: true}:
		default:
		}
	}

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
	tmpDir, err := pullToDir(ctx, imageRef, nil)
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
func pullToDir(ctx context.Context, imageRef string, progressCh chan<- progress.Event) (string, error) {
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

	layers, err := downloadLayers(ctx, client, srcRef.Repository, manifest.Layers, progressCh)
	if err != nil {
		return "", err
	}
	defer cleanupLayers(layers)

	slog.Debug("pullToDir", slog.String("action", "extracting-layers"), slog.String("image", imageRef))

	tmpDir, err := os.MkdirTemp(env.BaseTempDir, "sandal-image-*")
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
//
// A bare relative filename like "ll.sqfs" is ambiguous: the dot makes it
// parse as an OCI registry reference, but if a real file with that name
// exists on disk the user almost certainly means the file. The local-file
// check below resolves that ambiguity in favour of the filesystem.
func IsImageReference(s string) bool {
	// Bare "." or ".." always means a local path.
	if s == "." || s == ".." {
		return false
	}
	// Must not start with / or ./ (clearly a file path)
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return false
	}

	// If a file with this name exists on disk, treat it as a local path
	// even when the string would otherwise parse as an image reference.
	// This is what makes `sandal run -lw ll.sqfs` use ./ll.sqfs instead of
	// trying to pull "ll.sqfs" from a registry.
	if _, err := os.Stat(s); err == nil {
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

// SanitizeRef converts an image reference to the cache filename (without
// the .sqfs extension) used by Pull. Exported so housekeeping code can map
// raw references (e.g. container config Lower entries) back to cached files.
func SanitizeRef(ref string) string {
	return sanitizeRef(ref)
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

// dirSize returns the total size of regular files in a directory tree.
// It only calls Lstat, so no file data is read.
func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// PullFromArgs scans command args for -lw values that look like OCI image references,
// pulls them on the host, converts to squashfs, and rewrites the args to use
// the local sqfs path. Returns the modified args.
func PullFromArgs(args []string, imageDir string, progressCh chan<- progress.Event) []string {
	result := make([]string, len(args))
	copy(result, args)

	for i := 0; i < len(result); i++ {
		if result[i] == "--" {
			break
		}
		if result[i] == "-lw" && i+1 < len(result) {
			i++
			argv := result[i]
			// Strip -lw target suffix (:/target) and :=sub modifier so
			// we check only the source part against IsImageReference.
			// This mirrors parseLowerArg in pkg/container/host/rootfs.go.
			src := argv
			src = strings.TrimSuffix(src, ":=sub")
			if idx := strings.LastIndex(src, ":/"); idx > 0 {
				src = src[:idx]
			}
			if !IsImageReference(src) {
				continue
			}
			slog.Info("pull", slog.String("action", "pulling-on-host"), slog.String("image", src))
			sqfsPath, err := Pull(context.Background(), src, imageDir, progressCh)
			if err != nil {
				slog.Error("pull", slog.String("image", src), slog.Any("error", err))
				continue
			}
			slog.Info("pull", slog.String("action", "cached"), slog.String("image", src), slog.String("path", sqfsPath))
			// Rewrite the arg, preserving any target suffix the user
			// supplied so -lw image:latest:/mnt still mounts at /mnt.
			suffix := argv[len(src):]
			result[i] = sqfsPath + suffix
		}
	}
	return result
}
