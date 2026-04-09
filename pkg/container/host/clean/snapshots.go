//go:build linux || darwin

package clean

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
)

// PlanSnapshots returns Actions for every .sqfs file in
// env.BaseSnapshotDir that is not referenced by any container.
//
// Custom snapshot paths (c.Snapshot pointing outside BaseSnapshotDir)
// are never touched — they're user-owned and the safety guard will
// refuse them anyway.
func PlanSnapshots(usage UsageSet) []Action {
	dir := env.BaseSnapshotDir
	if ok, _ := IsInsideLibDir(dir); !ok {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var actions []Action
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sqfs") {
			continue
		}
		full := filepath.Join(dir, name)
		if _, used := usage.Snapshots[full]; used {
			continue
		}
		if ok, _ := IsInsideLibDir(full); !ok {
			continue
		}
		actions = append(actions, Action{
			Path:   full,
			Kind:   KindSnapshot,
			Reason: "not referenced by any container",
			Bytes:  fileSize(full),
		})
	}
	return actions
}
