//go:build linux || darwin

package clean

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
)

// PlanKernelCache returns Actions for stale initrd cache entries in
// env.BaseKernelDir. The cache is content-addressed by a hash of the
// sandal binary + base initrd (see pkg/vm/kernel/initrd.go), so every
// sandal rebuild creates a new file and the old entries become
// unreachable.
//
// Policy: keep the file with the newest mtime (interpreting "most
// recently used" as "most recently produced or touched"), delete the
// rest. vmlinuz-virt-* and initramfs-virt-* are Alpine-provided
// artifacts that require network to restore — we never touch them.
func PlanKernelCache() []Action {
	dir := env.BaseKernelDir
	if ok, _ := IsInsideLibDir(dir); !ok {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type candidate struct {
		path string
		size int64
		mtime int64
	}
	var cands []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Only our content-addressed initrds are candidates.
		if !strings.HasPrefix(name, "initramfs-sandal-") || !strings.HasSuffix(name, ".img") {
			continue
		}
		full := filepath.Join(dir, name)
		if ok, _ := IsInsideLibDir(full); !ok {
			continue
		}
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		cands = append(cands, candidate{
			path:  full,
			size:  info.Size(),
			mtime: info.ModTime().UnixNano(),
		})
	}
	if len(cands) <= 1 {
		return nil
	}

	// Find the newest entry to keep.
	newest := 0
	for i := 1; i < len(cands); i++ {
		if cands[i].mtime > cands[newest].mtime {
			newest = i
		}
	}

	var actions []Action
	for i, c := range cands {
		if i == newest {
			continue
		}
		actions = append(actions, Action{
			Path:   c.path,
			Kind:   KindKernelCache,
			Reason: "stale initrd cache entry",
			Bytes:  c.size,
		})
	}
	return actions
}
