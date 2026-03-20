package squash

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
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

	slog.Debug("Pull", slog.String("action", "flattening"), slog.String("image", imageRef))

	// Flatten layers into a tar stream, then extract to a temp directory
	// so we can create squashfs from it.
	pr, pw := io.Pipe()
	flattenErr := make(chan error, 1)
	go func() {
		flattenErr <- Flatten(layers, pw)
		pw.Close()
	}()

	tmpDir, err := os.MkdirTemp("", "sandal-image-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := extractTar(pr, tmpDir); err != nil {
		return "", fmt.Errorf("extracting flattened image: %w", err)
	}
	if err := <-flattenErr; err != nil {
		return "", fmt.Errorf("flattening layers: %w", err)
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

	slog.Debug("pullToDir", slog.String("action", "flattening"), slog.String("image", imageRef))

	pr, pw := io.Pipe()
	flattenErr := make(chan error, 1)
	go func() {
		flattenErr <- Flatten(layers, pw)
		pw.Close()
	}()

	tmpDir, err := os.MkdirTemp("", "sandal-image-*")
	if err != nil {
		return "", fmt.Errorf("creating temp directory: %w", err)
	}

	if err := extractTar(pr, tmpDir); err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("extracting flattened image: %w", err)
	}
	if err := <-flattenErr; err != nil {
		os.RemoveAll(tmpDir)
		return "", fmt.Errorf("flattening layers: %w", err)
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

// extractTar extracts a tar stream into a directory.
func extractTar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dir, filepath.Clean(hdr.Name))

		// Prevent path traversal
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) && target != filepath.Clean(dir) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
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
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Remove(target) // remove if exists
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			linkTarget := filepath.Join(dir, filepath.Clean(hdr.Linkname))
			os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		case tar.TypeChar, tar.TypeBlock:
			// Skip device nodes — they require CAP_MKNOD and are
			// typically not needed for container images used as rootfs.
			continue
		case tar.TypeFifo:
			continue
		}
	}
}
