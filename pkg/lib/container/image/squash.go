package squash

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/container/registry"
	"github.com/ahmetozer/sandal/pkg/lib/progress"
	"github.com/ahmetozer/sandal/pkg/lib/zstd"
)

// Run pulls the source image for the given platform, squashes all layers
// into one, and pushes the result to the destination registry.
func Run(ctx context.Context, src, dst string, platform registry.Platform) error {
	srcRef, err := registry.ParseReference(src)
	if err != nil {
		return fmt.Errorf("parse source: %w", err)
	}

	slog.Info("Run", slog.String("action", "pulling"), slog.String("image", src), slog.String("os", platform.OS), slog.String("arch", platform.Architecture))

	client := registry.NewClient(srcRef.Registry)

	// Fetch and resolve the manifest for the target platform.
	manifest, err := resolveManifest(ctx, client, srcRef, platform)
	if err != nil {
		return err
	}

	// Fetch the image config.
	configData, err := fetchBlob(ctx, client, srcRef.Repository, manifest.Config.Digest)
	if err != nil {
		return fmt.Errorf("fetch config: %w", err)
	}
	var imgConfig registry.ImageConfig
	if err := json.Unmarshal(configData, &imgConfig); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}

	// Download and decompress all layers.
	layers, err := downloadLayers(ctx, client, srcRef.Repository, manifest.Layers, nil)
	if err != nil {
		return err
	}
	defer cleanupLayers(layers)

	// Flatten all layers into a single tar.
	slog.Debug("Run", slog.String("action", "squashing"), slog.String("image", src))
	var flatTar bytes.Buffer
	if err := Flatten(layers, &flatTar); err != nil {
		return fmt.Errorf("flatten: %w", err)
	}

	// Gzip compress the flattened tar.
	var compressedLayer bytes.Buffer
	gw, _ := gzip.NewWriterLevel(&compressedLayer, gzip.BestCompression)
	if _, err := io.Copy(gw, bytes.NewReader(flatTar.Bytes())); err != nil {
		return fmt.Errorf("compress layer: %w", err)
	}
	gw.Close()

	// Compute digests.
	layerDigest := "sha256:" + sha256hex(compressedLayer.Bytes())
	diffID := "sha256:" + sha256hex(flatTar.Bytes())

	// Build new config preserving the runtime settings.
	newConfig := registry.ImageConfig{
		Architecture: imgConfig.Architecture,
		OS:           imgConfig.OS,
		Config:       imgConfig.Config,
		RootFS: registry.RootFS{
			Type:    "layers",
			DiffIDs: []string{diffID},
		},
		History: []registry.History{{
			CreatedBy: "mybuild squash",
			Comment:   "squashed from " + src,
		}},
	}
	configJSON, _ := json.Marshal(newConfig)
	configDigest := "sha256:" + sha256hex(configJSON)

	// Build the new manifest.
	newManifest := registry.Manifest{
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
			Size:      int64(compressedLayer.Len()),
		}},
	}
	manifestJSON, _ := json.Marshal(newManifest)

	// Push to destination.
	dstRef, err := registry.ParseReference(dst)
	if err != nil {
		return fmt.Errorf("parse destination: %w", err)
	}

	dstClient := registry.NewClient(dstRef.Registry)

	slog.Info("Run", slog.String("action", "pushing"), slog.String("destination", dst))
	slog.Debug("Run", slog.String("action", "uploading-layer"), slog.Int64("sizeBytes", int64(compressedLayer.Len())))
	if err := dstClient.UploadBlob(ctx, dstRef.Repository, compressedLayer.Bytes(), layerDigest); err != nil {
		return fmt.Errorf("upload layer: %w", err)
	}

	slog.Debug("Run", slog.String("action", "uploading-config"))
	if err := dstClient.UploadBlob(ctx, dstRef.Repository, configJSON, configDigest); err != nil {
		return fmt.Errorf("upload config: %w", err)
	}

	slog.Debug("Run", slog.String("action", "uploading-manifest"))
	if err := dstClient.PutManifest(ctx, dstRef.Repository, dstRef.Ref(), manifestJSON, registry.MediaTypeOCIManifest); err != nil {
		return fmt.Errorf("upload manifest: %w", err)
	}

	slog.Info("Run", slog.String("action", "pushed"), slog.String("ref", dstRef.String()))
	return nil
}

