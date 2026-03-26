//go:build linux

package snapshot

import (
	"fmt"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/overlayfs"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
	"golang.org/x/sys/unix"
)

// FilterOptions controls which paths are included/excluded from the snapshot.
type FilterOptions struct {
	Includes []string // paths to include (default: everything if empty)
	Excludes []string // paths to exclude (takes priority over includes)
}

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

// resolveOutputPath determines the output file path for the snapshot.
func resolveOutputPath(c *config.Config, filePath string) (string, error) {
	if filePath == "" {
		filePath = c.Snapshot
	}
	if filePath == "" {
		if err := os.MkdirAll(env.BaseSnapshotDir, 0o755); err != nil {
			return "", fmt.Errorf("creating snapshot directory: %w", err)
		}
		filePath = path.Join(env.BaseSnapshotDir, c.Name+".sqfs")
	}
	return filePath, nil
}

// Create creates a squashfs snapshot from the container's upper workdir.
// filePath overrides the output path; if empty, c.Snapshot is used;
// if that is also empty, the default SANDAL_SNAPSHOT_DIR/<name>.sqfs is used.
// If a previous snapshot exists, it is merged with the current upper dir
// so that accumulated changes are preserved across successive snapshots.
func Create(c *config.Config, filePath string, filter FilterOptions) (string, error) {
	upperDir := overlayfs.GetChangeDir(c).GetUpper()
	if _, err := os.Stat(upperDir); err != nil {
		return "", fmt.Errorf("change directory not found: %w", err)
	}

	filePath, err := resolveOutputPath(c, filePath)
	if err != nil {
		return "", err
	}

	// Determine the source directory for squashfs creation.
	// If a previous snapshot exists, merge it with the current upper dir
	// using a read-only overlay so accumulated changes are preserved.
	sourceDir := upperDir
	prevSnapshot := Resolve(c)

	if prevSnapshot != "" {
		mergedDir, cleanup, err := mergeWithPrevious(prevSnapshot, upperDir)
		if err != nil {
			return "", fmt.Errorf("merging with previous snapshot: %w", err)
		}
		defer cleanup()
		sourceDir = mergedDir
	}

	// Merge sub-mount upper dirs into the source so that changes to
	// paths on separate partitions (e.g. /root) are included.
	subUppers := overlayfs.GetSubMountUpperDirs(c.ChangeDir)
	if len(subUppers) > 0 {
		mergedDir, cleanup, err := mergeSubMountUppers(sourceDir, subUppers)
		if err != nil {
			return "", fmt.Errorf("merging sub-mount uppers: %w", err)
		}
		defer cleanup()
		sourceDir = mergedDir
	}

	// Write to a temp file first, then rename. This avoids corrupting
	// the previous snapshot which may be mounted for the merge overlay.
	tmpFile, err := os.CreateTemp(path.Dir(filePath), ".snapshot-*.sqfs.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	var writerOpts []squashfs.WriterOption
	if len(filter.Includes) > 0 || len(filter.Excludes) > 0 {
		includes := filter.Includes
		if len(includes) == 0 {
			includes = []string{"/"}
		}
		writerOpts = append(writerOpts, squashfs.WithPathFilter(
			squashfs.NewIncludeExcludeFilter(includes, filter.Excludes),
		))
	}

	w, err := squashfs.NewWriter(tmpFile, writerOpts...)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(sourceDir); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("creating squashfs image: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("replacing snapshot file: %w", err)
	}

	return filePath, nil
}

// mergeSubMountUppers creates a temporary directory that combines the main
// source dir with all sub-mount upper directories at their correct relative
// paths. It bind-mounts the source, then overlays each sub-mount upper on top.
func mergeSubMountUppers(sourceDir string, subUppers []overlayfs.SubMountUpperDir) (string, func(), error) {
	tmpBase, err := os.MkdirTemp(env.RunDir, "snapshot-submount-merge-")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp dir: %w", err)
	}
	mergedDir := path.Join(tmpBase, "merged")
	if err := os.MkdirAll(mergedDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("creating merge dir: %w", err)
	}

	// Bind-mount the source as the base.
	if err := unix.Mount(sourceDir, mergedDir, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		os.Remove(mergedDir)
		return "", nil, fmt.Errorf("bind-mount source: %w", err)
	}

	var overlayMounts []string

	for _, su := range subUppers {
		target := path.Join(mergedDir, su.RelPath)
		if err := os.MkdirAll(target, 0o755); err != nil {
			continue
		}

		// Read-only overlay: sub-mount upper (higher priority) over existing target content.
		opts := fmt.Sprintf("lowerdir=%s:%s", su.UpperDir, target)
		if err := unix.Mount("overlay", target, "overlay", unix.MS_RDONLY, opts); err != nil {
			// If overlay fails (e.g. empty dirs), just skip.
			continue
		}
		overlayMounts = append(overlayMounts, target)
	}

	cleanup := func() {
		for i := len(overlayMounts) - 1; i >= 0; i-- {
			unix.Unmount(overlayMounts[i], unix.MNT_DETACH)
		}
		unix.Unmount(mergedDir, unix.MNT_DETACH)
		os.Remove(mergedDir)
		os.Remove(tmpBase)
	}

	return mergedDir, cleanup, nil
}

// mergeWithPrevious mounts the previous snapshot and the current upper dir
// as two lower layers in a read-only overlay, returning the merged mount path
// and a cleanup function.
func mergeWithPrevious(prevSnapshotFile, upperDir string) (string, func(), error) {
	tmpBase := path.Join(env.RunDir, "snapshot-merge")

	mergedDir := path.Join(tmpBase, "merged")
	if err := os.MkdirAll(mergedDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("creating merge dir: %w", err)
	}

	// Mount previous snapshot squashfs
	img, err := diskimage.Mount(prevSnapshotFile)
	if err != nil {
		return "", nil, fmt.Errorf("mounting previous snapshot: %w", err)
	}

	// Read-only overlay: upperDir (higher priority) over previous snapshot (lower priority)
	options := fmt.Sprintf("lowerdir=%s:%s", upperDir, img.MountDir)
	if err := unix.Mount("overlay", mergedDir, "overlay", unix.MS_RDONLY, options); err != nil {
		diskimage.Umount(&img)
		return "", nil, fmt.Errorf("mounting merge overlay: %w", err)
	}

	cleanup := func() {
		unix.Unmount(mergedDir, 0)
		os.Remove(mergedDir)
		os.Remove(tmpBase)
		diskimage.Umount(&img)
	}

	return mergedDir, cleanup, nil
}
