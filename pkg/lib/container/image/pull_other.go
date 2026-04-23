//go:build !linux

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

	"github.com/ahmetozer/sandal/pkg/lib/progress"
)

// mergeLayers merges OCI image layers into a single directory.
// On non-Linux platforms, layers are extracted and merged with direct
// OCI whiteout processing (no overlayfs available). The returned
// pathIndex carries the post-whiteout path set built from tar headers
// so the squashfs writer can enumerate entries without relying on
// os.ReadDir over the merged tmpDir.
func mergeLayers(layers []io.Reader, dir string, progressCh ...chan<- progress.Event) (*pathIndex, error) {
	idx := newPathIndex()
	for i, layer := range layers {
		slog.Debug("mergeLayers", slog.String("action", "applying"), slog.Int("layer", i+1), slog.Int("total", len(layers)))
		if err := applyLayerTar(layer, dir, idx); err != nil {
			return nil, fmt.Errorf("applying layer %d: %w", i, err)
		}
	}
	return idx, nil
}

// applyLayerTar extracts a tar layer directly into dir, processing OCI
// whiteout entries inline. Layers are applied in order so last layer wins.
// If idx is non-nil, each header is also recorded so the caller can
// assemble a post-whiteout path set across layers.
func applyLayerTar(layer io.Reader, dir string, idx *pathIndex) error {
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

		if idx != nil {
			idx.record(name, hdr.Typeflag)
		}

		nameDir := path.Dir(name)
		base := path.Base(name)

		// OCI opaque whiteout: delete all existing children of the directory.
		if base == ".wh..wh..opq" {
			targetDir := filepath.Join(dir, filepath.FromSlash(nameDir))
			removeDirectoryContents(targetDir)
			continue
		}

		// OCI file whiteout (.wh.<name>): delete a specific file/directory.
		if strings.HasPrefix(base, ".wh.") {
			targetName := base[len(".wh."):]
			target := filepath.Join(dir, filepath.FromSlash(nameDir), targetName)
			os.RemoveAll(target)
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