// Export pulls the source image, squashes all layers, and writes
// the flattened filesystem as a tar stream to w.
func Export(ctx context.Context, src string, platform registry.Platform, w io.Writer) error {
	srcRef, err := registry.ParseReference(src)
	if err != nil {
		return fmt.Errorf("parse source: %w", err)
	}

	slog.Info("Export", slog.String("action", "pulling"), slog.String("image", src), slog.String("os", platform.OS), slog.String("arch", platform.Architecture))

	client := registry.NewClient(srcRef.Registry)

	manifest, err := resolveManifest(ctx, client, srcRef, platform)
	if err != nil {
		return err
	}

	layers, err := downloadLayers(ctx, client, srcRef.Repository, manifest.Layers, nil)
	if err != nil {
		return err
	}
	defer cleanupLayers(layers)

	slog.Debug("Export", slog.String("action", "squashing"), slog.String("image", src))
	if err := Flatten(layers, w); err != nil {
		return fmt.Errorf("flatten: %w", err)
	}

	slog.Info("Export", slog.String("action", "done"), slog.String("image", src))
	return nil
}

// resolveManifest fetches the manifest for the target platform.
// If the reference points to a manifest list/index, it resolves the
// platform-specific manifest.
func resolveManifest(ctx context.Context, client *registry.Client, ref registry.Reference, platform registry.Platform) (*registry.Manifest, error) {
	data, contentType, err := client.GetManifest(ctx, ref.Repository, ref.Ref())
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}

	// Check if this is a manifest list/index.
	if isIndex(contentType) {
		var index registry.Index
		if err := json.Unmarshal(data, &index); err != nil {
			return nil, fmt.Errorf("decode index: %w", err)
		}

		digest, err := matchPlatform(index, platform)
		if err != nil {
			return nil, err
		}

		// Fetch the platform-specific manifest.
		data, contentType, err = client.GetManifest(ctx, ref.Repository, digest)
		if err != nil {
			return nil, fmt.Errorf("fetch platform manifest: %w", err)
		}
	}

	if !isManifest(contentType) {
		return nil, fmt.Errorf("unexpected content type: %s", contentType)
	}

	var manifest registry.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}

	return &manifest, nil
}

// matchPlatform finds the descriptor in an index that matches the desired platform.
func matchPlatform(index registry.Index, platform registry.Platform) (string, error) {
	for _, d := range index.Manifests {
		if d.Platform == nil {
			continue
		}
		if d.Platform.OS == platform.OS && d.Platform.Architecture == platform.Architecture {
			if platform.Variant != "" && d.Platform.Variant != platform.Variant {
				continue
			}
			return d.Digest, nil
		}
	}
	return "", fmt.Errorf("no manifest found for platform %s/%s", platform.OS, platform.Architecture)
}

func isIndex(ct string) bool {
	return strings.Contains(ct, "manifest.list") || strings.Contains(ct, "image.index")
}

func isManifest(ct string) bool {
	return strings.Contains(ct, "manifest")
}

