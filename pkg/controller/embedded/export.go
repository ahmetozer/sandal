//go:build linux

package embedded

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

// exportShareDir is a fixed staging directory under the sandal-lib virtiofs
// mount. The host's env.LibDir is mounted here at /var/lib/sandal by
// BuildVirtioFSMounts (pkg/sandal/vm.go), so any file written here is
// visible on the host at ${env.LibDir}/export/<name>.
const exportShareDir = "/var/lib/sandal/export"

type exportRequest struct {
	Container string   `json:"container"`
	Includes  []string `json:"includes"`
	Excludes  []string `json:"excludes"`
}

// exportHandler builds the container's rootfs as a squashfs image directly
// into the virtiofs-shared staging directory. The host CLI then renames the
// staged file onto the user's destination — no HTTP body bytes, no tmp file
// in the VM.
func exportHandler(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	c, err := getFirstContainer()
	if err != nil {
		http.Error(w, "container not found: "+err.Error(), http.StatusNotFound)
		return
	}
	if _, err := os.Stat(c.RootfsDir); err != nil {
		http.Error(w, "rootfs not found (is the container running?)", http.StatusNotFound)
		return
	}

	if err := os.MkdirAll(exportShareDir, 0o755); err != nil {
		http.Error(w, "mkdir share: "+err.Error(), http.StatusInternalServerError)
		return
	}
	name := c.Name + ".sqfs"
	stagingPath := filepath.Join(exportShareDir, name)

	outFile, err := os.Create(stagingPath)
	if err != nil {
		http.Error(w, "create staging: "+err.Error(), http.StatusInternalServerError)
		return
	}
	cleanup := func() {
		outFile.Close()
		os.Remove(stagingPath)
	}

	var opts []squashfs.WriterOption
	if len(req.Includes) > 0 || len(req.Excludes) > 0 {
		inc := req.Includes
		if len(inc) == 0 {
			inc = []string{"/"}
		}
		opts = append(opts, squashfs.WithPathFilter(
			squashfs.NewIncludeExcludeFilter(inc, req.Excludes),
		))
	}

	sqWriter, err := squashfs.NewWriter(outFile, opts...)
	if err != nil {
		cleanup()
		http.Error(w, "squashfs writer: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := sqWriter.CreateFromDir(c.RootfsDir); err != nil {
		cleanup()
		http.Error(w, "export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := outFile.Close(); err != nil {
		os.Remove(stagingPath)
		http.Error(w, "close staging: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"name": name})
}
