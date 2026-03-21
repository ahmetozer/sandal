//go:build linux

package cruntime

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"golang.org/x/sys/unix"
)

// subMount represents a mount point discovered inside a lower directory.
type subMount struct {
	// HostPath is the absolute path on the host (e.g. /root).
	HostPath string
	// RelPath is the path relative to the lower directory (e.g. root).
	RelPath string
}

// supportedFSTypes lists real filesystem types that should be included
// when discovering sub-mounts.
var supportedFSTypes = map[string]bool{
	"ext2":  true,
	"ext3":  true,
	"ext4":  true,
	"xfs":   true,
	"btrfs": true,
	"zfs":   true,
	"f2fs":  true,
	"ntfs":  true,
	"vfat":  true,
	"exfat": true,
	"hfs":   true,
	"hfsplus": true,
	"apfs":  true,
	"bcachefs": true,
}

// findSubMounts parses /proc/self/mountinfo and returns all real filesystem
// mounts that are children of lowerDir. Results are sorted by path depth
// (shallowest first).
func findSubMounts(lowerDir string) ([]subMount, error) {
	lowerDir = filepath.Clean(lowerDir)

	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("opening mountinfo: %w", err)
	}
	defer f.Close()

	prefix := lowerDir
	if prefix != "/" {
		prefix += "/"
	}

	var mounts []subMount
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		mountPoint, fsType, err := parseMountinfoLine(line)
		if err != nil {
			continue
		}

		if !supportedFSTypes[fsType] {
			continue
		}

		if mountPoint == lowerDir {
			continue
		}
		if !strings.HasPrefix(mountPoint, prefix) {
			continue
		}

		rel, err := filepath.Rel(lowerDir, mountPoint)
		if err != nil {
			continue
		}

		mounts = append(mounts, subMount{
			HostPath: mountPoint,
			RelPath:  rel,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading mountinfo: %w", err)
	}

	sort.Slice(mounts, func(i, j int) bool {
		di := strings.Count(mounts[i].RelPath, "/")
		dj := strings.Count(mounts[j].RelPath, "/")
		if di != dj {
			return di < dj
		}
		return mounts[i].RelPath < mounts[j].RelPath
	})

	slog.Debug("findSubMounts", slog.String("lowerDir", lowerDir), slog.Int("count", len(mounts)))
	for _, m := range mounts {
		slog.Debug("findSubMounts", slog.String("host", m.HostPath), slog.String("rel", m.RelPath))
	}

	return mounts, nil
}

// mountSubMountOverlays discovers sub-mounts under each host directory lowerdir
// and creates a mini-overlay for each one on top of the main rootfs. Each
// mini-overlay uses the sub-mount as its lowerdir and a subdirectory under
// the main change dir as its upperdir/workdir, giving true COW behavior.
//
// Paths already covered by user-specified volumes are skipped.
func mountSubMountOverlays(c *config.Config, hostDirs []string, changeBase string) error {
	// Build a set of volume destinations for conflict detection.
	volDests := make(map[string]bool)
	for _, v := range c.Volumes {
		parts := strings.SplitN(v, ":", 3)
		if len(parts) >= 2 {
			volDests[filepath.Clean(parts[1])] = true
		} else if len(parts) == 1 {
			volDests[filepath.Clean(parts[0])] = true
		}
	}

	for _, dir := range hostDirs {
		subs, err := findSubMounts(dir)
		if err != nil {
			slog.Warn("mountSubMountOverlays: discovery failed", slog.String("dir", dir), slog.Any("error", err))
			continue
		}

		for _, sm := range subs {
			containerRel := "/" + sm.RelPath
			if volDests[containerRel] {
				slog.Debug("mountSubMountOverlays: skipping (volume exists)", slog.String("path", containerRel))
				continue
			}

			// Create upper/work dirs for this sub-mount's mini-overlay.
			safeName := strings.ReplaceAll(sm.RelPath, "/", "_")
			upper := filepath.Join(changeBase, "submount-upper", safeName, "upper")
			work := filepath.Join(changeBase, "submount-upper", safeName, "work")
			if err := os.MkdirAll(upper, 0o755); err != nil {
				slog.Warn("mountSubMountOverlays: mkdir upper failed", slog.String("path", upper), slog.Any("error", err))
				continue
			}
			if err := os.MkdirAll(work, 0o755); err != nil {
				slog.Warn("mountSubMountOverlays: mkdir work failed", slog.String("path", work), slog.Any("error", err))
				continue
			}

			target := filepath.Join(c.RootfsDir, sm.RelPath)
			if err := os.MkdirAll(target, 0o755); err != nil {
				slog.Warn("mountSubMountOverlays: mkdir target failed", slog.String("target", target), slog.Any("error", err))
				continue
			}

			opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", sm.HostPath, upper, work)
			if err := unix.Mount("overlay", target, "overlay", 0, opts); err != nil {
				slog.Warn("mountSubMountOverlays: overlay mount failed", slog.String("target", target), slog.String("opts", opts), slog.Any("error", err))
				continue
			}

			slog.Debug("mountSubMountOverlays: mounted", slog.String("src", sm.HostPath), slog.String("target", target))
			subMountRegistry[c.Name] = append(subMountRegistry[c.Name], target)
		}
	}
	return nil
}

// subMountRegistry tracks mini-overlay mount paths per container for cleanup.
var subMountRegistry = map[string][]string{}

// unmountSubMountOverlays unmounts all mini-overlays for a container in reverse order.
func unmountSubMountOverlays(name string) []error {
	targets, ok := subMountRegistry[name]
	if !ok {
		return nil
	}
	var errs []error
	for i := len(targets) - 1; i >= 0; i-- {
		if err := unix.Unmount(targets[i], unix.MNT_DETACH); err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("unmount sub-overlay %s: %w", targets[i], err))
			}
		}
	}
	delete(subMountRegistry, name)
	return errs
}

// parseMountinfoLine extracts the mount point and filesystem type from a
// /proc/self/mountinfo line.
func parseMountinfoLine(line string) (mountPoint string, fsType string, err error) {
	parts := strings.SplitN(line, " - ", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("no separator found")
	}

	leftFields := strings.Fields(parts[0])
	if len(leftFields) < 5 {
		return "", "", fmt.Errorf("too few fields before separator")
	}
	mountPoint = unescapeMountinfo(leftFields[4])

	rightFields := strings.Fields(parts[1])
	if len(rightFields) < 1 {
		return "", "", fmt.Errorf("no fstype after separator")
	}
	fsType = rightFields[0]

	return mountPoint, fsType, nil
}

// unescapeMountinfo handles octal escape sequences in mountinfo paths.
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			o1, o2, o3 := s[i+1]-'0', s[i+2]-'0', s[i+3]-'0'
			if o1 <= 7 && o2 <= 7 && o3 <= 7 {
				b.WriteByte(o1*64 + o2*8 + o3)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
