//go:build linux || darwin

package clean

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
)

// PlanOrphans scans env.BaseChangeDir for entries that belong to
// containers whose state file has disappeared. Both the overlay
// directory form ("<name>") and the ext4 image form ("<name>.img")
// are handled.
//
// A changedir entry is an orphan when no container in usage.Containers
// claims it. The caller is responsible for populating usage.Containers
// with the set of containers that will *survive* cleanup, i.e. already
// excluding anything phase 1 is about to delete.
func PlanOrphans(usage UsageSet) []Action {
	dir := env.BaseChangeDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	// Safety gate: refuse to process anything if the change dir
	// itself isn't under LibDir (user may have redirected it
	// outside via SANDAL_CHANGE_DIR — not our business then).
	if ok, _ := IsInsideLibDir(dir); !ok {
		return nil
	}

	var actions []Action
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(dir, name)

		// Ensure this specific path still resolves inside LibDir
		// (defence in depth against symlink smuggling).
		if ok, _ := IsInsideLibDir(full); !ok {
			continue
		}

		container := strings.TrimSuffix(name, ".img")
		if _, alive := usage.Containers[container]; alive {
			continue
		}

		kind := KindOrphanChangeDir
		if strings.HasSuffix(name, ".img") {
			kind = KindOrphanChangeImg
		}
		actions = append(actions, Action{
			Path:   full,
			Kind:   kind,
			Reason: "state file missing",
			Bytes:  fileSize(full),
		})
	}
	return actions
}
