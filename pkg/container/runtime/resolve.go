//go:build linux

package runtime

import (
	"os"
	"strings"
)

// ResolvePath translates a host path to its VirtioFS mount location inside the VM.
// Mount spec format: tag=hostpath or tag=hostpath=guestpath.
// Paths that don't match any VirtioFS share are returned unchanged.
func ResolvePath(hostPath string) string {
	if strings.HasPrefix(hostPath, "/mnt/") {
		return hostPath
	}

	mountSpec := os.Getenv("SANDAL_VM_MOUNTS")
	if mountSpec == "" {
		return hostPath
	}

	for _, entry := range strings.Split(mountSpec, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 3)
		if len(parts) < 2 {
			continue
		}
		shareDir := parts[1]
		guestBase := "/mnt" + shareDir
		if len(parts) == 3 && parts[2] != "" {
			guestBase = parts[2]
		}
		if hostPath == shareDir || strings.HasPrefix(hostPath, shareDir+"/") {
			rel := hostPath[len(shareDir):]
			return guestBase + rel
		}
	}

	return hostPath
}
