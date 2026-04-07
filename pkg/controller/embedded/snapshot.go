//go:build linux

package embedded

import (
	"encoding/json"
	"net/http"

	"github.com/ahmetozer/sandal/pkg/container/snapshot"
)

type snapshotRequest struct {
	Container string   `json:"container"`
	Includes  []string `json:"includes"`
	Excludes  []string `json:"excludes"`
}

// snapshotHandler creates a snapshot of the container's changes.
//
// The output path is always the canonical default
// (${env.BaseSnapshotDir}/<name>.sqfs), which lives under env.LibDir and is
// therefore visible on the host through the sandal-lib virtiofs share. The
// host CLI is responsible for moving the file to a user-supplied -f
// destination after this handler returns.
func snapshotHandler(w http.ResponseWriter, r *http.Request) {
	var req snapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}

	outPath, err := snapshot.Create(c, "", snapshot.FilterOptions{
		Includes: req.Includes,
		Excludes: req.Excludes,
	})
	if err != nil {
		http.Error(w, "snapshot failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"path": outPath})
}
