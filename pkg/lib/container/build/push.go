package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
)

// PushOpts collects everything needed to push a built image as a single-
// layer OCI image to a registry.
type PushOpts struct {
	RootfsDir string                  // local on-disk rootfs to tar+gzip
	Tag       string                  // destination ref (e.g. registry/repo:tag)
	Config    registry.RuntimeConfig  // image RuntimeConfig
	History   []registry.History
	Platform  registry.Platform
}

// Push assembles a single-layer OCI image from RootfsDir and uploads it
// to the registry implied by Tag. Reuses the registry.Client primitives.
//
// Layers in OCI manifests reference the COMPRESSED layer digest; the
// rootfs.diff_ids array references the UNCOMPRESSED tar digest.
func Push(ctx context.Context, opts PushOpts) error {
	dstRef, err := registry.ParseReference(opts.Tag)
	if err != nil {
		return fmt.Errorf("parse destination: %w", err)
	}

	// 1. Tar + gzip the rootfs into a single-layer blob.
	uncompTar, err := tarDirectory(opts.RootfsDir)
	if err != nil {
		return fmt.Errorf("tar rootfs: %w", err)
	}

	var compressed bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if _, err := io.Copy(gw, bytes.NewReader(uncompTar)); err != nil {
		return fmt.Errorf("compress layer: %w", err)
	}
	gw.Close()

	layerDigest := "sha256:" + sha256hex(compressed.Bytes())
	diffID := "sha256:" + sha256hex(uncompTar)

	// 2. Build the OCI image config JSON.
	platform := opts.Platform
	if platform.OS == "" {
		platform.OS = "linux"
	}
	if platform.Architecture == "" {
		platform.Architecture = runtime.GOARCH
	}
	imgCfg := registry.ImageConfig{
		Architecture: platform.Architecture,
		OS:           platform.OS,
		Config:       opts.Config,
		RootFS: registry.RootFS{
			Type:    "layers",
			DiffIDs: []string{diffID},
		},
		History: opts.History,
	}
	configJSON, err := json.Marshal(imgCfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	configDigest := "sha256:" + sha256hex(configJSON)

	// 3. Build the OCI manifest.
	manifest := registry.Manifest{
		SchemaVersion: 2,
		MediaType:     registry.MediaTypeOCIManifest,
		Config: registry.Descriptor{
			MediaType: registry.MediaTypeOCIConfig,
			Digest:    configDigest,
			Size:      int64(len(configJSON)),
		},
		Layers: []registry.Descriptor{{
			MediaType: registry.MediaTypeOCILayer,
			Digest:    layerDigest,
			Size:      int64(compressed.Len()),
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// 4. Upload to destination registry.
	client := registry.NewClient(dstRef.Registry)

	slog.Info("Push", slog.String("action", "uploading-layer"),
		slog.Int("size", compressed.Len()), slog.String("digest", layerDigest[:19]))
	if err := client.UploadBlob(ctx, dstRef.Repository, compressed.Bytes(), layerDigest); err != nil {
		return fmt.Errorf("upload layer: %w", err)
	}
	slog.Info("Push", slog.String("action", "uploading-config"))
	if err := client.UploadBlob(ctx, dstRef.Repository, configJSON, configDigest); err != nil {
		return fmt.Errorf("upload config: %w", err)
	}
	slog.Info("Push", slog.String("action", "uploading-manifest"),
		slog.String("ref", dstRef.Ref()))
	if err := client.PutManifest(ctx, dstRef.Repository, dstRef.Ref(), manifestJSON, registry.MediaTypeOCIManifest); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}
	slog.Info("Push", slog.String("action", "pushed"), slog.String("ref", dstRef.String()))
	return nil
}

// tarDirectory walks rootDir and writes a tar stream of its contents.
// The tar is OCI-compatible: paths are relative, no leading slash, and
// directory metadata is emitted before file contents.
//
// We do NOT preserve uid/gid mappings beyond raw integers — builds run
// as root and produce root-owned content.
func tarDirectory(rootDir string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip the root itself; we want its contents.
		if path == rootDir {
			return nil
		}

		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		// OCI tar entries use slash-separated paths.
		rel = strings.ReplaceAll(rel, "\\", "/")

		var link string
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return fmt.Errorf("readlink %s: %w", rel, err)
			}
		}

		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		// Reset modtimes for reproducibility (could be made configurable).
		// hdr.ModTime = time.Time{}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, f); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
