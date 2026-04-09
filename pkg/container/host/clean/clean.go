//go:build linux || darwin

package clean

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
)

// UsageSet captures everything currently referenced by at least one
// container state file. Anything outside the set is a cleanup
// candidate.
type UsageSet struct {
	// Images: absolute paths of .sqfs files under env.BaseImageDir
	// that are in use.
	Images map[string]struct{}
	// Snapshots: absolute paths referenced by any container.
	Snapshots map[string]struct{}
	// Containers: names of containers whose state file currently
	// exists (whether running or not).
	Containers map[string]struct{}
}

// BuildUsageSet walks the supplied container configs and builds the
// set of artifacts that must be preserved. Callers should exclude
// containers that are about to be deleted so that scanners treat
// their artifacts as reclaimable.
func BuildUsageSet(conts []*config.Config) UsageSet {
	set := UsageSet{
		Images:     map[string]struct{}{},
		Snapshots:  map[string]struct{}{},
		Containers: map[string]struct{}{},
	}
	for _, c := range conts {
		if c == nil {
			continue
		}
		set.Containers[c.Name] = struct{}{}

		// Resolved images (post-mount). These are the authoritative
		// paths; they may live anywhere on disk, but we only care
		// about entries under BaseImageDir for cleanup purposes.
		for _, im := range c.ImmutableImages {
			if im.File == "" {
				continue
			}
			if abs, err := filepath.Abs(im.File); err == nil {
				set.Images[abs] = struct{}{}
			}
		}
		// Raw -lw refs — map each back to the cache filename so we
		// protect images that belong to containers that haven't been
		// fully started yet.
		for _, ref := range c.Lower {
			cachePath := filepath.Join(env.BaseImageDir, squash.SanitizeRef(ref)+".sqfs")
			set.Images[cachePath] = struct{}{}
		}

		// Snapshots: both the explicit c.Snapshot path (if set) and
		// the default-location snapshot file.
		if c.Snapshot != "" {
			if abs, err := filepath.Abs(c.Snapshot); err == nil {
				set.Snapshots[abs] = struct{}{}
			}
		}
		defaultSnap := filepath.Join(env.BaseSnapshotDir, c.Name+".sqfs")
		set.Snapshots[defaultSnap] = struct{}{}
	}
	return set
}

// fileSize returns the on-disk size of path, or the recursive tree
// size if path is a directory. Best-effort: errors are logged and
// zero returned.
func fileSize(p string) int64 {
	info, err := os.Lstat(p)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.Walk(p, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total
}

// removeSafe deletes path after verifying it resolves inside
// env.LibDir. Returns true on success, false (with a log line) if the
// path is outside LibDir or the removal fails.
func removeSafe(p string) bool {
	ok, err := IsInsideLibDir(p)
	if err != nil {
		slog.Warn("clean: safety check failed", "path", p, "err", err)
		return false
	}
	if !ok {
		slog.Warn("clean: refusing to delete path outside SANDAL_LIB_DIR", "path", p)
		return false
	}
	if err := os.RemoveAll(p); err != nil {
		slog.Warn("clean: remove failed", "path", p, "err", err)
		return false
	}
	return true
}

// Apply prints (dryRun=true) or performs the given actions. Returns
// the count applied and the total bytes reclaimed.
func Apply(actions []Action, dryRun bool) (count int, bytes int64) {
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Kind != actions[j].Kind {
			return actions[i].Kind < actions[j].Kind
		}
		return actions[i].Path < actions[j].Path
	})
	for _, a := range actions {
		if dryRun {
			fmt.Printf("would remove [%s] %s  (%s, %s)\n", a.Kind, a.Path, humanBytes(a.Bytes), a.Reason)
			count++
			bytes += a.Bytes
			continue
		}
		if removeSafe(a.Path) {
			fmt.Printf("removed [%s] %s  (%s, %s)\n", a.Kind, a.Path, humanBytes(a.Bytes), a.Reason)
			count++
			bytes += a.Bytes
		}
	}
	return count, bytes
}

// humanBytes formats a byte count compactly for report output.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
