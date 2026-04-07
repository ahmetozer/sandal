//go:build linux || darwin

package sandal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/coreutils"
	"github.com/ahmetozer/sandal/pkg/vm/mgmt"
)

// Snapshot dispatches snapshot to the native path or VM management socket.
// The caller is responsible for looking up the container config.
func Snapshot(c *config.Config, filePath string, includes, excludes []string) (string, error) {
	if c.VM != "" {
		return snapshotViaMgmt(c.Name, filePath, includes, excludes)
	}
	return snapshotNative(c, filePath, includes, excludes)
}

func snapshotViaMgmt(contName, filePath string, includes, excludes []string) (string, error) {
	client, err := mgmt.NewHTTPClient(contName)
	if err != nil {
		return "", fmt.Errorf("management socket for %q: %w", contName, err)
	}

	// Never forward filePath — the in-VM handler would resolve it against
	// the VM's filesystem. Instead let it write to the canonical
	// virtiofs-shared location, then move on the host.
	reqBody, _ := json.Marshal(map[string]any{
		"container": contName,
		"includes":  includes,
		"excludes":  excludes,
	})

	resp, err := client.Post("http://unix/snapshot", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("snapshot request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("snapshot failed: %s", string(body))
	}

	// Drain body for wire compat; we compute the staging path ourselves
	// from the known mapping between /var/lib/sandal (VM) and env.LibDir
	// (host) set up by BuildVirtioFSMounts.
	var result map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&result)

	stagingPath := filepath.Join(env.BaseSnapshotDir, contName+".sqfs")

	if filePath == "" {
		return stagingPath, nil
	}
	if err := coreutils.Mv(stagingPath, filePath); err != nil {
		return "", fmt.Errorf("move snapshot to %s: %w", filePath, err)
	}
	return filePath, nil
}