// fetchBlob downloads a blob and returns its contents.
func fetchBlob(ctx context.Context, client *registry.Client, repo, digest string) ([]byte, error) {
	rc, err := client.GetBlob(ctx, repo, digest)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// isFilesystemLayer returns true if the media type represents a container
// filesystem layer (tar, tar+gzip, or tar+zstd), as opposed to signatures,
// attestations, or other non-filesystem blobs.
func isFilesystemLayer(mediaType string) bool {
	switch mediaType {
	case
		"application/vnd.docker.image.rootfs.diff.tar.gzip",
		"application/vnd.docker.image.rootfs.diff.tar",
		"application/vnd.oci.image.layer.v1.tar+gzip",
		"application/vnd.oci.image.layer.v1.tar+zstd",
		"application/vnd.oci.image.layer.v1.tar",
		"application/vnd.oci.image.layer.nondistributable.v1.tar+gzip",
		"application/vnd.oci.image.layer.nondistributable.v1.tar+zstd",
		"application/vnd.oci.image.layer.nondistributable.v1.tar":
		return true
	}
	return false
}

// isZstdLayer returns true if the media type indicates zstd compression.
func isZstdLayer(mediaType string) bool {
	return strings.HasSuffix(mediaType, "+zstd")
}

// downloadLayers fetches all filesystem layer blobs, decompresses them
// (gzip or raw), and returns them as in-memory readers for Flatten.
// Non-filesystem layers (e.g. cosign signatures) are skipped with a warning.
func downloadLayers(ctx context.Context, client *registry.Client, repo string, descriptors []registry.Descriptor, progressCh chan<- progress.Event) ([]io.Reader, error) {
	// Filter to only filesystem layers.
	var fsLayers []registry.Descriptor
	for _, l := range descriptors {
		if isFilesystemLayer(l.MediaType) {
			fsLayers = append(fsLayers, l)
		} else {
			slog.Debug("downloadLayers", slog.String("action", "skipping"), slog.String("digest", l.Digest[:19]), slog.String("mediaType", l.MediaType))
		}
	}

	if len(fsLayers) == 0 {
		return nil, fmt.Errorf("image has no filesystem layers (found %d non-filesystem layers — this may be a signature or attestation image)", len(descriptors))
	}

	slog.Info("downloadLayers", slog.String("action", "downloading"), slog.Int("layers", len(fsLayers)))

	// Download and decompress each layer to a temp file to avoid
	// holding all uncompressed data in RAM simultaneously.
	// Limit concurrency to avoid memory pressure from many parallel
	// HTTP connections + decompression buffers on constrained devices.
	tmpFiles := make([]*os.File, len(fsLayers))
	errs := make([]error, len(fsLayers))
	sem := make(chan struct{}, 3)

	var wg sync.WaitGroup
	for i, l := range fsLayers {
		wg.Add(1)
		go func(i int, l registry.Descriptor) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			slog.Debug("downloadLayers", slog.Int("layer", i+1), slog.Int("total", len(fsLayers)), slog.String("digest", l.Digest[:19]), slog.Int64("sizeBytes", l.Size))

			rc, err := client.GetBlob(ctx, repo, l.Digest)
			if err != nil {
				errs[i] = fmt.Errorf("fetch layer %d: %w", i, err)
				return
			}

			// Wrap with progress tracking if a channel is provided.
			var src io.ReadCloser = rc
			if progressCh != nil {
				src = progress.NewReadCloser(rc, progressCh, progress.PhaseDownload,
					fmt.Sprintf("layer %d", i+1), l.Size)
			}

			// Stream download → decompress → temp file (no full layer in RAM).
			tmpFile, err := os.CreateTemp(env.BaseTempDir, "mybuild-layer-*.tar")
			if err != nil {
				src.Close()
				errs[i] = fmt.Errorf("create temp file for layer %d: %w", i, err)
				return
			}

			err = decompressLayerStream(src, tmpFile, l.MediaType)
			src.Close()
			if err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				errs[i] = fmt.Errorf("decompress layer %d: %w", i, err)
				return
			}

			// Seek back to start for later reading.
			if _, err := tmpFile.Seek(0, 0); err != nil {
				tmpFile.Close()
				os.Remove(tmpFile.Name())
				errs[i] = fmt.Errorf("seek layer %d: %w", i, err)
				return
			}

			tmpFiles[i] = tmpFile
		}(i, l)
	}
	wg.Wait()

	// Check for errors and clean up on failure.
	for _, err := range errs {
		if err != nil {
			for _, f := range tmpFiles {
				if f != nil {
					f.Close()
					os.Remove(f.Name())
				}
			}
			return nil, err
		}
	}

	// Build readers in order. Caller is responsible for cleanup via cleanupLayers.
	layers := make([]io.Reader, len(tmpFiles))
	for i, f := range tmpFiles {
		layers[i] = f
	}
	return layers, nil
}

// cleanupLayers closes and removes temp files used for layer storage.
func cleanupLayers(layers []io.Reader) {
	for _, r := range layers {
		if f, ok := r.(*os.File); ok {
			name := f.Name()
			f.Close()
			os.Remove(name)
		}
	}
}

// decompressLayerStream streams compressed data from src through a decompressor
// into dst, without buffering the entire layer in memory.
func decompressLayerStream(src io.Reader, dst io.Writer, mediaType string) error {
	if isZstdLayer(mediaType) {
		zr := zstd.NewReader(src)
		_, err := io.Copy(dst, zr)
		return err
	}

	// Try gzip. We need to peek at the first 2 bytes to check magic.
	var buf [2]byte
	n, err := io.ReadFull(src, buf[:])
	if err != nil && n == 0 {
		return err
	}

	// Reconstruct a reader with the peeked bytes prepended.
	combined := io.MultiReader(bytes.NewReader(buf[:n]), src)

	if n >= 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		// Gzip magic number detected.
		gr, err := gzip.NewReader(combined)
		if err != nil {
			return err
		}
		defer gr.Close()
		_, err = io.Copy(dst, gr)
		return err
	}

	// Not gzip — treat as raw tar.
	_, err = io.Copy(dst, combined)
	return err
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
