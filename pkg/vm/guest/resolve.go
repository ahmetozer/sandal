package guest

import "github.com/ahmetozer/sandal/pkg/container/runtime"

// ResolvePath translates a host path to its VirtioFS mount location inside the VM.
// Mount spec format: tag=hostpath or tag=hostpath=guestpath.
// Paths that don't match any VirtioFS share are returned unchanged.
func ResolvePath(hostPath string) string {
	return runtime.ResolvePath(hostPath)
}
