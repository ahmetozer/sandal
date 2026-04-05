//go:build linux

package embedded

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

type exportRequest struct {
	Container  string   `json:"container"`
	OutputPath string   `json:"outputPath"`
	Includes   []string `json:"includes"`
	Excludes   []string `json:"excludes"`
}

// exportHandler exports the container's full filesystem as squashfs.
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

	rootfsDir := c.RootfsDir
	if _, err := os.Stat(rootfsDir); err != nil {
		http.Error(w, "rootfs not found (is the container running?)", http.StatusNotFound)
		return
	}

	outputPath := req.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(env.BaseSnapshotDir, c.Name+"-export.sqfs")
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		http.Error(w, "create output: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer outFile.Close()

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
		os.Remove(outputPath)
		http.Error(w, "squashfs writer: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := sqWriter.CreateFromDir(rootfsDir); err != nil {
		os.Remove(outputPath)
		http.Error(w, "export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"path": outputPath})
}
