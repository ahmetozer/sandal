//go:build linux || darwin

package sandal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/ahmetozer/sandal/pkg/container/config"
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

	reqBody, _ := json.Marshal(map[string]any{
		"container": contName,
		"filePath":  filePath,
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

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["path"], nil
}
