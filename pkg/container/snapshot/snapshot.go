//go:build linux

package snapshot

import (
	"fmt"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/overlayfs"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

// Resolve returns the snapshot file path for the container if one exists.
// It checks c.Snapshot first, then the default location. Returns "" if no snapshot exists.
func Resolve(c *config.Config) string {
	if c.Snapshot != "" {
		if _, err := os.Stat(c.Snapshot); err == nil {
			return c.Snapshot
		}
	}
	defaultPath := path.Join(env.BaseSnapshotDir, c.Name+".sqfs")
	if _, err := os.Stat(defaultPath); err == nil {
		return defaultPath
	}
	return ""
}

// Create creates a squashfs snapshot from the container's upper workdir.
// filePath overrides the output path; if empty, c.Snapshot is used;
// if that is also empty, the default SANDAL_SNAPSHOT_DIR/<name>.sqfs is used.
func Create(c *config.Config, filePath string) (string, error) {
	upperDir := overlayfs.GetChangeDir(c).GetUpper()
	if _, err := os.Stat(upperDir); err != nil {
		return "", fmt.Errorf("change directory not found: %w", err)
	}

	if filePath == "" {
		filePath = c.Snapshot
	}
	if filePath == "" {
		if err := os.MkdirAll(env.BaseSnapshotDir, 0o755); err != nil {
			return "", fmt.Errorf("creating snapshot directory: %w", err)
		}
		filePath = path.Join(env.BaseSnapshotDir, c.Name+".sqfs")
	}

	outFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	w, err := squashfs.NewWriter(outFile)
	if err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(upperDir); err != nil {
		os.Remove(filePath)
		return "", fmt.Errorf("creating squashfs image: %w", err)
	}

	return filePath, nil
}
