//go:build linux

package diskimage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
)

// normRun collapses the /var/run -> /run symlink so mountpoint strings from
// /proc/mounts (which the kernel reports as /run/...) compare equal to config/
// option strings (which sandal writes as /var/run/...).
func normRun(p string) string {
	if rest, ok := strings.CutPrefix(p, "/var/run/"); ok {
		return "/run/" + rest
	}
	return p
}

// loopReuseMatch reports whether an existing loop's geometry is a valid reuse
// target for a desired mount. Match key = (backing file, offset, sizelimit);
// a deleted backing (image replaced since attach) is never reused.
func loopReuseMatch(haveFile string, haveDeleted bool, haveOff, haveSize uint64, wantFile string, wantOff, wantSize uint64) bool {
	if haveDeleted {
		return false
	}
	return haveFile == wantFile && haveOff == wantOff && haveSize == wantSize
}

// parseImmutableMounts returns loopNo -> mountpoint for every mount directly
// under immBase. The loop number is the mountpoint basename (sandal names
// immutable mount dirs by loop number). Paths are normalized for /var/run.
func parseImmutableMounts(procMounts, immBase string) map[int]string {
	base := normRun(filepath.Clean(immBase))
	out := map[int]string{}
	for _, line := range strings.Split(procMounts, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		mp := f[1]
		norm := normRun(filepath.Clean(mp))
		if filepath.Dir(norm) != base {
			continue
		}
		no, err := strconv.Atoi(filepath.Base(norm))
		if err != nil {
			continue
		}
		out[no] = mp
	}
	return out
}

// overlayReferencesImmutable reports whether any overlay mount in procMounts
// has a lowerdir entry referencing mountDir (normalized). Used as the teardown
// guard: a shared immutable must stay mounted while a sibling overlay uses it.
func overlayReferencesImmutable(mountDir, procMounts string) bool {
	want := normRun(filepath.Clean(mountDir))
	for _, line := range strings.Split(procMounts, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[2] != "overlay" {
			continue
		}
		for _, opt := range strings.Split(f[3], ",") {
			val, ok := strings.CutPrefix(opt, "lowerdir=")
			if !ok {
				continue
			}
			for _, d := range strings.Split(val, ":") {
				if normRun(filepath.Clean(d)) == want {
					return true
				}
			}
		}
	}
	return false
}

func readProcMounts() string {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return ""
	}
	return string(data)
}

// findMountedImmutable looks for an already-mounted immutable image whose loop
// device backs the same file at the same offset/sizelimit, so it can be reused
// instead of allocating a new loop. Returns the existing mount dir + loop number.
func findMountedImmutable(wantFile string, wantOff, wantSize uint64) (mountDir string, loopNo int, found bool) {
	// The kernel records the loop's backing_file as an absolute path, so compare
	// against the absolute form (a -lw path may be relative to cwd).
	want := filepath.Clean(wantFile)
	if abs, err := filepath.Abs(want); err == nil {
		want = abs
	}
	for no, mp := range parseImmutableMounts(readProcMounts(), env.BaseImmutableImageDir) {
		path, deleted := loopdev.BackingRaw(no)
		if loopReuseMatch(path, deleted, loopdev.Offset(no), loopdev.SizeLimit(no), want, wantOff, wantSize) {
			return mp, no, true
		}
	}
	return "", 0, false
}

// ImmutableInUse reports whether a live overlay still references mountDir as a
// lowerdir. Teardown uses it to avoid unmounting a base image shared with a
// still-running container.
func ImmutableInUse(mountDir string) bool {
	return overlayReferencesImmutable(mountDir, readProcMounts())
}
